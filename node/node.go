// Package node 提供按配置顺序拥有并驱动多个 Service 的一次性运行节点。
package node

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"runtime/debug"
	"sync"
	"sync/atomic"
	"time"

	"github.com/duanhf2012/origin/v3/errs"
	"github.com/duanhf2012/origin/v3/internal/bufferpool"
	internaldiscovery "github.com/duanhf2012/origin/v3/internal/discovery"
	"github.com/duanhf2012/origin/v3/internal/timerwheel"
	originlog "github.com/duanhf2012/origin/v3/log"
	"github.com/duanhf2012/origin/v3/rpc"
	"github.com/duanhf2012/origin/v3/service"
)

// interfaceService 保持 ServiceBinding 字段声明简短，同时仍使用公开生命周期契约。
type interfaceService = service.IService

// Node 按配置顺序拥有一组相互独立的 Service 实例。
//
// Node 是一次性对象。Start 成功或失败后都不能再次启动；Stop 和 Rollback 只由
// Application 生命周期控制路径调用。
type Node struct {
	// id、private 和 logger 在构造完成后只读。
	id        string
	sessionID uint64
	private   bool
	labels    map[string]string
	logger    originlog.Logger
	// schedulerConfig 是当前 Node 为每个 ServiceScheduler 提供的冻结默认策略。
	schedulerConfig service.SchedulerConfig

	// state 为查询提供无锁快照，生命周期写入由单一控制路径串行执行。
	state atomic.Uint32
	// services 同时提供稳定顺序和按实际名称的 O(1) 本地查询。
	services []*serviceEntry
	byName   map[string]*serviceEntry
	// started 只包含已经进入过 OnStart 的 Service，决定唯一停止顺序。
	started []*serviceEntry
	// timerEngine 是当前 Node 独占的 Deadline 时间轮；它先于 OnStart 启动、晚于 OnStop 关闭。
	timerEngine *timerwheel.Engine
	// timerResources 只保存标量、原子计数和时区，不按最大额度预分配业务 Timer。
	timerResources nodeTimerResources
	// rpcRuntime 是当前 Node 独占的本地路由目录；BufferPool 仍由 Application 共享。
	rpcRuntime *rpc.Runtime
	// discovery 是当前 Node 独占的可见目录；source/subscription 只属于 M14 过渡数据源。
	discovery             *discoveryRuntime
	discoverySource       *internaldiscovery.Source
	discoverySubscription *internaldiscovery.Subscription
	discoveryPublished    atomic.Bool
	// runtimeFailure 只通知 Application 的唯一控制路径；failureOnce 防止重复撤销和停机。
	runtimeFailure func(nodeID string, cause error)
	failureOnce    sync.Once
}

// nodeTimerResources 管理当前 Node 生命周期内唯一 TimerID 和共享活跃额度。
type nodeTimerResources struct {
	maxTimers     int64
	activeTimers  atomic.Int64
	nextTimerID   atomic.Uint64
	timerLocation *time.Location
}

// serviceEntry 保存单个 Service 的运行身份和由 Node 拥有的状态。
type serviceEntry struct {
	nodeID              string
	name                string
	template            string
	private             bool
	instance            service.IService
	logger              originlog.Logger
	state               atomic.Uint32
	startError          bool
	contractID          uint64
	contractFingerprint [32]byte
	// discoveryRun 是当前 Service 唯一且稳定的发现状态同步函数。
	discoveryRun func(context.Context)
}

// serviceRuntime 把 Service 的只读查询限制在所属 Node 和当前实例。
type serviceRuntime struct {
	node  *Node
	entry *serviceEntry
}

// dispatcherProvider 是 origingen 为实现公开 RPC 契约的 Service 生成的装配适配接口。
//
// 接口留在 node 包内部，业务不需要手工注册 Dispatcher。
type dispatcherProvider interface {
	RPCDispatcher() rpc.Dispatcher
}

