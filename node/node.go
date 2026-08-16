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

	originconfig "github.com/duanhf2012/origin/v3/config"
	publicprovider "github.com/duanhf2012/origin/v3/discovery/provider"
	"github.com/duanhf2012/origin/v3/errs"
	"github.com/duanhf2012/origin/v3/internal/bufferpool"
	internaldiscovery "github.com/duanhf2012/origin/v3/internal/discovery"
	origindiscovery "github.com/duanhf2012/origin/v3/internal/discovery/origin"
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
	// config 是 Application 一次加载后冻结的根视图；所有 Service 仅持有派生 View。
	config originconfig.View
	// schedulerConfig 是当前 Node 为每个 ServiceScheduler 提供的冻结默认策略。
	schedulerConfig service.SchedulerConfig
	// application 是 Application 注入的受限进程外观，装配后保持只读。
	application service.ApplicationRuntime
	// state 为查询提供无锁快照，生命周期写入由单一控制路径串行执行。
	state atomic.Uint32
	// gameTimeOffset 是当前 Node 相对真实时间的纳秒偏移；Now 热路径只执行原子读取。
	gameTimeOffset atomic.Int64
	// gameTimeMu 串行化低频 Set/Add，并在后续同时保护 Timer 重排和停止准入边界。
	gameTimeMu sync.Mutex
	// stopMu/stopComplete 把正常 Stop 与启动失败 Rollback 串行化，并保留首次最终结果。
	stopMu       sync.Mutex
	stopComplete bool
	stopResult   error
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
	// discovery 是当前 Node 独占的可见目录；source/subscription 只属于进程内发现源。
	discovery             *discoveryRuntime
	discoverySource       *internaldiscovery.Source
	discoverySubscription *internaldiscovery.Subscription
	discoveryPublished    atomic.Bool
	discoveryProvider     *providerRuntime
	discoveryPublication  *discoveryPublication
	discoveryServer       discoveryServer
	// discoveryAvailable 和三个原子快照只在生命周期、恢复和故障冷路径更新。
	discoveryAvailable atomic.Bool
	transportStatus    atomic.Pointer[transportStatusSnapshot]
	healthStatus       atomic.Pointer[healthStatusSnapshot]
	discoveryStatus    atomic.Pointer[discoveryStatusSnapshot]
	// publicServices 在静态装配后冻结，用于区分“没有公开 Service”和“全部公开 Service 失败”。
	publicServices int
	// serviceFailure 把真正隔离的 Service 摘要交给 Application，但不触发 Stop。
	serviceFailure func(nodeID string, serviceName string, cause error)
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
	config              originconfig.View
	logger              originlog.Logger
	state               atomic.Pointer[serviceStateSnapshot]
	startError          bool
	contractID          uint64
	contractFingerprint [32]byte
	// discoveryRun 是当前 Service 唯一且稳定的发现状态同步函数。
	discoveryRun func(context.Context)
	// failure 只保存第一个无法恢复根因，生命周期结束后仍保留供本地诊断。
	failure atomic.Pointer[serviceFailureSnapshot]
}

type serviceStateSnapshot struct {
	State     service.State
	EnteredAt time.Time
}

// serviceRuntime 把 Service 的只读查询限制在所属 Node 和当前实例。
type serviceRuntime struct {
	node  *Node
	entry *serviceEntry
}

// 编译期固定生产 Runtime 必须完整实现业务可见的最小 Node 时间外观。
var _ service.NodeRuntime = (*serviceRuntime)(nil)

