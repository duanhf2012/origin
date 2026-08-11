package redismodule

import (
	"context"
	"errors"
	"time"

	"github.com/bsm/redislock"
)

const (
	lockRetryBase      = 50 * time.Millisecond
	lockReleaseTimeout = 2 * time.Second
)

// Lock 表示由 Redis Token 与 TTL 保护的 Lease Lock。
//
// Lock 不暴露 Token 和第三方对象。Lease 可能在业务完成前过期，因此不能作为金币、道具、奖励、
// 支付或跨系统事务的唯一正确性依据。
type Lock struct {
	key  string
	lock *redislock.Lock
}

// TryLock 立即尝试一次获得 key 对应的 Lease。
//
// key 必须非空、ttl 必须大于 0。锁被占用返回 nil、false、nil；网络、Context 或参数错误正常返回。
func (module *Module) TryLock(ctx context.Context, key string, ttl time.Duration) (*Lock, bool, error) {
	if key == "" {
		return nil, false, invalidArgument("redismodule Lock Key 不能为空")
	}
	if ttl <= 0 {
		return nil, false, invalidArgument("redismodule Lock TTL 必须大于 0")
	}
	client, err := module.requireLockClient(ctx)
	if err != nil {
		return nil, false, err
	}
	lease, err := client.Obtain(ctx, key, ttl, nil)
	if errors.Is(err, redislock.ErrNotObtained) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	return &Lock{key: key, lock: lease}, true, nil
}

// Lock 在 waitTimeout 与 ctx 的共同边界内等待获得 key 对应的 Lease。
//
// key 必须非空，ttl 和 waitTimeout 必须大于 0。首次立即尝试，之后约每 40ms～60ms 重试；
// ctx 先结束返回 ctx.Err，等待时间先耗尽返回 ErrLockNotObtained。
func (module *Module) Lock(ctx context.Context, key string, ttl, waitTimeout time.Duration) (*Lock, error) {
	if key == "" {
		return nil, invalidArgument("redismodule Lock Key 不能为空")
	}
	if ttl <= 0 || waitTimeout <= 0 {
		return nil, invalidArgument("redismodule Lock TTL 和 WaitTimeout 必须大于 0")
	}
	client, err := module.requireLockClient(ctx)
	if err != nil {
		return nil, err
	}
	waitCtx, cancel := context.WithTimeout(ctx, waitTimeout)
	defer cancel()
	strategy := &jitterRetry{state: uint64(time.Now().UnixNano()) ^ uint64(len(key))}
	lease, err := client.Obtain(waitCtx, key, ttl, &redislock.Options{RetryStrategy: strategy})
	if err == nil {
		return &Lock{key: key, lock: lease}, nil
	}
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if waitCtx.Err() != nil || errors.Is(err, redislock.ErrNotObtained) {
		return nil, ErrLockNotObtained
	}
	return nil, err
}

// WithLock 获得 Lease、在当前 goroutine 同步执行 fn，并在结束时尝试释放。
//
// fn 不能为空。释放使用继承 Context Value、但独立于业务取消且最多 2s 的清理 Context；回调和释放
// 同时失败时返回 errors.Join。发生 panic 时仍尝试释放，然后继续传播 panic。
func (module *Module) WithLock(ctx context.Context, key string, ttl, waitTimeout time.Duration, fn func(context.Context) error) (err error) {
	if fn == nil {
		return invalidArgument("redismodule WithLock Callback 不能为空")
	}
	lease, err := module.Lock(ctx, key, ttl, waitTimeout)
	if err != nil {
		return err
	}
	defer func() {
		cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), lockReleaseTimeout)
		defer cancel()
		err = errors.Join(err, lease.Release(cleanupCtx))
	}()
	return fn(ctx)
}

// Key 返回该 Lease 使用的 Redis Key，不执行网络 I/O。
func (lock *Lock) Key() string {
	if lock == nil {
		return ""
	}
	return lock.key
}

// TTL 查询服务端确认的剩余 Lease 时间。
//
// ctx 不能为空；Lease 已过期时返回 0、nil。调用结果只代表查询瞬间，不能证明后续仍持有锁。
func (lock *Lock) TTL(ctx context.Context) (time.Duration, error) {
	if ctx == nil {
		return 0, ErrInvalidArgument
	}
	if lock == nil || lock.lock == nil {
		return 0, ErrLockNotHeld
	}
	return lock.lock.TTL(ctx)
}

// Refresh 在 Token 仍匹配时把 Lease 更新为 ttl；ttl 必须大于 0。
//
// Lease 已过期或所有权改变返回 ErrLockNotHeld。Refresh 失败后业务必须停止受保护操作或进入补偿流程。
func (lock *Lock) Refresh(ctx context.Context, ttl time.Duration) error {
	if ctx == nil {
		return ErrInvalidArgument
	}
	if ttl <= 0 {
		return invalidArgument("redismodule Lock TTL 必须大于 0")
	}
	if lock == nil || lock.lock == nil {
		return ErrLockNotHeld
	}
	return lock.lock.Refresh(ctx, ttl, nil)
}

// Release 仅在 Token 仍匹配时释放 Lease。
//
// ctx 不能为空；重复释放、Lease 过期或所有权改变返回 ErrLockNotHeld。
func (lock *Lock) Release(ctx context.Context) error {
	if ctx == nil {
		return ErrInvalidArgument
	}
	if lock == nil || lock.lock == nil {
		return ErrLockNotHeld
	}
	return lock.lock.Release(ctx)
}

type jitterRetry struct{ state uint64 }

// NextBackoff 返回 40ms 到 60ms 的包内抖动退避，仅供 redislock 等待策略调用。
func (strategy *jitterRetry) NextBackoff() time.Duration {
	// 每个 Lock 调用独占状态，避免包级随机数和锁；xorshift 只用于退避抖动，不用于安全 Token。
	if strategy.state == 0 {
		strategy.state = 0x9e3779b97f4a7c15
	}
	strategy.state ^= strategy.state << 13
	strategy.state ^= strategy.state >> 7
	strategy.state ^= strategy.state << 17
	jitterMillis := int64(strategy.state%21) - 10
	return lockRetryBase + time.Duration(jitterMillis)*time.Millisecond
}