// New 校验绑定数据并创建尚未启动的 Node。
func New(
	config Config,
	bindings []ServiceBinding,
	logger originlog.Logger,
	options Options,
) (*Node, error) {
	// Node 身份和 Service 列表必须在分配运行对象前完整有效。
	if config.ID == "" {
		return nil, invalidConfig("Node ID 不能为空")
	}
	if len(bindings) == 0 {
		return nil, invalidConfig(fmt.Sprintf("Node %q 没有 Service", config.ID))
	}
	if options.MaxTimersPerNode <= 0 {
		return nil, invalidConfig(fmt.Sprintf(
			"Node %q 的 MaxTimersPerNode 必须大于 0",
			config.ID,
		))
	}
	if options.TimerLocation == nil {
		return nil, invalidConfig(fmt.Sprintf(
			"Node %q 的 TimerLocation 不能为空",
			config.ID,
		))
	}
	if options.BufferPool == nil {
		// node.New 仍可用于独立单元测试；正式 Application 会传入进程级共享 Pool。
		options.BufferPool = bufferpool.NewPool(bufferpool.Options{})
	}
	if err := internaldiscovery.ValidateNodeLabels(config.Labels); err != nil {
		return nil, invalidConfig(fmt.Sprintf(
			"Node %q 的 labels 无效: %v",
			config.ID,
			err,
		))
	}
	sessionID, err := newSessionID()
	if err != nil {
		return nil, fmt.Errorf("创建 Node %q SessionID: %w", config.ID, err)
	}
	discoveryRuntime, err := newDiscoveryRuntime(config.ID, config.DiscoveryFilter)
	if err != nil {
		return nil, fmt.Errorf("创建 Node %q 服务发现目录: %w", config.ID, err)
	}

	rpcRuntime, err := rpc.NewRuntime(
		config.ID,
		options.BufferPool,
		logger.With(originlog.String("node_id", config.ID)),
	)
	if err != nil {
		return nil, fmt.Errorf("创建 Node %q RPC Runtime: %w", config.ID, err)
	}
	if err := rpcRuntime.Configure(config.RPC); err != nil {
		return nil, fmt.Errorf("配置 Node %q RPC Runtime: %w", config.ID, err)
	}

	// 按已知数量一次分配有序表和查询表，避免装配时重复扩容。
	instance := &Node{
		id:              config.ID,
		sessionID:       sessionID,
		private:         config.Private,
		labels:          cloneLabels(config.Labels),
		logger:          logger.With(originlog.String("node_id", config.ID)),
		schedulerConfig: config.Scheduler,
		services:        make([]*serviceEntry, 0, len(bindings)),
		byName:          make(map[string]*serviceEntry, len(bindings)),
		started:         make([]*serviceEntry, 0, len(bindings)),
		timerResources: nodeTimerResources{
			maxTimers:     int64(options.MaxTimersPerNode),
			timerLocation: options.TimerLocation,
		},
		rpcRuntime:      rpcRuntime,
		discovery:       discoveryRuntime,
		discoverySource: options.DiscoverySource,
		runtimeFailure:  options.RuntimeFailure,
	}
	discoveryRuntime.bindNode(instance)
	if err := rpcRuntime.BindSessionID(sessionID); err != nil {
		return nil, fmt.Errorf("绑定 Node %q RPC SessionID: %w", config.ID, err)
	}
	if err := rpcRuntime.BindRemoteResolver(discoveryRuntime); err != nil {
		return nil, fmt.Errorf("绑定 Node %q RPC 服务发现目录: %w", config.ID, err)
	}
	if err := rpcRuntime.BindFailureHandler(instance.handleRuntimeFailure); err != nil {
		return nil, fmt.Errorf("绑定 Node %q RPC 终态处理器: %w", config.ID, err)
	}
	instance.state.Store(uint32(StateCreated))

	// 所有 Service 必须先完成创建、登记和 Runtime 绑定，之后才允许调用第一个 OnInit。
	for _, binding := range bindings {
		if binding.Name == "" || binding.Template == "" || binding.Service == nil {
			return nil, invalidConfig(fmt.Sprintf("Node %q 包含无效 Service 绑定", config.ID))
		}
		if _, exists := instance.byName[binding.Name]; exists {
			return nil, invalidConfig(fmt.Sprintf(
				"Node %q 的 ServiceName %q 重复",
				config.ID,
				binding.Name,
			))
		}

		// Entry 先建立完整稳定身份，再把只读 Runtime 交给业务基础对象。
		entry := &serviceEntry{
			nodeID:   config.ID,
			name:     binding.Name,
			template: binding.Template,
			private:  binding.Private,
			instance: binding.Service,
			logger: instance.logger.With(
				originlog.String("service_name", binding.Name),
			),
		}
		entry.discoveryRun = func(ctx context.Context) {
			instance.discovery.deliver(ctx, entry)
		}
		entry.state.Store(uint32(service.StateCreated))
		runtime := &serviceRuntime{node: instance, entry: entry}
		if err := service.BindRuntime(binding.Service, runtime); err != nil {
			return nil, fmt.Errorf(
				"绑定 Node %q Service %q: %w",
				config.ID,
				binding.Name,
				err,
			)
		}
		var dispatcher rpc.Dispatcher
		if provider, ok := binding.Service.(dispatcherProvider); ok {
			dispatcher = provider.RPCDispatcher()
			if dispatcher == nil {
				return nil, invalidConfig(fmt.Sprintf(
					"Node %q Service %q 返回空 RPC Dispatcher",
					config.ID,
					binding.Name,
				))
			}
			entry.contractID = uint64(dispatcher.ContractID())
			entry.contractFingerprint = [32]byte(dispatcher.Fingerprint())
		}
		if err := instance.rpcRuntime.RegisterServiceVisibility(
			binding.Name,
			binding.Service,
			dispatcher,
			!config.Private && !binding.Private,
		); err != nil {
			return nil, fmt.Errorf(
				"登记 Node %q Service %q RPC: %w",
				config.ID,
				binding.Name,
				err,
			)
		}
		instance.services = append(instance.services, entry)
		instance.byName[binding.Name] = entry
	}
	if err := instance.rpcRuntime.Freeze(); err != nil {
		return nil, fmt.Errorf("冻结 Node %q RPC Runtime: %w", config.ID, err)
	}

	// 所有 Service 绑定成功后再创建 Node 独占时间轮，避免前序校验失败时遗留底层 Timer。
	timerEngine, err := timerwheel.New(timerwheel.DefaultOptions())
	if err != nil {
		return nil, fmt.Errorf("创建 Node %q TimerEngine: %w", config.ID, err)
	}
	instance.timerEngine = timerEngine
	if instance.discoverySource != nil {
		subscription, err := instance.discoverySource.Subscribe(instance.discovery.apply)
		if err != nil {
			_ = timerEngine.Close()
			rpcRuntime.Close()
			return nil, fmt.Errorf("订阅 Node %q 过渡服务发现: %w", config.ID, err)
		}
		instance.discoverySubscription = subscription
	}
	return instance, nil
}

