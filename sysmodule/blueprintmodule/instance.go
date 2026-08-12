package blueprintmodule

import (
	"context"
	"sync"
)

// InstanceOption 是长期蓝图 Instance 的封闭构造选项。
type InstanceOption interface{ apply(*instanceOptions) error }

type instanceOptionFunc func(*instanceOptions) error

func (fn instanceOptionFunc) apply(options *instanceOptions) error { return fn(options) }

type instanceOptions struct{ key string }

// WithKey 为 Instance 增加战斗 ID、玩家 ID 或会话 ID 等业务诊断信息。
//
// Key 不参与查找和唯一性判断，也不建立进程级实例注册中心。
func WithKey(key string) InstanceOption {
	return instanceOptionFunc(func(options *instanceOptions) error {
		options.key = key
		return nil
	})
}

// noCopy 让 go vet copylocks 检测 Instance 的错误值复制。
type noCopy struct{}

func (*noCopy) Lock()   {}
func (*noCopy) Unlock() {}

// Instance 表示一个由 Module 拥有、由业务显式关闭的蓝图图身份。
//
// Instance 必须通过指针使用，允许多个字段借用同一个指针，但只能有一个业务所有者负责 Close。多次 Run/Start
// 共享图名身份，不共享每次 Execution 的变量、VM 或调用栈。
type Instance struct {
	noCopy noCopy

	mu     sync.RWMutex
	module *Module
	id     int64
	name   string
	key    string
	closed bool
}

// Create 创建绑定 graphName 的长期 Instance。
func (module *Module) Create(graphName string, options ...InstanceOption) (*Instance, error) {
	if module == nil || graphName == "" {
		return nil, ErrInvalidArgument
	}
	configured := instanceOptions{}
	for _, option := range options {
		if option == nil || isNilInterface(option) {
			return nil, ErrInvalidArgument
		}
		if err := option.apply(&configured); err != nil {
			return nil, err
		}
	}

	// 在 Module 读锁内线性化运行状态检查和底层 Create，阻止 Stop 在中间撤下引擎。
	module.mu.Lock()
	defer module.mu.Unlock()
	if module.state != stateRunning || module.engine == nil {
		return nil, ErrNotRunning
	}
	id := module.engine.Create(graphName)
	if id == 0 {
		return nil, ErrGraphNotFound
	}
	instance := &Instance{module: module, id: id, name: graphName, key: configured.key}
	if module.instances == nil {
		module.instances = make(map[int64]*Instance)
	}
	module.instances[id] = instance
	module.stats.createdTotal.Add(1)
	return instance, nil
}

// Run 创建一次独立 Execution，并以协作等待方式返回最终端口结果。
func (instance *Instance) Run(ctx context.Context, entranceID int64, args ...any) (PortArray, error) {
	execution, err := instance.Start(ctx, entranceID, args...)
	if err != nil {
		return nil, err
	}
	if execution.IsDone() {
		return execution.Result()
	}

	// Await 的等待函数只观察 Done；等待期间 Service 释放执行权，让 Resume 任务可以继续蓝图。
	err = instance.module.Await(ctx, func(waitCtx context.Context) error {
		select {
		case <-execution.Done():
			return nil
		case <-waitCtx.Done():
			execution.Cancel()
			<-execution.Done()
			return waitCtx.Err()
		}
	})
	if err != nil {
		execution.Cancel()
		return nil, err
	}
	return execution.Result()
}

// Start 在当前 Service 工作协程执行蓝图，直到同步终态或首次 Yield，并返回执行句柄。
func (instance *Instance) Start(ctx context.Context, entranceID int64, args ...any) (*Execution, error) {
	if instance == nil || ctx == nil {
		return nil, ErrInvalidArgument
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	instance.mu.RLock()
	if instance.closed || instance.module == nil {
		instance.mu.RUnlock()
		return nil, ErrInstanceClosed
	}
	module, id := instance.module, instance.id
	instance.mu.RUnlock()

	// 只在短锁内取得引擎快照。engine.Start 的首次节点会内联执行业务代码，不能跨该调用持有包装层锁；
	// Stop/Close 若在解锁后先线性化，底层引擎会以 Closed/Released 错误安全拒绝本次启动。
	module.mu.RLock()
	if module.state != stateRunning || module.engine == nil {
		module.mu.RUnlock()
		return nil, ErrNotRunning
	}
	engine := module.engine
	module.mu.RUnlock()
	underlying, err := engine.Start(ctx, id, entranceID, args...)
	if err != nil {
		return nil, err
	}
	module.stats.startedTotal.Add(1)
	return &Execution{execution: underlying, module: module, context: ctx}, nil
}

// ID 返回底层实例 ID，仅用于日志和诊断，不能用于绕过 Instance 执行。
func (instance *Instance) ID() int64 {
	if instance == nil {
		return 0
	}
	return instance.id
}

// Name 返回 Instance 绑定的蓝图名称。
func (instance *Instance) Name() string {
	if instance == nil {
		return ""
	}
	return instance.name
}

// Key 返回 WithKey 设置的业务诊断信息。
func (instance *Instance) Key() string {
	if instance == nil {
		return ""
	}
	return instance.key
}

// Close 幂等释放底层图身份，并取消该 Instance 尚未完成的 Execution。
func (instance *Instance) Close() error {
	if instance == nil {
		return ErrInvalidArgument
	}
	instance.mu.Lock()
	if instance.closed {
		instance.mu.Unlock()
		return nil
	}
	instance.closed = true
	module, id := instance.module, instance.id
	instance.mu.Unlock()
	if module == nil {
		return nil
	}

	// 与 Stop/Create 共用 Module 锁，保证释放和实例索引注销是单一线性化事务。
	module.mu.Lock()
	if module.engine != nil {
		module.engine.ReleaseGraph(id)
	}
	if _, exists := module.instances[id]; exists {
		delete(module.instances, id)
		module.stats.closedTotal.Add(1)
	}
	module.mu.Unlock()
	return nil
}

// Run 自动创建临时 Instance，执行到终态后立即释放。
func (module *Module) Run(ctx context.Context, graphName string, entranceID int64, args ...any) (PortArray, error) {
	if module == nil || ctx == nil {
		return nil, ErrInvalidArgument
	}
	instance, err := module.Create(graphName)
	if err != nil {
		return nil, err
	}
	defer instance.Close()
	return instance.Run(ctx, entranceID, args...)
}
