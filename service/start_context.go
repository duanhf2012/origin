package service

import (
	"context"
	"sync"
	"sync/atomic"

	"github.com/duanhf2012/origin/v3/errs"
)

// startContextKey 是业务无法从 service 包外构造的 OnStart 执行权令牌键。
type startContextKey struct{}

// startContext 把父 Context 与当前 ServiceScheduler 的一次 OnStart 代次绑定。
//
// Go 无法可靠校验 goroutine 身份，因此令牌只验证 Service、代次和有效期。业务仍不得把
// 有效的 OnStart Context 交给其他 goroutine 调用执行权 API。
type startContext struct {
	context.Context
	scheduler  *serviceScheduler
	generation uint64
	active     atomic.Bool
}

// Value 优先返回框架私有令牌，再委托父 Context 查询业务值。
func (start *startContext) Value(key any) any {
	if _, matched := key.(startContextKey); matched {
		return start
	}
	return start.Context.Value(key)
}

// PrepareStartContext 为 Node 即将调用的 OnStart 创建一次性私有执行权 Context。
//
// 该函数是 node/service 跨包装配入口，不是业务主动创建执行权的 API。返回的 finish 必须
// 在 OnStart 返回后调用，并且可以安全重复调用。
func PrepareStartContext(
	target IService,
	parent context.Context,
) (context.Context, func(), error) {
	// 只有已经绑定 Runtime、进入 Starting 且完成 Scheduler Prepare 的真实 Service 合法。
	if target == nil || isNilService(target) || parent == nil {
		return nil, nil, errs.ErrInvalidArgument
	}
	base := target.baseService()
	if base == nil || base.runtime == nil ||
		base.runtime.State() != StateStarting {
		return nil, nil, errs.ErrInvalidArgument
	}
	scheduler := base.scheduler.Load()
	if scheduler == nil {
		return nil, nil, errs.ErrServiceNotReady
	}

	// 同一 Service 生命周期只允许一个活动 OnStart 令牌，代次防止旧 Context 复用。
	scheduler.mu.Lock()
	if scheduler.state != schedulerPrepared || scheduler.startActive {
		scheduler.mu.Unlock()
		return nil, nil, errs.ErrInvalidArgument
	}
	scheduler.startGeneration++
	generation := scheduler.startGeneration
	derived, cancel := context.WithCancelCause(parent)
	token := &startContext{
		Context:    derived,
		scheduler:  scheduler,
		generation: generation,
	}
	token.active.Store(true)
	scheduler.startActive = true
	scheduler.mu.Unlock()

	// finish 先原子使令牌失效，再清理 Scheduler 冷路径状态和 Context 子树。
	var once sync.Once
	finish := func() {
		once.Do(func() {
			token.active.Store(false)
			scheduler.mu.Lock()
			if scheduler.startGeneration == generation {
				scheduler.startActive = false
			}
			scheduler.mu.Unlock()
			cancel(nil)
		})
	}
	return token, finish, nil
}