// handleRuntimeFailure 先撤销当前会话的公开发现，再唤醒 Application 的串行停机路径。
func (node *Node) handleRuntimeFailure(cause error) {
	if node == nil || cause == nil {
		return
	}
	node.failureOnce.Do(func() {
		node.withdrawDiscovery()
		if node.runtimeFailure != nil {
			node.runtimeFailure(node.id, cause)
		}
	})
}

// newSessionID 使用系统安全随机源创建不由业务配置控制的 Node 进程会话标识。
func newSessionID() (uint64, error) {
	var raw [8]byte
	for {
		// crypto/rand 直接生成不可预测的进程会话；不混入 NodeID、时间或机器信息。
		if _, err := rand.Read(raw[:]); err != nil {
			return 0, err
		}
		sessionID := binary.BigEndian.Uint64(raw[:])
		if sessionID != 0 {
			return sessionID, nil
		}
		// 全零概率为 2^-64；显式重试让零值始终保留为“未绑定/非法”语义。
	}
}

// cloneLabels 冻结 Node 发布标签，防止配置根 Map 在启动后被外部修改。
func cloneLabels(source map[string]string) map[string]string {
	if len(source) == 0 {
		return nil
	}
	result := make(map[string]string, len(source))
	for key, value := range source {
		result[key] = value
	}
	return result
}

