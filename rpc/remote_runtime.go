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
// 成本。targets 只由所属 Node 的不可变发现目录完整对账，不扫描进程中的其他 Runtime，
// 也不是第二份服务发现事实。
type remoteRuntime struct {
	owner  *Runtime
	config Config

	mu       sync.Mutex
	started  bool
	stopping bool
	listener *tcpnet.Listener
	inbound  *inboundHandler
	targets  map[string]*remoteTarget
	// retired 只保存已经取消但 goroutine 尚未退出的旧发现目标，退出回调会立即删除。
	retired map[*remoteTarget]struct{}

	deadlines *inboundDeadlines
}

// newRemoteRuntime 创建尚未启动、没有后台 goroutine 的远端资源容器。
func newRemoteRuntime(owner *Runtime, config Config) *remoteRuntime {
	return &remoteRuntime{
		owner:   owner,
		config:  config,
		targets: make(map[string]*remoteTarget),
		retired: make(map[*remoteTarget]struct{}),
	}
}

// StartNetwork 在 Node 时间轮已经运行后启动 Listener、Deadline watcher 和已知目标。
func (runtime *Runtime) StartNetwork(engine *timerwheel.Engine) error {
	if runtime == nil || engine == nil {
		return errs.ErrInvalidArgument
	}
	if runtime.remote == nil {
		if runtime.nats == nil {
			return nil
		}
		return runtime.nats.start(engine)
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

// TransportInfo 返回当前 Node 对服务发现公开的传输和可选地址。
func (runtime *Runtime) TransportInfo() (transport string, address string, enabled bool) {
	if runtime == nil {
		return "", "", false
	}
	if runtime.remote != nil {
		return TransportTCP, runtime.remote.config.TCP.Advertise, true
	}
	if runtime.nats != nil {
		return TransportNATS, "", true
	}
	return "", "", false
}

// start 一次性发布远端 RPC 资源。
func (remote *remoteRuntime) start(engine *timerwheel.Engine) error {
	remote.mu.Lock()
	if remote.started || remote.stopping {
		remote.mu.Unlock()
		return errs.ErrInvalidArgument
	}

	// DeadlineQueue 必须先于 Listener 建立；入站 Request 一旦可读就能够登记唯一超时。
	deadlines, err := newInboundDeadlines(engine)
	if err != nil {
		remote.mu.Unlock()
		return err
	}
	remote.deadlines = deadlines
	remote.inbound = newInboundHandler(remote)
	options := remote.listenOptions()
	listener, err := tcpnet.Listen(
		remote.config.TCP.Listen,
		options,
		remote.inbound,
	)
	if err != nil {
		remote.deadlines = nil
		remote.inbound = nil
		remote.mu.Unlock()
		deadlines.close(errs.ErrServiceStopped)
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

	for _, target := range targets {
		target.start()
	}
	// 监听器永久退出属于 Node 级基础设施终态；正常 StopAccept 的 Cause 为 nil，不会误报。
	go remote.watchListener(listener)
	return nil
}

// watchListener 把 TCP AcceptLoop 的永久终态提升到唯一 Application 生命周期路径。
func (remote *remoteRuntime) watchListener(listener *tcpnet.Listener) {
	if remote == nil || listener == nil {
		return
	}
	<-listener.AcceptDone()
	cause := listener.Cause()
	if cause == nil {
		return
	}
	remote.mu.Lock()
	unexpected := remote.listener == listener && !remote.stopping
	remote.mu.Unlock()
	if unexpected {
		remote.owner.reportTransportFailure(cause)
	}
}

// AddTarget 保留 M13 的底层单目标兼容入口；M14 的 Node/Application 不再调用它，而统一
// 使用 ReconcileTargets 让服务发现目录提交完整目标集合。
//
// 相同 NodeID 和地址重复登记是幂等操作；同一 NodeID 的不同地址不会替换现有连接，
// 避免误启动实例抢占正在工作的目标。
func (runtime *Runtime) AddTarget(
	nodeID string,
	sessionID uint64,
	address string,
) error {
	if runtime == nil || runtime.remote == nil ||
		!validWireName(nodeID) || sessionID == 0 || nodeID == runtime.nodeID {
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
	target := newRemoteTarget(remote, nodeID, sessionID, address)
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
	remote.retired[target] = struct{}{}
	remote.mu.Unlock()
	return target.stop(ctx)
}

// ConnectionTarget 是发现目录交给 TCP Runtime 的 Node 级连接需求。
type ConnectionTarget struct {
	NodeID    string
	SessionID uint64
	Address   string
}

// ReconcileTargets 非阻塞地把 TCP 目标表对齐为发现目录的最新完整集合。
//
// 删除或替换只发出取消信号，不等待旧目标 goroutine；Runtime Stop 仍会等待当时尚未退出的
// retired 目标，保证资源所有权完整。
func (runtime *Runtime) ReconcileTargets(targets []ConnectionTarget) error {
	if runtime == nil {
		return errs.ErrInvalidArgument
	}
	if runtime.remote == nil {
		// 当前 Node 没有 TCP Transport 时仍可保存发现目录，但不会建立业务连接。
		return nil
	}

	// 先完整校验并建立临时 Map，任何坏输入都不能部分修改当前连接需求。
	desired := make(map[string]ConnectionTarget, len(targets))
	for _, target := range targets {
		if !validWireName(target.NodeID) ||
			target.SessionID == 0 ||
			target.NodeID == runtime.nodeID {
			return errs.ErrInvalidArgument
		}
		if err := validateAdvertiseAddress(target.Address); err != nil {
			return err
		}
		if _, duplicate := desired[target.NodeID]; duplicate {
			return errs.ErrInvalidArgument
		}
		desired[target.NodeID] = target
	}
	if len(desired) > maxRemoteTargets {
		return errs.ErrTransportOverloaded
	}

	remote := runtime.remote
	remote.mu.Lock()
	if remote.stopping || runtime.closed.Load() {
		remote.mu.Unlock()
		return errs.ErrServiceStopped
	}
	var stopping []*remoteTarget
	var starting []*remoteTarget
	for nodeID, current := range remote.targets {
		target, exists := desired[nodeID]
		if exists &&
			current.sessionID == target.SessionID &&
			current.address == target.Address {
			delete(desired, nodeID)
			continue
		}
		delete(remote.targets, nodeID)
		remote.retired[current] = struct{}{}
		stopping = append(stopping, current)
	}
	for nodeID, target := range desired {
		created := newRemoteTarget(
			remote,
			nodeID,
			target.SessionID,
			target.Address,
		)
		remote.targets[nodeID] = created
		starting = append(starting, created)
	}
	started := remote.started
	remote.mu.Unlock()

	// 取消和启动均不等待网络 I/O；新快照已经先成为 RPC 路由事实。
	for _, target := range stopping {
		target.requestStop()
	}
	if started {
		for _, target := range starting {
			target.start()
		}
	}
	return nil
}

// BeginStop 立即拒绝新入站 RPC，但保留既有连接、出站调用和已接受任务的 Deadline。
//
// 目标 Service 随后的 Scheduler Drain 会执行完已经进入 FIFO 的任务，OnStop 也仍可调用
// 其他 Service；最终 Close 才关闭既有连接、出站目标和 Deadline。
func (runtime *Runtime) BeginStop(ctx context.Context) error {
	if runtime == nil || ctx == nil {
		return errs.ErrInvalidArgument
	}
	runtime.inboundReady.Store(false)
	if runtime.remote == nil {
		if runtime.nats == nil {
			return nil
		}
		return runtime.nats.beginStop(ctx)
	}
	return runtime.remote.beginStop(ctx)
}

// beginStop 只停止新的 TCP 连接与入站业务准入。
func (remote *remoteRuntime) beginStop(ctx context.Context) error {
	remote.mu.Lock()
	if remote.stopping {
		listener := remote.listener
		remote.mu.Unlock()
		if listener == nil {
			return nil
		}
		return listener.StopAccept(ctx)
	}
	remote.stopping = true
	listener := remote.listener
	remote.mu.Unlock()
	if listener == nil {
		return nil
	}
	return listener.StopAccept(ctx)
}

// closeTransport 在 Service 排空后关闭出站目标和全部已接受 TCP 连接。
func (remote *remoteRuntime) closeTransport(ctx context.Context) error {
	if remote == nil || ctx == nil {
		return errs.ErrInvalidArgument
	}
	remote.mu.Lock()
	remote.stopping = true
	listener := remote.listener
	targets := make([]*remoteTarget, 0, len(remote.targets))
	for _, target := range remote.targets {
		targets = append(targets, target)
	}
	for target := range remote.retired {
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

// closeDeadlines 在所有 Service 已排空后关闭入站 Deadline 和 watcher。
func (remote *remoteRuntime) closeDeadlines() {
	remote.mu.Lock()
	deadlines := remote.deadlines
	remote.deadlines = nil
	remote.mu.Unlock()
	if deadlines != nil {
		deadlines.close(errs.ErrServiceStopped)
	}
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
func (remote *remoteRuntime) targetSession(
	nodeID string,
	sessionID uint64,
) *outboundSession {
	remote.mu.Lock()
	target := remote.targets[nodeID]
	remote.mu.Unlock()
	if target == nil || target.sessionID != sessionID {
		return nil
	}
	return target.currentSession()
}

// targetDone 删除已经退出的 retired 目标；当前活动目标不会自行退出并删除连接需求。
func (remote *remoteRuntime) targetDone(target *remoteTarget) {
	remote.mu.Lock()
	delete(remote.retired, target)
	remote.mu.Unlock()
}

// connectionOptions 把 RPC 冻结配置转换为 M5 TCP 适配参数。
func (remote *remoteRuntime) connectionOptions() tcpnet.ConnectionOptions {
	options := tcpnet.DefaultConnectionOptions(remote.owner.pool)
	options.Logger = remote.owner.logger
	options.MaxMessageSize = remote.config.frameLimit()
	options.SendQueueFrames = remote.config.TCP.SendQueueMessages
	options.ReadTimeout = remote.config.TCP.ReadIdleTimeout
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
	if remote.config.TCP.ReadIdleTimeout <= 0 {
		return 0
	}
	interval := remote.config.TCP.ReadIdleTimeout / heartbeatDivisor
	if interval <= 0 {
		return time.Millisecond
	}
	return interval
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
