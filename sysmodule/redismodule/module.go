package redismodule

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"

	"github.com/bsm/redislock"
	"github.com/duanhf2012/origin/v3/service"
	"github.com/redis/go-redis/v9"
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

type runtimeHolder struct {
	runtime    clientRuntime
	lockClient *redislock.Client
}

// Module 管理一个逻辑 Redis 部署、官方 Client 和锁 Client 的完整生命周期。
//
// 推荐嵌入业务 Module，把 Key 命名、序列化和缓存策略集中在业务边界。Module 自身不创建
// 命令 goroutine、队列、对象池或自动锁续租任务。
type Module struct {
	service.Module
	mu      sync.Mutex
	state   moduleState
	config  Config
	options *redis.UniversalOptions
	hooks   []redis.Hook
	factory runtimeFactory
	runtime atomic.Pointer[runtimeHolder]
	cluster atomic.Bool
	// transitionDone covers the only in-flight start or stop transition.
	transitionDone chan struct{}
	startCancel    context.CancelFunc
	transitionErr  error
}

// New 校验并冻结配置，返回可交给 Service.AddModule 的 Redis Module。
//
// New 不执行网络 I/O；Client 创建与全拓扑 Ping 在 OnStart 中完成。
func New(config Config, options ...Option) (*Module, error) {
	module := &Module{}
	if err := module.configure(config, options...); err != nil {
		return nil, err
	}
	return module, nil
}

// Setup 在已绑定业务 Module 的 OnInit 中校验并冻结 Redis 配置。
//
// Setup 只能成功调用一次且不执行网络 I/O；通过 New 构造的 Module 不需要再次调用。
func (module *Module) Setup(config Config, options ...Option) error {
	if module == nil || module.Service() == nil {
		return ErrNotSetup
	}
	return module.configure(config, options...)
}

func (module *Module) configure(input Config, options ...Option) error {
	if module == nil {
		return ErrInvalidArgument
	}
	module.mu.Lock()
	defer module.mu.Unlock()
	if module.state != stateUnconfigured {
		return ErrAlreadySetup
	}
	current, err := normalizeConfig(input)
	if err != nil {
		return err
	}
	configured := moduleOptions{factory: newDriverRuntime}
	for _, option := range options {
		if option == nil {
			return invalidConfig("redismodule Option 不能为空")
		}
		if err = option.apply(&configured); err != nil {
			return err
		}
	}
	driverOptions, err := buildUniversalOptions(current, configured.tlsConfig)
	if err != nil {
		return err
	}
	module.config = current
	module.cluster.Store(current.Mode == ModeCluster)
	module.options = driverOptions
	module.hooks = append([]redis.Hook(nil), configured.hooks...)
	module.factory = configured.factory
	module.state = stateConfigured
	return nil
}

// OnInit 验证 Module 已经通过 New 或 Setup 完成配置。
func (module *Module) OnInit() error {
	if module == nil {
		return ErrInvalidArgument
	}
	module.mu.Lock()
	defer module.mu.Unlock()
	if module.state != stateConfigured {
		return ErrNotSetup
	}
	return nil
}

// OnStart 创建唯一 Client、安装 Hook 并检查当前逻辑部署。
//
// Standalone/Sentinel Ping 当前数据节点；Cluster 并发 Ping 当前全部 Primary。任一步失败都会
// 关闭已创建 Client，且不会发布半初始化 Handle。
func (module *Module) OnStart(ctx context.Context) error {
	if module == nil || ctx == nil {
		return ErrInvalidArgument
	}
	module.mu.Lock()
	if module.state != stateConfigured {
		module.mu.Unlock()
		return ErrNotSetup
	}
	module.state = stateStarting
	startCtx, startCancel := context.WithCancel(ctx)
	done := make(chan struct{})
	module.startCancel = startCancel
	module.transitionDone = done
	module.transitionErr = nil
	factory, options, mode := module.factory, module.options, module.config.Mode
	hooks := append([]redis.Hook(nil), module.hooks...)
	module.mu.Unlock()
	defer startCancel()
	runtime, err := factory(options, mode, hooks)
	if err != nil {
		module.failedStart(done)
		return err
	}
	if runtime == nil {
		module.failedStart(done)
		return errors.New("redismodule: runtime factory returned nil")
	}
	if err = runtime.ping(startCtx); err != nil {
		closeErr := runtime.close()
		module.failedStart(done)
		return errors.Join(err, closeErr)
	}

	module.mu.Lock()
	if module.state != stateStarting || module.transitionDone != done {
		module.mu.Unlock()
		closeErr := runtime.close()
		module.failedStart(done)
		startErr := startCtx.Err()
		if startErr == nil {
			startErr = context.Canceled
		}
		return errors.Join(startErr, closeErr)
	}
	module.runtime.Store(&runtimeHolder{runtime: runtime, lockClient: redislock.New(runtime.client())})
	module.state = stateRunning
	module.startCancel = nil
	module.transitionDone = nil
	module.transitionErr = nil
	close(done)
	module.mu.Unlock()
	return nil
}