// acquireTimerSlot 原子申请一个活跃额度并分配永不复用的 Node TimerID。
func (node *Node) acquireTimerSlot() (service.TimerID, bool) {
	if node == nil {
		return service.InvalidTimerID, false
	}

	// 先申请共享活跃额度；CAS 失败时重新读取，确保多个 Service 并发创建不会突破上限。
	for {
		active := node.timerResources.activeTimers.Load()
		if active >= node.timerResources.maxTimers {
			return service.InvalidTimerID, false
		}
		if node.timerResources.activeTimers.CompareAndSwap(active, active+1) {
			break
		}
	}

	// ID 使用不回绕的 CAS。达到 MaxUint64 后永久拒绝新 ID，并归还刚取得的额度。
	for {
		previous := node.timerResources.nextTimerID.Load()
		if previous == math.MaxUint64 {
			node.releaseTimerSlot()
			return service.InvalidTimerID, false
		}
		if node.timerResources.nextTimerID.CompareAndSwap(previous, previous+1) {
			return service.TimerID(previous + 1), true
		}
	}
}

// releaseTimerSlot 归还一个已经脱离全部业务容器的 Timer 活跃额度。
func (node *Node) releaseTimerSlot() {
	if node == nil {
		panic("node: nil Node 不能归还 Timer Slot")
	}
	for {
		active := node.timerResources.activeTimers.Load()
		if active <= 0 {
			panic("node: Timer Slot 重复归还")
		}
		if node.timerResources.activeTimers.CompareAndSwap(active, active-1) {
			return
		}
	}
}

// ID 返回 Node 的稳定身份。
func (node *Node) ID() string {
	if node == nil {
		return ""
	}
	return node.id
}

// Private 报告 Node 是否被配置为不公开。
func (node *Node) Private() bool {
	return node != nil && node.private
}

// State 返回 Node 的无锁生命周期状态快照。
func (node *Node) State() State {
	if node == nil {
		return StateFailed
	}
	return State(node.state.Load())
}

// Logger 返回已经预绑定 NodeID 的 Logger。
func (node *Node) Logger() originlog.Logger {
	if node == nil {
		return originlog.NewNop()
	}
	return node.logger
}

// Service 按实际 ServiceName 查询当前 Node 的本地实例。
func (node *Node) Service(name string) (service.IService, bool) {
	if node == nil || name == "" {
		return nil, false
	}
	entry, exists := node.byName[name]
	if !exists {
		return nil, false
	}
	return entry.instance, true
}

// Services 返回按配置顺序排列的独立 Slice 快照。
func (node *Node) Services() []service.IService {
	if node == nil || len(node.services) == 0 {
		return nil
	}
	result := make([]service.IService, len(node.services))
	for index, entry := range node.services {
		result[index] = entry.instance
	}
	return result
}

