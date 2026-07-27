// Package node 提供按配置顺序拥有并驱动多个 Service 的一次性运行节点。
package node

import (
	"context"
	"errors"
	"fmt"
	"runtime/debug"
	"sync/atomic"

	"github.com/duanhf2012/origin/v3/errs"
	"github.com/duanhf2012/origin/v3/internal/timerwheel"
	originlog "github.com/duanhf2012/origin/v3/log"
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
	id      string
	private bool
	logger  originlog.Logger

	// state 为查询提供无锁快照，生命周期写入由单一控制路径串行执行。
	state atomic.Uint32
	// services 同时提供稳定顺序和按实际名称的 O(1) 本地查询。
	services []*serviceEntry
	byName   map[string]*serviceEntry
	// started 只包含已经进入过 OnStart 的 Service，决定唯一停止顺序。
	started []*serviceEntry
	// timerEngine 是当前 Node 独占的 Deadline 时间轮；它先于 OnStart 启动、晚于 OnStop 关闭。
	timerEngine *timerwheel.Engine
}

// serviceEntry 保存单个 Service 的运行身份和由 Node 拥有的状态。
type serviceEntry struct {
	nodeID     string
	name       string
	template   string
	private    bool
	instance   service.IService
	logger     originlog.Logger
	state      atomic.Uint32
	startError bool
}

// serviceRuntime 把 Service 的只读查询限制在所属 Node 和当前实例。
type serviceRuntime struct {
	node  *Node
	entry *serviceEntry
}

// New 校验绑定数据并创建尚未启动的 Node。
func New(config Config, bindings []ServiceBinding, logger originlog.Logger) (*Node, error) {
	// Node 身份和 Service 列表必须在分配运行对象前完整有效。
	if config.ID == "" {
		return nil, invalidConfig("Node ID 不能为空")
	}
	if len(bindings) == 0 {
		return nil, invalidConfig(fmt.Sprintf("Node %q 没有 Service", config.ID))
	}

	// 按已知数量一次分配有序表和查询表，避免装配时重复扩容。
	instance := &Node{
		id:       config.ID,
		private:  config.Private,
		logger:   logger.With(originlog.String("node_id", config.ID)),
		services: make([]*serviceEntry, 0, len(bindings)),
		byName:   make(map[string]*serviceEntry, len(bindings)),
		started:  make([]*serviceEntry, 0, len(bindings)),
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
		instance.services = append(instance.services, entry)
		instance.byName[binding.Name] = entry
	}

	// 所有 Service 绑定成功后再创建 Node 独占时间轮，避免前序校验失败时遗留底层 Timer。
	timerEngine, err := timerwheel.New(timerwheel.DefaultOptions())
	if err != nil {
		return nil, fmt.Errorf("创建 Node %q TimerEngine: %w", config.ID, err)
	}
	instance.timerEngine = timerEngine
	return instance, nil
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

	// 时间轮运行后再进入启动阶段；started 在调用前追加以保证失败实例也会 OnStop。
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
		node.started = append(node.started, entry)
		err := callLifecycle(entry, "on_start", func() error {
			return entry.instance.OnStart(ctx)
		})
		if err != nil {
			entry.startError = true
			entry.state.Store(uint32(service.StateFailed))
			node.state.Store(uint32(StateFailed))
			return err
		}
		entry.state.Store(uint32(service.StateRunning))
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

	// 所有 Service 成功后一次性发布 Node Ready。
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
	result := node.stopStarted(ctx, true)
	result = errors.Join(result, node.timerEngine.Close())
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
	// Service 清理阶段保留时间轮运行，全部 OnStop 返回后才回收 Node 最后的后台资源。
	result := node.stopStarted(ctx, false)
	result = errors.Join(result, node.timerEngine.Close())
	node.state.Store(uint32(StateStopped))
	node.logger.Info("node stopped")
	return result
}

// stopStarted 按 started 的严格反序执行唯一一次清理。
func (node *Node) stopStarted(ctx context.Context, rollback bool) error {
	var result error
	for index := len(node.started) - 1; index >= 0; index-- {
		entry := node.started[index]
		// started 只由 Start 追加一次；清理后置空可以让重复 Stop 保持幂等。
		entry.state.Store(uint32(service.StateStopping))
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