func (module *Module) failedStart(done chan struct{}) {
	module.runtime.Store(nil)
	module.mu.Lock()
	if module.transitionDone == done {
		module.state = stateStopped
		module.startCancel = nil
		module.transitionDone = nil
		module.transitionErr = nil
		close(done)
	}
	module.mu.Unlock()
}

// OnStop 先撤销运行 Handle，再启动一次 Client 关闭并等待该转换；重复与并发停止安全。
//
// 所有并发调用者观察同一个关闭错误。ctx 先结束时当前调用返回 ctx.Err，关闭仍在内部继续，且不会重新发布 Client。
func (module *Module) OnStop(ctx context.Context) error {
	if module == nil || ctx == nil {
		return ErrInvalidArgument
	}
	module.mu.Lock()
	if module.state == stateUnconfigured || module.state == stateConfigured || module.state == stateStopped {
		module.mu.Unlock()
		return nil
	}
	if module.state == stateStarting {
		module.state = stateStopping
		if module.startCancel != nil {
			module.startCancel()
		}
		done := module.transitionDone
		module.mu.Unlock()
		return module.waitTransition(ctx, done)
	}
	if module.state == stateStopping {
		done := module.transitionDone
		module.mu.Unlock()
		return module.waitTransition(ctx, done)
	}
	if module.state != stateRunning {
		module.mu.Unlock()
		return ErrNotRunning
	}
	module.state = stateStopping
	done := make(chan struct{})
	module.transitionDone = done
	module.transitionErr = nil
	holder := module.runtime.Swap(nil)
	module.mu.Unlock()
	go module.finishStop(holder, done)
	return module.waitTransition(ctx, done)
}

func (module *Module) finishStop(holder *runtimeHolder, done chan struct{}) {
	var err error
	if holder != nil {
		err = holder.runtime.close()
	}
	module.mu.Lock()
	if module.transitionDone == done {
		module.state = stateStopped
		module.transitionErr = err
		close(done)
	}
	module.mu.Unlock()
}

func (module *Module) waitTransition(ctx context.Context, done <-chan struct{}) error {
	if done == nil {
		return ErrNotRunning
	}
	select {
	case <-done:
		module.mu.Lock()
		err := module.transitionErr
		module.mu.Unlock()
		return err
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Client 返回当前运行中的官方 UniversalClient，其他状态返回 nil。
//
// 返回值只借用，调用方不得 Close，也不得保存到 Module 生命周期之外。该方法不执行网络 I/O。
func (module *Module) Client() redis.UniversalClient {
	if module == nil {
		return nil
	}
	holder := module.runtime.Load()
	if holder == nil {
		return nil
	}
	return holder.runtime.client()
}

func (module *Module) requireClient(ctx context.Context) (redis.UniversalClient, error) {
	if ctx == nil {
		return nil, ErrInvalidArgument
	}
	client := module.Client()
	if client == nil {
		return nil, ErrNotRunning
	}
	return client, nil
}

func (module *Module) requireLockClient(ctx context.Context) (*redislock.Client, error) {
	if ctx == nil {
		return nil, ErrInvalidArgument
	}
	if module == nil {
		return nil, ErrNotRunning
	}
	holder := module.runtime.Load()
	if holder == nil || holder.lockClient == nil {
		return nil, ErrNotRunning
	}
	return holder.lockClient, nil
}

func (module *Module) isClusterMode() bool {
	return module != nil && module.cluster.Load()
}

var _ service.IModule = (*Module)(nil)