// Start 按配置顺序完成全部 Service 的 OnInit 和 OnStart。
func (node *Node) Start(ctx context.Context) error {
	// 生命周期入口拒绝 nil Context 和非 Created 状态，避免隐式重启一次性对象。
	if node == nil {
		return invalidArgument("Node 不能为空")
	}
	if ctx == nil {
		return invalidArgument(fmt.Sprintf("Node %q 的启动 Context 不能为空", node.id))
	}
	if node.State() != StateCreated {
		return invalidArgument(fmt.Sprintf(
			"Node %q 不能从状态 %d 启动",
			node.id,
			node.State(),
		))
	}
	node.state.Store(uint32(StateStarting))
	node.logger.Info("node starting")

	// 第一阶段只执行纯初始化；任一失败时当前 Node 不调用任何 OnStart 或 OnStop。
	for _, entry := range node.services {
		entry.state.Store(uint32(service.StateInitializing))
		err := callLifecycle(entry, "on_init", func() error {
			return entry.instance.OnInit()
		})
		if err != nil {
			entry.state.Store(uint32(service.StateFailed))
			node.state.Store(uint32(StateFailed))
			return err
		}
		entry.state.Store(uint32(service.StateInitialized))
	}

	// 全部 OnInit 成功后启动 Node 唯一时间轮，使每个 OnStart 都能依赖统一 Deadline 能力。
	if err := node.timerEngine.Start(); err != nil {
		node.state.Store(uint32(StateFailed))
		return &lifecycleContext{
			nodeID: node.id,
			phase:  "timer_engine_start",
			cause:  err,
		}
	}
	// TCP Listener 和出站连接依赖已经运行的 M8 DeadlineQueue，并且必须先于 OnStart
	// 建立，使启动逻辑可以调用已经可达的远端 Service。
	if err := node.rpcRuntime.StartNetwork(node.timerEngine); err != nil {
		node.state.Store(uint32(StateFailed))
		return &lifecycleContext{
			nodeID: node.id,
			phase:  "rpc_network_start",
			cause:  err,
		}
	}

	// 时间轮运行后再进入启动阶段。每个 Scheduler 先 Prepare，使 OnStart 可以登记 Timer，
	// 但不启动任何用户任务；Prepare 失败的 Service 尚未进入 OnStart，因此不加入停止序列。
	for _, entry := range node.services {
		// 在进入每个业务回调前观察取消，避免超时后继续启动后续 Service。
		if err := contextFailure(ctx); err != nil {
			node.state.Store(uint32(StateFailed))
			return &lifecycleContext{
				nodeID:      node.id,
				serviceName: entry.name,
				phase:       "on_start",
				cause:       err,
			}
		}
		entry.state.Store(uint32(service.StateStarting))
		if err := service.PrepareScheduler(
			entry.instance,
			node.schedulerConfig,
			node.timerEngine,
		); err != nil {
			entry.state.Store(uint32(service.StateFailed))
			node.state.Store(uint32(StateFailed))
			return &lifecycleContext{
				nodeID:      node.id,
				serviceName: entry.name,
				phase:       "scheduler_prepare",
				cause:       err,
			}
		}
		// OnInit 中登记的监听器在 Scheduler Prepared 后形成脏标记，但仍等待统一激活屏障。
		node.discovery.activateOwner(entry)

		// 从真正进入 OnStart 前开始记录停止责任；OnStart 或 Activate 失败都必须先清理
		// Prepared Scheduler，再执行当前 Service 的 OnStop。
		node.started = append(node.started, entry)
		startContext, finishStart, err := service.PrepareStartContext(
			entry.instance,
			ctx,
		)
		if err != nil {
			entry.startError = true
			entry.state.Store(uint32(service.StateFailed))
			node.state.Store(uint32(StateFailed))
			return &lifecycleContext{
				nodeID:      node.id,
				serviceName: entry.name,
				phase:       "start_context_prepare",
				cause:       err,
			}
		}
		err = callLifecycle(entry, "on_start", func() error {
			return entry.instance.OnStart(startContext)
		})
		finishStart()
		if err != nil {
			entry.startError = true
			entry.state.Store(uint32(service.StateFailed))
			node.state.Store(uint32(StateFailed))
			return err
		}
	}
	// 最后一个回调可能在执行期间越过 Deadline 却返回 nil，发布 Ready 前必须再次确认。
	if err := contextFailure(ctx); err != nil {
		node.state.Store(uint32(StateFailed))
		return &lifecycleContext{
			nodeID:      node.id,
			serviceName: node.services[len(node.services)-1].name,
			phase:       "on_start",
			cause:       err,
		}
	}

	// 全部 OnStart 成功后统一发布 Running 并激活 Runner，任何业务任务都不能与后续
	// Service 的 OnStart 并发。
	for _, entry := range node.started {
		entry.state.Store(uint32(service.StateRunning))
		if err := service.ActivateScheduler(entry.instance); err != nil {
			entry.startError = true
			entry.state.Store(uint32(service.StateFailed))
			node.state.Store(uint32(StateFailed))
			return &lifecycleContext{
				nodeID:      node.id,
				serviceName: entry.name,
				phase:       "scheduler_activate",
				cause:       err,
			}
		}
	}

	// 全部 Runner 已激活后开放入站业务准入；随后一次性发布全部公开 Service。
	if err := node.rpcRuntime.OpenInbound(); err != nil {
		node.state.Store(uint32(StateFailed))
		return &lifecycleContext{
			nodeID: node.id,
			phase:  "rpc_inbound_open",
			cause:  err,
		}
	}
	if err := node.publishDiscovery(); err != nil {
		node.state.Store(uint32(StateFailed))
		return &lifecycleContext{
			nodeID: node.id,
			phase:  "discovery_publish",
			cause:  err,
		}
	}
	node.state.Store(uint32(StateReady))
	node.logger.Info("node ready")
	return nil
}

