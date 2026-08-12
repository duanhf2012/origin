package blueprintmodule

import (
	"context"
	"sync"
	"sync/atomic"

	blueprint "github.com/duanhf2012/OriginBlueprint/engine/go/blueprint"
	"github.com/duanhf2012/origin/v3/service"
)

type moduleState uint8

const (
	stateUnconfigured moduleState = iota
	stateConfigured
	stateStarting
	stateRunning
	stateStopping
	stateStopped
)

// Module 管理一个 OriginBlueprint 引擎、全部 Instance 和所属 Service 调度适配的完整生命周期。
//
// 推荐在业务 Module 中匿名嵌入 Module，并在业务 Module.OnInit 中调用 Setup 与 RegisterNodes。通过 New
// 构造的独立 Module 可以直接交给 Service.AddModule。Module 不建立隐藏 Worker Pool 或第二层执行队列。
type Module struct {
	service.Module

	mu               sync.RWMutex
	state            moduleState
	config           Config
	options          moduleOptions
	factories        []NodeFactory
	factoryNames     map[string]struct{}
	engine           *blueprint.Blueprint
	instances        map[int64]*Instance
	stats            moduleStats
	reloadInProgress atomic.Bool
}

// New 校验并冻结配置，返回可直接加入 Service 的 Blueprint Module。
//
// New 不读取文件或编译蓝图；真正的目录加载和全量编译发生在 OnStart。
func New(config Config, options ...Option) (*Module, error) {
	module := &Module{}
	if err := module.configure(config, options...); err != nil {
		return nil, err
	}
	return module, nil
}

// Setup 在已经绑定业务 Module 的 OnInit 中校验并冻结配置。
//
// Setup 只能成功一次且不执行文件 I/O。通过 New 构造的独立 Module 不需要再次调用。
func (module *Module) Setup(config Config, options ...Option) error {
	if module == nil || module.Service() == nil {
		return invalidArgument("blueprintmodule.Setup 只能在已绑定 Module.OnInit 中调用")
	}
	return module.configure(config, options...)
}

func (module *Module) configure(input Config, options ...Option) error {
	if module == nil {
		return ErrInvalidArgument
	}

	// 先在锁外归一化临时值；只有全部配置和 Option 成功后才一次性发布冻结状态。
	config, err := normalizeConfig(input)
	if err != nil {
		return err
	}
	configured := moduleOptions{}
	for _, option := range options {
		if option == nil || isNilInterface(option) {
			return invalidConfig("blueprintmodule Option 不能为空")
		}
		if err = option.apply(&configured); err != nil {
			return err
		}
	}

	module.mu.Lock()
	defer module.mu.Unlock()
	if module.state != stateUnconfigured {
		return ErrAlreadySetup
	}
	module.config = config
	module.options = configured
	module.state = stateConfigured
	return nil
}

// RegisterNodes 在首次 OnStart 前登记一个或多个自定义节点工厂。
//
// 每个工厂在注册时调用一次以验证非空和名称唯一；加载、热加载和每次节点执行还会再次调用，因此每次必须
// 返回全新的节点对象。启动后工厂集合冻结。
func (module *Module) RegisterNodes(factories ...NodeFactory) error {
	if module == nil || len(factories) == 0 {
		return ErrInvalidArgument
	}

	// 在调用方环境预检工厂，避免持有 Module 锁时运行外部构造代码。
	type namedFactory struct {
		name    string
		factory NodeFactory
	}
	prepared := make([]namedFactory, 0, len(factories))
	seen := make(map[string]struct{}, len(factories))
	for _, factory := range factories {
		if factory == nil {
			return invalidArgument("blueprintmodule NodeFactory 不能为空")
		}
		node := factory()
		if isNilInterface(node) || node.GetName() == "" {
			return invalidArgument("blueprintmodule NodeFactory 必须返回具有名称的新节点")
		}
		name := node.GetName()
		if _, exists := seen[name]; exists {
			return invalidArgument("blueprintmodule 本次注册包含重复节点名称")
		}
		seen[name] = struct{}{}
		prepared = append(prepared, namedFactory{name: name, factory: factory})
	}

	module.mu.Lock()
	defer module.mu.Unlock()
	if module.state != stateConfigured {
		return ErrNotRunning
	}
	for name := range seen {
		if _, duplicate := module.factoryNames[name]; duplicate {
			return invalidArgument("blueprintmodule 节点名称已经注册")
		}
	}
	if module.factoryNames == nil {
		module.factoryNames = make(map[string]struct{}, len(prepared))
	}
	for _, item := range prepared {
		module.factories = append(module.factories, item.factory)
		module.factoryNames[item.name] = struct{}{}
	}
	return nil
}

