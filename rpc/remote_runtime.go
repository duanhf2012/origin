package rpc

import (
	"context"
	"errors"
	"sort"
	"sync"
	"time"

	"github.com/duanhf2012/origin/v3/errs"
	"github.com/duanhf2012/origin/v3/internal/tcpnet"
	"github.com/duanhf2012/origin/v3/internal/timerwheel"
	originlog "github.com/duanhf2012/origin/v3/log"
)

const (
	// maxRemoteTargets 给 M13 的显式 Node 地址表设置固定安全边界。
	maxRemoteTargets = 4096
	// reconnectInitialDelay 在远端尚未启动时保持快速恢复。
	reconnectInitialDelay = 200 * time.Millisecond
	// reconnectMaximumDelay 防止长期故障形成高频 Dial 风暴。
	reconnectMaximumDelay = 5 * time.Second
	// heartbeatDivisor 在一个 ReadTimeout 内至少产生两次探测机会。
	heartbeatDivisor = 3
)

// remoteRuntime 只管理一个 Node 的 TCP RPC 冷路径资源。
//
// 本地 RPC 未配置 TCP 时 Runtime.remote 为 nil，不承担连接表、DeadlineQueue 或 goroutine
// 成本。targets 由 Application 或未来 Discovery 显式维护，不扫描进程中的其他 Runtime。
type remoteRuntime struct {
	owner  *Runtime
	config Config

	mu       sync.Mutex
	started  bool
	stopping bool
	listener *tcpnet.Listener
	inbound  *inboundHandler
	targets  map[string]*remoteTarget

	deadlineQueue    *timerwheel.DeadlineQueue
	deadlineBindings map[timerwheel.DeadlineID]context.CancelCauseFunc
	deadlineDone     chan struct{}
}

// newRemoteRuntime 创建尚未启动、没有后台 goroutine 的远端资源容器。
func newRemoteRuntime(owner *Runtime, config Config) *remoteRuntime {
	return &remoteRuntime{
		owner:            owner,
		config:           config,
		targets:          make(map[string]*remoteTarget),
		deadlineBindings: make(map[timerwheel.DeadlineID]context.CancelCauseFunc),
		deadlineDone:     make(chan struct{}),
	}
}

// StartNetwork 在 Node 时间轮已经运行后启动 Listener、Deadline watcher 和已知目标。
func (runtime *Runtime) StartNetwork(engine *timerwheel.Engine) error {
	if runtime == nil || engine == nil {
		return errs.ErrInvalidArgument
	}
	if runtime.remote == nil {
		return nil
	}
	return runtime.remote.start(engine)
}

// AdvertiseAddress 返回当前 Runtime 冻结的可连接地址。
func (runtime *Runtime) AdvertiseAddress() (string, bool) {
	if runtime == nil || runtime.remote == nil {
		return "", false
	}
	return runtime.remote.config.TCP.Advertise, true
}

// start 一次性发布远端 RPC 资源。
func (remote *remoteRuntime) start(engine *timerwheel.Engine) error {
	remote.mu.Lock()
	if remote.started || remote.stopping {
		remote.mu.Unlock()
		return errs.ErrInvalidArgument
	}

	// DeadlineQueue 必须先于 Listener 建立；入站 Request 一旦可读就能够登记唯一超时。
	queue, err := engine.NewDeadlineQueue()
	if err != nil {
		remote.mu.Unlock()
		return err
	}
	remote.deadlineQueue = queue
	remote.inbound = newInboundHandler(remote)
	options := remote.listenOptions()
	listener, err := tcpnet.Listen(
		remote.config.TCP.Listen,
		options,
		remote.inbound,
	)
	if err != nil {
		remote.deadlineQueue = nil
		remote.inbound = nil
		remote.mu.Unlock()
		queue.Close()
		return err
	}

	// started 在目标 goroutine 启动前发布；AddTarget 此后会自行启动新增目标。
	remote.listener = listener
	remote.started = true
	targets := make([]*remoteTarget, 0, len(remote.targets))
	for _, target := range remote.targets {
		targets = append(targets, target)
	}
	remote.mu.Unlock()

	go remote.watchDeadlines(queue)
	for _, target := range targets {
		target.start()
	}
	return nil
}