// Rollback 清理启动失败 Node 中所有进入过 OnStart 的 Service。
//
// Rollback 完成后 Node 保持 Failed，明确表示它不能作为正常停止对象重新使用。
func (node *Node) Rollback(ctx context.Context) error {
	if node == nil {
		return nil
	}
	if ctx == nil {
		return invalidArgument(fmt.Sprintf("Node %q 的回滚 Context 不能为空", node.id))
	}
	// 失败实例仍先获得反序 OnStop，最后再关闭 Node 时间轮并等待其 goroutine 退出。
	node.withdrawDiscovery()
	result := node.rpcRuntime.BeginStop(ctx)
	result = errors.Join(result, node.stopStarted(ctx, true))
	node.rpcRuntime.Close()
	result = errors.Join(result, node.timerEngine.Close())
	node.closeDiscoverySubscription()
	node.state.Store(uint32(StateFailed))
	return result
}

// Stop 严格反序停止全部进入过 OnStart 的 Service。
func (node *Node) Stop(ctx context.Context) error {
	if node == nil {
		return nil
	}
	if ctx == nil {
		return invalidArgument(fmt.Sprintf("Node %q 的停止 Context 不能为空", node.id))
	}
	state := node.State()
	if state == StateStopped {
		return nil
	}
	if state != StateReady && state != StateFailed {
		return invalidArgument(fmt.Sprintf(
			"Node %q 不能从状态 %d 停止",
			node.id,
			state,
		))
	}

	// 正常停止会发布 Stopping/Stopped；失败回滚由 Rollback 保持 Failed。
	node.state.Store(uint32(StateStopping))
	node.logger.Info("node stopping")
	node.withdrawDiscovery()
	result := node.rpcRuntime.BeginStop(ctx)
	// Service 清理阶段保留时间轮运行，全部 OnStop 返回后才回收 Node 最后的后台资源。
	result = errors.Join(result, node.stopStarted(ctx, false))
	node.rpcRuntime.Close()
	result = errors.Join(result, node.timerEngine.Close())
	node.closeDiscoverySubscription()
	node.state.Store(uint32(StateStopped))
	node.logger.Info("node stopped")
	return result
}

// publishDiscovery 在全部 OnStart 和 Runner 激活成功后整体发布当前 Node 的公开 Service。
func (node *Node) publishDiscovery() error {
	if node.discoverySource == nil || node.private {
		return nil
	}
	services := make([]internaldiscovery.RawService, 0, len(node.services))
	for _, entry := range node.services {
		if entry.private {
			continue
		}
		services = append(services, internaldiscovery.RawService{
			ServiceName:         entry.name,
			State:               internaldiscovery.ServiceStateRunning,
			ContractID:          entry.contractID,
			ContractFingerprint: entry.contractFingerprint,
		})
	}
	// 全部 Service 均为私有时没有远端可见事实，不发布空 Node 记录。
	if len(services) == 0 {
		return nil
	}
	transport := internaldiscovery.TransportNone
	address := ""
	if transportName, advertised, exists := node.rpcRuntime.TransportInfo(); exists {
		switch transportName {
		case rpc.TransportTCP:
			transport = internaldiscovery.TransportTCP
			address = advertised
		case rpc.TransportNATS:
			transport = internaldiscovery.TransportNATS
		}
	}
	if err := node.discoverySource.Publish(internaldiscovery.RawNode{
		NodeID:    node.id,
		SessionID: node.sessionID,
		Labels:    cloneLabels(node.labels),
		Transport: transport,
		Address:   address,
		Services:  services,
	}); err != nil {
		return err
	}
	node.discoveryPublished.Store(true)
	return nil
}