// prepareRPCDispatchers 在创建 Node 资源和绑定 Service Runtime 前完成静态契约预检。
// 成功返回的 Dispatcher 与 bindings 顺序一一对应，nil 表示普通非 RPC Service。
func prepareRPCDispatchers(
	nodeID string,
	bindings []ServiceBinding,
) ([]rpc.Dispatcher, error) {
	dispatchers := make([]rpc.Dispatcher, len(bindings))
	seenNames := make(map[string]struct{}, len(bindings))
	for index, binding := range bindings {
		if binding.Name == "" || binding.Template == "" || binding.Service == nil {
			return nil, invalidConfig(fmt.Sprintf("Node %q 包含无效 Service 绑定", nodeID))
		}
		if _, exists := seenNames[binding.Name]; exists {
			return nil, invalidConfig(fmt.Sprintf(
				"Node %q 的 ServiceName %q 重复",
				nodeID,
				binding.Name,
			))
		}
		seenNames[binding.Name] = struct{}{}

		descriptor, found, err := rpc.FindGeneratedContract(binding.Template)
		if err != nil {
			return nil, invalidConfig(fmt.Sprintf(
				"Node %q 装配 Service %q 的 RPC 契约失败: %v",
				nodeID,
				binding.Name,
				err,
			))
		}
		if !found {
			continue
		}
		dispatcher, compatible := descriptor.NewDispatcher(binding.Service)
		if !compatible {
			return nil, invalidConfig(fmt.Sprintf(
				"Node %q Service %q（模板 %q，类型 %T）未实现 RPC 契约 %q",
				nodeID,
				binding.Name,
				binding.Template,
				binding.Service,
				descriptor.ContractName,
			))
		}
		if dispatcher == nil {
			return nil, invalidConfig(fmt.Sprintf(
				"Node %q Service %q 的 RPC 契约 %q 返回空 Dispatcher",
				nodeID,
				binding.Name,
				descriptor.ContractName,
			))
		}
		dispatchers[index] = dispatcher
	}
	return dispatchers, nil
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
	dispatchers, err := prepareRPCDispatchers(config.ID, bindings)
	if err != nil {
		return nil, err
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
		logger.WithScope(config.ID, ""),
	)
	if err != nil {
		return nil, fmt.Errorf("创建 Node %q RPC Runtime: %w", config.ID, err)
	}
	if err := rpcRuntime.Configure(config.RPC); err != nil {
		return nil, fmt.Errorf("配置 Node %q RPC Runtime: %w", config.ID, err)
	}
	if options.DiscoveryKind == "origin" {
		if err := rpcRuntime.EnableSystem(); err != nil {
			return nil, fmt.Errorf("启用 Node %q Discovery 系统 RPC: %w", config.ID, err)
		}
		options.DiscoveryFactory = origindiscovery.NewFactory(
			rpcRuntime,
			options.DiscoverySystemTarget,
		)
	}

	// 按已知数量一次分配有序表和查询表，避免装配时重复扩容。
	instance := &Node{
		id:              config.ID,
		sessionID:       sessionID,
		private:         config.Private,
		labels:          cloneLabels(config.Labels),
		logger:          logger.WithScope(config.ID, ""),
		config:          options.Config.Root(),
		schedulerConfig: config.Scheduler,
		application:     options.Application,
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
		serviceFailure:  options.ServiceFailure,
	}
	discoveryRuntime.bindNode(instance)
	if options.DiscoveryFactory != nil {
		if options.DiscoverySource != nil {
			return nil, invalidConfig(fmt.Sprintf(
				"Node %q 不能同时配置外部 Provider 与进程内 Source",
				config.ID,
			))
		}
		discoveryProvider, providerErr := newProviderRuntime(
			instance,
			options.DiscoveryKind,
			options.DiscoveryConfig,
			options.DiscoveryFactory,
		)
		if providerErr != nil {
			return nil, fmt.Errorf(
				"创建 Node %q Discovery Provider: %w",
				config.ID,
				providerErr,
			)
		}
		instance.discoveryProvider = discoveryProvider
	}
	if err := rpcRuntime.BindLocalLabels(instance.labels); err != nil {
		return nil, fmt.Errorf("绑定 Node %q RPC 本地标签: %w", config.ID, err)
	}
	if err := rpcRuntime.BindSessionID(sessionID); err != nil {
		return nil, fmt.Errorf("绑定 Node %q RPC SessionID: %w", config.ID, err)
	}
	if err := rpcRuntime.BindRemoteResolver(discoveryRuntime); err != nil {
		return nil, fmt.Errorf("绑定 Node %q RPC 服务发现目录: %w", config.ID, err)
	}
	if err := rpcRuntime.BindTransportObserver(instance.handleTransportEvent); err != nil {
		return nil, fmt.Errorf("绑定 Node %q RPC 状态观察器: %w", config.ID, err)
	}
	instance.state.Store(uint32(StateCreated))

	// 所有 Service 必须先完成创建、登记和 Runtime 绑定，之后才允许调用第一个 OnInit。
	for index, binding := range bindings {
		// Entry 先建立完整稳定身份，再把只读 Runtime 交给业务基础对象。
		entry := &serviceEntry{
			nodeID:   config.ID,
			name:     binding.Name,
			template: binding.Template,
			private:  binding.Private,
			instance: binding.Service,
			config:   selectServiceConfig(options.Config, config.ID, binding.Name),
			logger:   instance.logger.WithScope(config.ID, binding.Name),
		}
		if server, ok := binding.Service.(discoveryServer); ok {
			if instance.discoveryServer != nil {
				return nil, invalidConfig(fmt.Sprintf(
					"Node %q 配置了多个 DiscoveryService",
					config.ID,
				))
			}
			instance.discoveryServer = server
		}
		entry.discoveryRun = func(ctx context.Context) {
			instance.discovery.deliver(ctx, entry)
		}
		entry.setState(service.StateCreated)
		runtime := &serviceRuntime{node: instance, entry: entry}
		if err := service.BindRuntime(binding.Service, runtime); err != nil {
			return nil, fmt.Errorf(
				"绑定 Node %q Service %q: %w",
				config.ID,
				binding.Name,
				err,
			)
		}
		dispatcher := dispatchers[index]
		if dispatcher != nil {
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
		if !config.Private && !binding.Private {
			instance.publicServices++
		}
	}
	if binder, ok := instance.discoveryServer.(discoverySystemBinder); ok {
		if err := binder.BindSystemRPC(instance.rpcRuntime); err != nil {
			return nil, fmt.Errorf("绑定 Node %q Discovery 系统 RPC: %w", config.ID, err)
		}
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
			_ = rpcRuntime.Close(context.Background())
			return nil, fmt.Errorf("订阅 Node %q 过渡服务发现: %w", config.ID, err)
		}
		instance.discoverySubscription = subscription
	}
	instance.initializeStatus(rpcRuntime.TransportKind())
	if !instance.private &&
		(instance.discoveryProvider != nil || instance.discoverySource != nil) {
		instance.discoveryPublication = newDiscoveryPublication(instance)
	}
	return instance, nil
}