// AddTarget 新增一个由外部地址源确认的 Node 连接目标。
//
// 相同 NodeID 和地址重复登记是幂等操作；同一 NodeID 的不同地址不会替换现有连接，
// 避免误启动实例抢占正在工作的目标。
func (runtime *Runtime) AddTarget(nodeID, address string) error {
	if runtime == nil || runtime.remote == nil ||
		!validWireName(nodeID) || nodeID == runtime.nodeID {
		return errs.ErrInvalidArgument
	}
	if err := validateAdvertiseAddress(address); err != nil {
		return err
	}

	remote := runtime.remote
	remote.mu.Lock()
	if remote.stopping || runtime.closed.Load() {
		remote.mu.Unlock()
		return errs.ErrServiceStopped
	}
	if current, exists := remote.targets[nodeID]; exists {
		if current.address == address {
			remote.mu.Unlock()
			return nil
		}
		remote.mu.Unlock()
		return errs.NewMessage(
			errs.CodeTransportProtocol,
			"相同 NodeID 已经绑定到其他 RPC 地址",
		)
	}
	if len(remote.targets) >= maxRemoteTargets {
		remote.mu.Unlock()
		return errs.ErrTransportOverloaded
	}
	target := newRemoteTarget(remote, nodeID, address)
	remote.targets[nodeID] = target
	started := remote.started
	remote.mu.Unlock()

	if started {
		target.start()
	}
	return nil
}

// RemoveTarget 删除与地址仍匹配的目标，并等待该目标连接管理 goroutine 退出。
//
// 地址条件防止旧服务发现事件误删同 NodeID 的后续地址；M13 不允许地址原地替换，但该
// 条件为未来 Discovery 保留确定的所有权边界。
func (runtime *Runtime) RemoveTarget(
	ctx context.Context,
	nodeID string,
	address string,
) error {
	if runtime == nil || runtime.remote == nil || ctx == nil {
		return errs.ErrInvalidArgument
	}
	remote := runtime.remote
	remote.mu.Lock()
	target, exists := remote.targets[nodeID]
	if !exists || target.address != address {
		remote.mu.Unlock()
		return nil
	}
	delete(remote.targets, nodeID)
	remote.mu.Unlock()
	return target.stop(ctx)
}

// BeginStop 立即拒绝新 RPC、关闭 Listener 和出站连接，但保留入站已接受任务的 Deadline。
//
// 目标 Service 随后的 Scheduler Drain 会执行完已经进入 FIFO 的任务；最终 Close 再关闭
// DeadlineQueue。这样调用方断线不会撤回已经被服务端接受的写操作。
func (runtime *Runtime) BeginStop(ctx context.Context) error {
	if runtime == nil || ctx == nil {
		return errs.ErrInvalidArgument
	}
	runtime.closed.Store(true)
	if runtime.remote == nil {
		return nil
	}
	return runtime.remote.beginStop(ctx)
}

// beginStop 停止所有网络准入，并聚合资源等待错误。
func (remote *remoteRuntime) beginStop(ctx context.Context) error {
	remote.mu.Lock()
	if remote.stopping {
		listener := remote.listener
		remote.mu.Unlock()
		if listener == nil {
			return nil
		}
		return listener.Close(ctx)
	}
	remote.stopping = true
	listener := remote.listener
	targets := make([]*remoteTarget, 0, len(remote.targets))
	for _, target := range remote.targets {
		targets = append(targets, target)
	}
	remote.mu.Unlock()

	var result error
	for _, target := range targets {
		result = errors.Join(result, target.stop(ctx))
	}
	if listener != nil {
		result = errors.Join(result, listener.Close(ctx))
	}
	return result
}

// closeDeadlines 在所有 Service 已排空后关闭远端 DeadlineQueue 和 watcher。
func (remote *remoteRuntime) closeDeadlines() {
	remote.mu.Lock()
	queue := remote.deadlineQueue
	if queue == nil {
		remote.mu.Unlock()
		return
	}
	remote.deadlineQueue = nil

	// Queue.Close 不回调每个 ID；先取得绑定并在锁外取消 Context。
	cancels := make([]context.CancelCauseFunc, 0, len(remote.deadlineBindings))
	for _, cancel := range remote.deadlineBindings {
		cancels = append(cancels, cancel)
	}
	clear(remote.deadlineBindings)
	done := remote.deadlineDone
	remote.mu.Unlock()

	queue.Close()
	for _, cancel := range cancels {
		cancel(errs.ErrServiceStopped)
	}
	<-done
}

// publicCatalog 返回按 ServiceName 排序的稳定公开目录。
func (remote *remoteRuntime) publicCatalog() []wireServiceEntry {
	endpoints := remote.owner.endpoints
	services := make([]wireServiceEntry, 0, len(endpoints))
	for _, endpoint := range endpoints {
		if !endpoint.public || endpoint.dispatcher == nil {
			continue
		}
		services = append(services, wireServiceEntry{
			name:        endpoint.serviceName,
			fingerprint: endpoint.dispatcher.Fingerprint(),
		})
	}
	sort.Slice(services, func(left, right int) bool {
		return services[left].name < services[right].name
	})
	return services
}