// withdrawDiscovery 在正式停止新业务准入前撤销当前精确 Node 会话。
func (node *Node) withdrawDiscovery() {
	if node == nil || node.discoverySource == nil ||
		!node.discoveryPublished.Swap(false) {
		return
	}
	node.discoverySource.Withdraw(node.id, node.sessionID)
}

// closeDiscoverySubscription 幂等关闭当前 Node 对过渡完整快照源的订阅。
func (node *Node) closeDiscoverySubscription() {
	if node == nil {
		return
	}
	if node.discoverySubscription != nil {
		node.discoverySubscription.Close()
		node.discoverySubscription = nil
	}
	// 即使独立 node.New 没有过渡 Source，也必须使查询、等待和监听外观随 Node 一起失效。
	node.discovery.close()
}

// stopStarted 按 started 的严格反序执行唯一一次清理。
func (node *Node) stopStarted(ctx context.Context, rollback bool) error {
	var result error
	for index := len(node.started) - 1; index >= 0; index-- {
		entry := node.started[index]
		// 停止准入前先删除监听器并唤醒当前 Service 的发现等待，避免排空被新变化延长。
		node.discovery.removeOwner(entry, errs.ErrServiceStopping)
		// 先在 Scheduler 锁内关闭新任务和新 Timer 的准入，再向业务发布 Stopping。
		// 两者的固定顺序消除“业务已经看到 Stopping、旧创建方却仍提交 Timer”的竞态。
		if err := service.BeginStopScheduler(entry.instance); err != nil {
			result = errors.Join(result, &lifecycleContext{
				nodeID:      node.id,
				serviceName: entry.name,
				phase:       "scheduler_begin_stop",
				cause:       err,
			})
		}
		// started 只由 Start 追加一次；清理后置空可以让重复 Stop 保持幂等。
		entry.state.Store(uint32(service.StateStopping))

		// Scheduler 先拒绝新的根任务并排空已经接受的工作。它完全退出后 OnStop 才能安全
		// 访问 Service 状态，避免与旧 Task 并发。
		if err := service.StopScheduler(ctx, entry.instance); err != nil {
			result = errors.Join(result, &lifecycleContext{
				nodeID:      node.id,
				serviceName: entry.name,
				phase:       "scheduler_stop",
				cause:       err,
			})
		}
		err := callLifecycle(entry, "on_stop", func() error {
			return entry.instance.OnStop(ctx)
		})
		if err != nil {
			result = errors.Join(result, err)
		}
		if rollback && entry.startError {
			entry.state.Store(uint32(service.StateFailed))
		} else {
			entry.state.Store(uint32(service.StateStopped))
		}
	}
	node.started = node.started[:0]
	return result
}

// NodeID 实现 service.Runtime。
func (runtime *serviceRuntime) NodeID() string {
	return runtime.node.id
}

// ServiceName 实现 service.Runtime。
func (runtime *serviceRuntime) ServiceName() string {
	return runtime.entry.name
}

// State 实现 service.Runtime，并直接读取 Entry 原子状态。
func (runtime *serviceRuntime) State() service.State {
	return service.State(runtime.entry.state.Load())
}

// Logger 实现 service.Runtime。
func (runtime *serviceRuntime) Logger() originlog.Logger {
	return runtime.entry.logger
}

// LookupService 实现 service.Runtime，只查询当前 Node。
func (runtime *serviceRuntime) LookupService(name string) (service.IService, bool) {
	return runtime.node.Service(name)
}