// OnInit 验证通过 New 独立加入 Service 的 Module 已完成配置。
func (module *Module) OnInit() error {
	if module == nil {
		return ErrInvalidArgument
	}
	module.mu.RLock()
	configured := module.state == stateConfigured
	module.mu.RUnlock()
	if !configured {
		return ErrNotSetup
	}
	return nil
}

// OnStart 加载节点定义和蓝图目录，并在全部编译成功后发布唯一运行引擎。
func (module *Module) OnStart(ctx context.Context) error {
	if module == nil || ctx == nil {
		return ErrInvalidArgument
	}
	if err := ctx.Err(); err != nil {
		return err
	}

	// 冻结启动快照并切换到 Starting，防止并发注册和重复启动。
	module.mu.Lock()
	if module.state != stateConfigured {
		module.mu.Unlock()
		return ErrNotSetup
	}
	module.state = stateStarting
	config := module.config
	options := module.options
	factories := append([]NodeFactory(nil), module.factories...)
	module.mu.Unlock()

	// 引擎只在局部对象上完成注册、加载和编译；失败时关闭临时对象，不发布半初始化 Runtime。
	engine := &blueprint.Blueprint{}
	engine.SetExecutionDispatcher(&serviceDispatcher{module: module})
	if options.traceLogger != nil {
		engine.SetTraceLogger(options.traceLogger)
	}
	if options.diagnosticSink != nil {
		engine.SetDiagnosticSink(options.diagnosticSink)
	} else {
		engine.SetDiagnosticSink(&moduleDiagnosticSink{logger: module.Logger()})
	}
	for _, factory := range factories {
		engine.RegisterExecNode(factory)
	}
	if err := engine.Init(config.NodeDir, config.GraphDir, nil); err != nil {
		_ = engine.Close()
		module.mu.Lock()
		module.state = stateStopped
		module.mu.Unlock()
		return err
	}
	// Init 本身没有 Context 参数；若加载期间启动预算到期，必须关闭完整临时引擎而不能继续发布 Running。
	if err := ctx.Err(); err != nil {
		_ = engine.Close()
		module.mu.Lock()
		module.state = stateStopped
		module.mu.Unlock()
		return err
	}

	// 成功后一次性发布；运行路径从此只读取这个引擎实例。
	module.mu.Lock()
	module.engine = engine
	if module.instances == nil {
		module.instances = make(map[int64]*Instance)
	}
	module.state = stateRunning
	module.mu.Unlock()
	return nil
}

// OnStop 关闭准入和底层引擎，并取消全部尚未完成的执行；重复停止安全。
func (module *Module) OnStop(ctx context.Context) error {
	if module == nil || ctx == nil {
		return ErrInvalidArgument
	}

	// 先从公开状态撤下引擎，使停止开始后所有新操作稳定失败。
	module.mu.Lock()
	switch module.state {
	case stateUnconfigured, stateConfigured, stateStopped:
		module.state = stateStopped
		module.mu.Unlock()
		return nil
	case stateRunning:
		module.state = stateStopping
		engine := module.engine
		instances := make([]*Instance, 0, len(module.instances))
		for _, instance := range module.instances {
			instances = append(instances, instance)
		}
		module.engine = nil
		module.mu.Unlock()
		for _, instance := range instances {
			_ = instance.Close()
		}
		if engine != nil {
			if err := engine.Close(); err != nil {
				module.mu.Lock()
				module.state = stateStopped
				module.mu.Unlock()
				return err
			}
		}
		module.mu.Lock()
		module.state = stateStopped
		module.mu.Unlock()
		return nil
	default:
		module.mu.Unlock()
		return ErrNotRunning
	}
}

func (module *Module) runningEngine() (*blueprint.Blueprint, error) {
	if module == nil {
		return nil, ErrInvalidArgument
	}
	module.mu.RLock()
	defer module.mu.RUnlock()
	if module.state != stateRunning || module.engine == nil {
		return nil, ErrNotRunning
	}
	return module.engine, nil
}