// targetSession 返回目标当前已经握手完成的出站会话。
func (remote *remoteRuntime) targetSession(nodeID string) *outboundSession {
	remote.mu.Lock()
	target := remote.targets[nodeID]
	remote.mu.Unlock()
	if target == nil {
		return nil
	}
	return target.currentSession()
}

// connectionOptions 把 RPC 冻结配置转换为 M5 TCP 适配参数。
func (remote *remoteRuntime) connectionOptions() tcpnet.ConnectionOptions {
	options := tcpnet.DefaultConnectionOptions(remote.owner.pool)
	options.Logger = remote.owner.logger
	options.MaxMessageSize = remote.config.frameLimit()
	options.SendQueueFrames = remote.config.TCP.SendQueueFrames
	options.SendQueueBytes = remote.config.sendQueueBytes()
	options.ReadTimeout = remote.config.TCP.ReadTimeout
	options.WriteTimeout = remote.config.TCP.WriteTimeout
	return options
}

// listenOptions 为入站连接补充明确的连接总上限。
func (remote *remoteRuntime) listenOptions() tcpnet.ListenOptions {
	return tcpnet.ListenOptions{
		Connection:     remote.connectionOptions(),
		MaxConnections: maxRemoteTargets,
	}
}

// heartbeatInterval 返回应用层 Ping 周期；零表示关闭。
func (remote *remoteRuntime) heartbeatInterval() time.Duration {
	if remote.config.TCP.ReadTimeout <= 0 {
		return 0
	}
	interval := remote.config.TCP.ReadTimeout / heartbeatDivisor
	if interval <= 0 {
		return time.Millisecond
	}
	return interval
}

// watchDeadlines 批量消费入站 Request 的 M8 到期 ID。
func (remote *remoteRuntime) watchDeadlines(queue *timerwheel.DeadlineQueue) {
	defer close(remote.deadlineDone)
	expired := make([]timerwheel.DeadlineID, 0, 64)
	for range queue.ExpiredSignal() {
		expired = expired[:0]
		for {
			var err error
			expired, err = queue.DrainExpired(expired[:0], 256)
			if err != nil {
				return
			}
			if len(expired) == 0 {
				break
			}

			// 每个 ID 在同一锁下唯一取得对应取消函数；业务取消动作在锁外执行。
			cancels := make([]context.CancelCauseFunc, 0, len(expired))
			remote.mu.Lock()
			for _, id := range expired {
				if cancel := remote.deadlineBindings[id]; cancel != nil {
					delete(remote.deadlineBindings, id)
					cancels = append(cancels, cancel)
				}
			}
			remote.mu.Unlock()
			for _, cancel := range cancels {
				cancel(errs.ErrDeadlineExceeded)
			}
			if len(expired) < 256 {
				break
			}
		}
	}
}

// bindDeadline 登记一次入站 Request 的唯一 M8 超时。
func (remote *remoteRuntime) bindDeadline(
	delay time.Duration,
	cancel context.CancelCauseFunc,
) (timerwheel.DeadlineID, error) {
	if delay <= 0 || cancel == nil {
		return timerwheel.InvalidDeadlineID, errs.ErrInvalidArgument
	}
	remote.mu.Lock()
	queue := remote.deadlineQueue
	if queue == nil || remote.stopping {
		remote.mu.Unlock()
		return timerwheel.InvalidDeadlineID, errs.ErrServiceStopped
	}

	// ScheduleAfter 与绑定发布都位于 remote.mu 内。watcher 即使已经收到到期信号，也必须
	// 在 Drain 后取得同一把锁，因此不可能先消费 ID、再看到尚未登记的空绑定。
	id, err := queue.ScheduleAfter(delay)
	if err != nil {
		remote.mu.Unlock()
		return timerwheel.InvalidDeadlineID, err
	}
	remote.deadlineBindings[id] = cancel
	remote.mu.Unlock()
	return id, nil
}

// unbindDeadline 取消仍未到期的 ID，并删除可能已被 watcher 取得的绑定。
func (remote *remoteRuntime) unbindDeadline(id timerwheel.DeadlineID) {
	if id == timerwheel.InvalidDeadlineID {
		return
	}
	remote.mu.Lock()
	queue := remote.deadlineQueue
	delete(remote.deadlineBindings, id)
	remote.mu.Unlock()
	if queue != nil {
		queue.Cancel(id)
	}
}

// logReconnectFailure 记录目标冷路径失败，不在每次业务调用热路径写日志。
func (remote *remoteRuntime) logReconnectFailure(
	targetNodeID string,
	address string,
	err error,
) {
	remote.owner.logger.Warn(
		"RPC TCP 连接尚未就绪",
		originlog.String("target_node_id", targetNodeID),
		originlog.String("address", address),
		originlog.Err(err),
	)
}