// AcquireTimerSlot 实现 service.Runtime，并委托当前 Node 的唯一 ID 与共享额度。
func (runtime *serviceRuntime) AcquireTimerSlot() (service.TimerID, bool) {
	return runtime.node.acquireTimerSlot()
}

// ReleaseTimerSlot 实现 service.Runtime，并归还当前 Node 的共享活跃额度。
func (runtime *serviceRuntime) ReleaseTimerSlot() {
	runtime.node.releaseTimerSlot()
}

// TimerLimit 实现 service.Runtime，供每个 Scheduler 建立有界但不预分配的到期队列。
func (runtime *serviceRuntime) TimerLimit() int {
	return int(runtime.node.timerResources.maxTimers)
}

// TimerLocation 实现 service.Runtime，返回 Node 创建后保持只读的统一 Cron 时区。
func (runtime *serviceRuntime) TimerLocation() *time.Location {
	return runtime.node.timerResources.timerLocation
}

// RPC 实现 service.Runtime，返回当前 Node 独占且启动后只读的 RPC Runtime。
func (runtime *serviceRuntime) RPC() any {
	return runtime.node.rpcRuntime
}

// lifecycleContext 允许 Application 在不依赖具体错误类型时提取结构化失败位置。
type lifecycleContext struct {
	nodeID      string
	serviceName string
	phase       string
	cause       error
	panicStack  string
}

// Error 返回包含 Node、Service 和生命周期阶段的诊断文本。
func (failure *lifecycleContext) Error() string {
	// Node 自身资源阶段没有 ServiceName，使用单独文本避免输出含糊的空 Service。
	if failure.serviceName == "" {
		return fmt.Sprintf(
			"Node %q %s 失败: %v",
			failure.nodeID,
			failure.phase,
			failure.cause,
		)
	}
	return fmt.Sprintf(
		"Node %q Service %q %s 失败: %v",
		failure.nodeID,
		failure.serviceName,
		failure.phase,
		failure.cause,
	)
}

// Unwrap 保留业务错误码、errors.Is 和 errors.As 语义。
func (failure *lifecycleContext) Unwrap() error {
	return failure.cause
}

// LifecycleContext 返回 Application 最终错误日志使用的稳定定位字段。
func (failure *lifecycleContext) LifecycleContext() (nodeID, serviceName, phase string) {
	return failure.nodeID, failure.serviceName, failure.phase
}

// PanicStack 返回回调 panic 发生位置的原始堆栈；普通 error 返回空字符串。
func (failure *lifecycleContext) PanicStack() string {
	return failure.panicStack
}

// callLifecycle 在统一 panic 边界执行一个业务生命周期回调。
func callLifecycle(entry *serviceEntry, phase string, callback func() error) (result error) {
	// defer 必须位于业务回调外层，确保任何 panic 都转换为可回滚的 CodeInternal 错误。
	defer func() {
		if value := recover(); value != nil {
			cause := errs.Wrap(
				errs.CodeInternal,
				fmt.Errorf("panic: %v", value),
			)
			result = &lifecycleContext{
				nodeID:      entry.nodeID,
				serviceName: entry.name,
				phase:       phase,
				cause:       cause,
				panicStack:  string(debug.Stack()),
			}
		}
	}()

	// 普通 error 同样只补充定位上下文，不改变其原始错误码和错误链。
	if err := callback(); err != nil {
		return &lifecycleContext{
			nodeID:      entry.nodeID,
			serviceName: entry.name,
			phase:       phase,
			cause:       err,
		}
	}
	return nil
}

// invalidArgument 创建 Node API 使用错误。
func invalidArgument(message string) error {
	return errs.NewMessage(errs.CodeInvalidArgument, message)
}

// invalidConfig 创建 Node 配置错误。
func invalidConfig(message string) error {
	return errs.NewMessage(errs.CodeInvalidConfig, message)
}

// contextFailure 把启动 Context 的取消原因转换为稳定 Origin 错误。
func contextFailure(ctx context.Context) error {
	if ctx == nil || ctx.Err() == nil {
		return nil
	}
	return errs.Wrap(errs.CodeOf(ctx.Err()), ctx.Err())
}
