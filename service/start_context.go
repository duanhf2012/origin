package service

import (
	"context"
	"sync"
	"sync/atomic"

	"github.com/duanhf2012/origin/v3/errs"
)

// lifecycleContextKey 是业务无法从 service 包外构造的生命周期执行权令牌键。
type lifecycleContextKey struct{}

// lifecyclePhase 区分启动和停止 finalizer 的顺序 Await 准入。
type lifecyclePhase uint8

const (
	lifecyclePhaseStart lifecyclePhase = iota
	lifecyclePhaseFinalizer
)

// lifecycleContext 把父 Context 与当前 Scheduler 的一次 OnStart 或 OnStop 代次绑定。
//
// Go 无法可靠校验 goroutine 身份，因此令牌只验证 Service、阶段、代次和有效期。业务仍
// 不得把有效生命周期 Context 交给其他 goroutine 调用执行权 API。
type lifecycleContext struct {
	context.Context
	scheduler  *serviceScheduler
	generation uint64
	phase      lifecyclePhase
	active     atomic.Bool
}

// Value 优先返回框架私有令牌，再委托父 Context 查询业务值。
func (lifecycle *lifecycleContext) Value(key any) any {
	if _, matched := key.(lifecycleContextKey); matched {
		return lifecycle
	}
	return lifecycle.Context.Value(key)
}

// PrepareStartContext 为 Node 即将调用的 OnStart 创建一次性私有执行权 Context。
//
// 返回的 finish 必须在 OnStart 返回后调用，并且可以安全重复调用。
func PrepareStartContext(
	target IService,
	parent context.Context,
) (context.Context, func(), error) {
	return prepareLifecycleContext(
		target,
		parent,
		lifecyclePhaseStart,
		schedulerPrepared,
		StateStarting,
	)
}

// prepareFinalizerContext 为最后一个 Runner 即将执行的 OnStop 建立私有令牌。
func prepareFinalizerContext(
	target IService,
	parent context.Context,
) (context.Context, func(), error) {
	return prepareLifecycleContext(
		target,
		parent,
		lifecyclePhaseFinalizer,
		schedulerFinalizing,
		StateStopping,
	)
}

// prepareLifecycleContext 统一启动与 finalizer 的令牌建立和失效规则。
func prepareLifecycleContext(
	target IService,
	parent context.Context,
	phase lifecyclePhase,
	expectedScheduler schedulerState,
	expectedService State,
) (context.Context, func(), error) {
	// 只有已经绑定 Runtime、处于准确阶段并完成 Scheduler 装配的真实 Service 合法。
	if target == nil || isNilService(target) || parent == nil {
		return nil, nil, errs.ErrInvalidArgument
	}
	base := target.baseService()
	if base == nil || base.runtime == nil ||
		base.runtime.State() != expectedService {
		return nil, nil, errs.ErrInvalidArgument
	}
	scheduler := base.scheduler.Load()
	if scheduler == nil {
		return nil, nil, errs.ErrServiceNotReady
	}

	// 同一 Service 同时只允许一个活动生命周期令牌，代次防止旧 Context 复用。
	scheduler.mu.Lock()
	if scheduler.state != expectedScheduler || scheduler.activeLifecycle != nil {
		scheduler.mu.Unlock()
		return nil, nil, errs.ErrInvalidArgument
	}
	scheduler.lifecycleGeneration++
	generation := scheduler.lifecycleGeneration
	derived, cancel := context.WithCancelCause(parent)
	token := &lifecycleContext{
		Context:    derived,
		scheduler:  scheduler,
		generation: generation,
		phase:      phase,
	}
	token.active.Store(true)
	scheduler.activeLifecycle = token
	scheduler.mu.Unlock()

	// finish 先使令牌失效，再清理 Scheduler 冷路径状态和 Context 子树。
	var once sync.Once
	finish := func() {
		once.Do(func() {
			token.active.Store(false)
			scheduler.mu.Lock()
			if scheduler.activeLifecycle == token {
				scheduler.activeLifecycle = nil
			}
			scheduler.mu.Unlock()
			cancel(nil)
		})
	}
	return token, finish, nil
}