func (entry *serviceEntry) setState(state service.State) {
	for {
		current := entry.state.Load()
		if current != nil && current.State == state {
			return
		}
		next := &serviceStateSnapshot{State: state, EnteredAt: time.Now()}
		if entry.state.CompareAndSwap(current, next) {
			return
		}
	}
}

func (entry *serviceEntry) loadState() serviceStateSnapshot {
	if entry == nil {
		return serviceStateSnapshot{}
	}
	snapshot := entry.state.Load()
	if snapshot == nil {
		return serviceStateSnapshot{State: service.StateCreated}
	}
	return *snapshot
}

// selectServiceConfig 只按实际 ServiceName 选择一块完整业务配置。
//
// Node 专属块优先；不存在时才使用全局块。两个块绝不合并，也不读取模板名。
func selectServiceConfig(
	snapshot *originconfig.Snapshot,
	nodeID string,
	serviceName string,
) originconfig.View {
	if snapshot == nil {
		return originconfig.View{}
	}
	root := snapshot.Root()
	if configured, err := root.Lookup(
		"node_services." + nodeID + "." + serviceName,
	); err == nil {
		return configured
	}
	if configured, err := root.Lookup("services." + serviceName); err == nil {
		return configured
	}
	return originconfig.View{}
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

// SessionID 返回当前 Node 本次进程启动的随机会话标识。
// 该值只读且不由业务配置，用于拒绝旧进程遗留的精确实例路由。
func (node *Node) SessionID() uint64 {
	if node == nil {
		return 0
	}
	return node.sessionID
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
//
// Deprecated: 业务普通日志使用 log.Xxx，Service 与 Module 使用各自的 Logger。该方法仍是
// 当前已确认外观的一部分；是否删除必须经过单独的外观决策。
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
	node.refreshHealth()
	node.logger.Info("node starting")

	// 第一阶段只执行纯初始化。业务 OnInit 错误不会跳过后续 Service，确保一次启动能够
	// 报告完整配置/装配问题；只有外部 Context 已经结束时才不再开始新的生命周期回调。
	var initializationErrors error
	for _, entry := range node.services {
		if err := contextFailure(ctx); err != nil {
			node.state.Store(uint32(StateFailed))
			node.refreshHealth()
			return errors.Join(initializationErrors, &lifecycleContext{
				nodeID:      node.id,
				serviceName: entry.name,
				phase:       "on_init",
				cause:       err,
			})
		}
		entry.setState(service.StateInitializing)
		if err := service.BeginModuleInitialization(entry.instance); err != nil {
			entry.setState(service.StateFailed)
			entry.recordFailure(err)
			initializationErrors = errors.Join(initializationErrors, &lifecycleContext{
				nodeID:      node.id,
				serviceName: entry.name,
				phase:       "module_init_prepare",
				cause:       err,
			})
			continue
		}
		err := callLifecycle(entry, "on_init", func() error {
			return entry.instance.OnInit()
		})
		moduleErr := service.CompleteModuleInitialization(entry.instance, err == nil)
		err = errors.Join(err, moduleErr)
		if err != nil {
			entry.setState(service.StateFailed)
			entry.recordFailure(err)
			initializationErrors = errors.Join(initializationErrors, err)
			continue
		}
		entry.setState(service.StateInitialized)
	}
	if initializationErrors != nil {
		node.state.Store(uint32(StateFailed))
		node.refreshHealth()
		return initializationErrors
	}
	// 最后一个 OnInit 可能在执行期间观察到外部停止并取消启动 Context。进入任何
	// Timer、Transport 或 Discovery 资源阶段前重新裁决，避免为已经取消的启动创建资源。
	if err := contextFailure(ctx); err != nil {
		node.state.Store(uint32(StateFailed))
		node.refreshHealth()
		return &lifecycleContext{
			nodeID:      node.id,
			serviceName: node.services[len(node.services)-1].name,
			phase:       "on_start",
			cause:       err,
		}
	}

	// 全部 OnInit 成功后启动 Node 唯一时间轮，使每个 OnStart 都能依赖统一 Deadline 能力。
	if err := node.timerEngine.Start(); err != nil {
		node.state.Store(uint32(StateFailed))
		node.refreshHealth()
		return &lifecycleContext{
			nodeID: node.id,
			phase:  "timer_engine_start",
			cause:  err,
		}
	}
	// TCP Listener 和出站连接依赖已经运行的 DeadlineQueue，并且必须先于 OnStart
	// 建立，使启动逻辑可以调用已经可达的远端 Service。
	if err := node.rpcRuntime.StartNetwork(ctx, node.timerEngine); err != nil {
		node.state.Store(uint32(StateFailed))
		node.refreshHealth()
		return &lifecycleContext{
			nodeID: node.id,
			phase:  "rpc_network_start",
			cause:  err,
		}
	}
	// 共置 DiscoveryService 必须先准备控制 Listener，使当前 Node 自己的 Provider 可以完成
	// 首次同步；Prepare 不执行用户回调，也不开放业务 RPC。
	if node.discoveryServer != nil {
		if err := node.discoveryServer.PrepareDiscovery(ctx); err != nil {
			node.state.Store(uint32(StateFailed))
			node.refreshHealth()
			return &lifecycleContext{
				nodeID: node.id,
				phase:  "discovery_server_prepare",
				cause:  err,
			}
		}
	}
	// 正式 Provider 的首次权威快照必须先于全部业务 OnStart。未配置 Provider 的本地应用
	// 保持空远端目录；底层装配仍可显式注入进程内 Source。
	if node.discoveryProvider != nil {
		if err := node.discoveryProvider.startProvider(ctx); err != nil {
			node.state.Store(uint32(StateFailed))
			node.updateDiscoveryAvailable(false)
			return &lifecycleContext{
				nodeID: node.id,
				phase:  "discovery_start",
				cause:  err,
			}
		}
	}

	// 时间轮运行后再进入启动阶段。每个 Scheduler 先 Prepare，使 OnStart 可以登记 Timer，
	// 但不启动任何用户任务；Prepare 失败的 Service 尚未进入 OnStart，因此不加入停止序列。
	for _, entry := range node.services {
		// 在进入每个业务回调前观察取消，避免超时后继续启动后续 Service。
		if err := contextFailure(ctx); err != nil {
			node.state.Store(uint32(StateFailed))
			node.refreshHealth()
			return &lifecycleContext{
				nodeID:      node.id,
				serviceName: entry.name,
				phase:       "on_start",
				cause:       err,
			}
		}
		entry.setState(service.StateStarting)
		if err := service.PrepareScheduler(
			entry.instance,
			node.schedulerConfig,
			node.timerEngine,
		); err != nil {
			entry.setState(service.StateFailed)
			node.state.Store(uint32(StateFailed))
			node.refreshHealth()
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
			entry.setState(service.StateFailed)
			node.state.Store(uint32(StateFailed))
			node.refreshHealth()
			return &lifecycleContext{
				nodeID:      node.id,
				serviceName: entry.name,
				phase:       "start_context_prepare",
				cause:       err,
			}
		}
		err = callLifecycle(entry, "on_start", func() error {
			return service.StartWithModules(startContext, entry.instance)
		})
		finishStart()
		if err != nil {
			entry.startError = true
			entry.setState(service.StateFailed)
			node.state.Store(uint32(StateFailed))
			node.refreshHealth()
			return err
		}
	}
	// 最后一个回调可能在执行期间越过 Deadline 却返回 nil，发布 Ready 前必须再次确认。
	if err := contextFailure(ctx); err != nil {
		node.state.Store(uint32(StateFailed))
		node.refreshHealth()
		return &lifecycleContext{
			nodeID:      node.id,
			serviceName: node.services[len(node.services)-1].name,
			phase:       "on_start",
			cause:       err,
		}
	}

	// 全部 OnStart 成功后统一提交 Running 并激活 Runner，任何业务任务都不能与后续
	// Service 的 OnStart 并发。
	for _, entry := range node.started {
		entry.setState(service.StateRunning)
		if err := service.ActivateScheduler(entry.instance); err != nil {
			entry.startError = true
			entry.setState(service.StateFailed)
			node.state.Store(uint32(StateFailed))
			node.refreshHealth()
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
		node.refreshHealth()
		return &lifecycleContext{
			nodeID: node.id,
			phase:  "rpc_inbound_open",
			cause:  err,
		}
	}
	if err := node.publishDiscovery(); err != nil {
		node.state.Store(uint32(StateFailed))
		node.updateDiscoveryAvailable(false)
		node.refreshHealth()
		return &lifecycleContext{
			nodeID: node.id,
			phase:  "discovery_publish",
			cause:  err,
		}
	}
	if node.discoveryPublication != nil {
		node.discoveryPublication.startPublisher()
	}
	node.state.Store(uint32(StateReady))
	node.refreshHealth()
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
	node.stopMu.Lock()
	defer node.stopMu.Unlock()
	if node.stopComplete {
		return node.stopResult
	}
	// 先关闭逻辑时间修改准入，再回收 TimerEngine，避免 SetTime/AddTime
	// 在回滚期间重新登记业务 Deadline。
	node.gameTimeMu.Lock()
	node.state.Store(uint32(StateFailed))
	node.gameTimeMu.Unlock()
	// 失败实例仍先获得反序 OnStop，最后再关闭 Node 时间轮并等待其 goroutine 退出。
	node.stopDiscoveryPublication()
	result := node.withdrawDiscovery(ctx)
	result = errors.Join(result, node.rpcRuntime.BeginStop(ctx))
	result = errors.Join(result, node.stopStarted(ctx, true))
	result = joinProviderClose(result, node.discoveryProvider, ctx)
	if node.discoveryServer != nil {
		result = errors.Join(result, node.discoveryServer.CloseDiscovery(ctx))
	}
	result = errors.Join(result, node.rpcRuntime.Close(ctx))
	result = errors.Join(result, node.timerEngine.Close())
	node.closeDiscoverySubscription()
	node.refreshHealth()
	node.stopComplete = true
	node.stopResult = result
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
	node.stopMu.Lock()
	defer node.stopMu.Unlock()
	if node.stopComplete {
		return node.stopResult
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
	// 时间修改锁只覆盖状态发布：已在执行的重排先完成，之后的修改稳定返回 Stopping。
	node.gameTimeMu.Lock()
	node.state.Store(uint32(StateStopping))
	node.gameTimeMu.Unlock()
	node.refreshHealth()
	node.logger.Info("node stopping")
	node.stopDiscoveryPublication()
	result := node.withdrawDiscovery(ctx)
	result = errors.Join(result, node.rpcRuntime.BeginStop(ctx))
	// Service 清理阶段保留时间轮运行，全部 OnStop 返回后才回收 Node 最后的后台资源。
	result = errors.Join(result, node.stopStarted(ctx, false))
	result = joinProviderClose(result, node.discoveryProvider, ctx)
	if node.discoveryServer != nil {
		result = errors.Join(result, node.discoveryServer.CloseDiscovery(ctx))
	}
	result = errors.Join(result, node.rpcRuntime.Close(ctx))
	result = errors.Join(result, node.timerEngine.Close())
	node.closeDiscoverySubscription()
	if result == nil {
		// Scheduler Failed 正常会随 FinalizeScheduler 返回根因；该分支为其他隔离来源保留
		// 兜底，确保清理成功不会把已经记录的 Service Failure 掩盖成正常停止。
		result = node.serviceFailureResult()
	}
	if result != nil {
		// 清理动作已经全部执行，但任一 Service Failed 或停止阶段错误都不能被资源回收成功
		// 掩盖。Node 保留 Failed 终态，Application 将据此返回非零结果。
		node.state.Store(uint32(StateFailed))
		node.refreshHealth()
		node.logger.Error(
			"node stopped with failures",
			originlog.Err(result),
		)
		node.stopComplete = true
		node.stopResult = result
		return result
	}
	node.state.Store(uint32(StateStopped))
	node.refreshHealth()
	node.logger.Info("node stopped")
	node.stopComplete = true
	node.stopResult = nil
	return result
}

// publishDiscovery 在全部 OnStart 和 Runner 激活成功后整体发布当前 Node 的公开 Service。
func (node *Node) publishDiscovery() error {
	operationCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return node.publishDiscoveryContext(operationCtx)
}

func (node *Node) publishDiscoveryContext(ctx context.Context) error {
	if node.private {
		return nil
	}
	transportState := node.TransportStatus().State
	if transportState == TransportRecovering || transportState == TransportFailed {
		return node.withdrawDiscovery(ctx)
	}
	services := make([]internaldiscovery.RawService, 0, len(node.services))
	for _, entry := range node.services {
		state := entry.loadState().State
		if entry.private ||
			state == service.StateFailed ||
			state == service.StateStopping ||
			state == service.StateStopped {
			continue
		}
		discoveryState := internaldiscovery.ServiceStateRunning
		if state == service.StateRetired {
			discoveryState = internaldiscovery.ServiceStateRetired
		}
		services = append(services, internaldiscovery.RawService{
			ServiceName:         entry.name,
			State:               discoveryState,
			ContractID:          entry.contractID,
			ContractFingerprint: entry.contractFingerprint,
		})
	}
	// 全部 Service 均为私有时没有远端可见事实，不发布空 Node 记录。
	if len(services) == 0 {
		return node.withdrawDiscovery(ctx)
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
	raw := internaldiscovery.RawNode{
		NodeID:    node.id,
		SessionID: node.sessionID,
		Labels:    cloneLabels(node.labels),
		Transport: transport,
		Address:   address,
		Services:  services,
	}
	if node.discoveryProvider != nil {
		providerNode := publicProviderNode(raw)
		if err := node.discoveryProvider.publish(ctx, providerNode); err != nil {
			return err
		}
	} else if node.discoverySource != nil {
		if err := node.discoverySource.Publish(raw); err != nil {
			return err
		}
	} else {
		return nil
	}
	node.discoveryPublished.Store(true)
	return nil
}

// withdrawDiscovery 在正式停止新业务准入前撤销当前精确 Node 会话。
func (node *Node) withdrawDiscovery(ctx context.Context) error {
	if node == nil || !node.discoveryPublished.Load() {
		return nil
	}
	if node.discoveryProvider != nil {
		if err := node.discoveryProvider.withdraw(ctx); err != nil {
			return err
		}
		node.discoveryPublished.Store(false)
		return nil
	}
	if node.discoverySource != nil {
		node.discoverySource.Withdraw(node.id, node.sessionID)
	}
	node.discoveryPublished.Store(false)
	return nil
}

// publicProviderNode 把内部发布 DTO 转换成后端无关公共 DTO。
func publicProviderNode(raw internaldiscovery.RawNode) publicprovider.Node {
	result := publicprovider.Node{
		NodeID:    raw.NodeID,
		SessionID: raw.SessionID,
		Labels:    cloneLabels(raw.Labels),
		Transport: publicprovider.Transport(raw.Transport + 1),
		Address:   raw.Address,
		Services:  make([]publicprovider.Service, len(raw.Services)),
	}
	for index, service := range raw.Services {
		result.Services[index] = publicprovider.Service{
			ServiceName:         service.ServiceName,
			State:               publicprovider.ServiceState(service.State),
			ContractID:          service.ContractID,
			ContractFingerprint: service.ContractFingerprint,
		}
	}
	return result
}

// closeDiscoverySubscription 幂等关闭当前 Node 对进程内完整快照源的订阅。
func (node *Node) closeDiscoverySubscription() {
	if node == nil {
		return
	}
	if node.discoverySubscription != nil {
		node.discoverySubscription.Close()
		node.discoverySubscription = nil
	}
	// 即使独立 node.New 没有进程内 Source，也必须使查询、等待和监听外观随 Node 一起失效。
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
		entry.setState(service.StateStopping)

		// Scheduler 排空后由最后一个 Service Runner 独占执行 OnStop。OnStop 的 Context
		// 携带 finalizer 令牌，因此可以顺序 Await，但不能重新开放普通任务或 Timer。
		if err := service.FinalizeScheduler(
			ctx,
			entry.instance,
			func(finalizerContext context.Context) error {
				return service.StopWithModules(finalizerContext, entry.instance)
			},
		); err != nil {
			result = errors.Join(result, &lifecycleContext{
				nodeID:      node.id,
				serviceName: entry.name,
				phase:       "service_finalize",
				cause:       err,
			})
		}
		if entry.failureCause() != nil || (rollback && entry.startError) {
			entry.setState(service.StateFailed)
		} else {
			entry.setState(service.StateStopped)
		}
	}
	node.started = node.started[:0]
	return result
}

// NodeID 实现 service.Runtime。
func (runtime *serviceRuntime) NodeID() string {
	return runtime.node.id
}

// ID 实现 service.NodeRuntime，并返回所属 Node 的稳定身份。
func (runtime *serviceRuntime) ID() string {
	if runtime == nil || runtime.node == nil {
		return ""
	}
	return runtime.node.ID()
}

// SessionID 实现 service.NodeRuntime，并返回所属 Node 本次启动的会话标识。
func (runtime *serviceRuntime) SessionID() uint64 {
	if runtime == nil || runtime.node == nil {
		return 0
	}
	return runtime.node.SessionID()
}

// Now 实现 service.NodeRuntime，读取所属 Node 的游戏逻辑时间。
func (runtime *serviceRuntime) Now() time.Time {
	if runtime == nil || runtime.node == nil {
		return time.Time{}
	}
	return runtime.node.Now()
}

// SetTime 实现 service.NodeRuntime，设置所属 Node 的游戏逻辑时间。
func (runtime *serviceRuntime) SetTime(value time.Time) error {
	if runtime == nil || runtime.node == nil {
		return invalidArgument("Service Runtime 没有所属 Node")
	}
	return runtime.node.SetTime(value)
}

// AddTime 实现 service.NodeRuntime，调整所属 Node 的游戏逻辑时间偏移。
func (runtime *serviceRuntime) AddTime(delta time.Duration) error {
	if runtime == nil || runtime.node == nil {
		return invalidArgument("Service Runtime 没有所属 Node")
	}
	return runtime.node.AddTime(delta)
}

// ServiceName 实现 service.Runtime。
func (runtime *serviceRuntime) ServiceName() string {
	return runtime.entry.name
}

// State 实现 service.Runtime，并直接读取 Entry 原子状态。
func (runtime *serviceRuntime) State() service.State {
	return runtime.entry.loadState().State
}

// Logger 实现 service.Runtime。
func (runtime *serviceRuntime) Logger() originlog.Logger {
	return runtime.entry.logger
}

// LookupLocalService 实现 service.Runtime，只查询当前 Node。
func (runtime *serviceRuntime) LookupLocalService(name string) (service.IService, bool) {
	return runtime.node.Service(name)
}

// RootConfig 实现 service 的可选配置适配面。
func (runtime *serviceRuntime) RootConfig() originconfig.View {
	return runtime.node.config
}

// ServiceConfig 实现 service 的可选配置适配面。
func (runtime *serviceRuntime) ServiceConfig() originconfig.View {
	return runtime.entry.config
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

// Failure 实现 service.Runtime，并返回当前 Service 首个不可恢复根因。
func (runtime *serviceRuntime) Failure() error {
	if runtime == nil || runtime.entry == nil {
		return nil
	}
	return runtime.entry.failureCause()
}

// ReportFailure 实现 service.Runtime，把 Scheduler 隔离结果提交给 Node 冷路径。
func (runtime *serviceRuntime) ReportFailure(cause error) {
	if runtime == nil || runtime.node == nil || runtime.entry == nil || cause == nil {
		return
	}
	runtime.node.recordServiceFailure(runtime.entry, cause)
}

// RPC 实现 service.Runtime，返回当前 Node 独占且启动后只读的 RPC Runtime。
func (runtime *serviceRuntime) RPC() any {
	return runtime.node.rpcRuntime
}

// Application 返回 Application 在 Node 装配期注入的受限进程外观。
func (runtime *serviceRuntime) Application() service.ApplicationRuntime {
	if runtime == nil || runtime.node == nil {
		return nil
	}
	return runtime.node.application
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
