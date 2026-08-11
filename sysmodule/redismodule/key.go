package redismodule

import (
	"context"
	"time"
)

// Del 同步删除 keys，返回实际删除的 Key 数；keys 不能为空。
//
// Cluster 下全部 Key 必须位于同一 Slot。方法执行网络 I/O，Context 取消时返回底层错误链。
func (module *Module) Del(ctx context.Context, keys ...string) (int64, error) {
	if err := requireValues(len(keys), "Keys"); err != nil {
		return 0, err
	}
	if err := module.validateSameSlot(keys); err != nil {
		return 0, err
	}
	client, err := module.requireClient(ctx)
	if err != nil {
		return 0, err
	}
	return client.Del(ctx, keys...).Result()
}

// Unlink 异步释放 keys 对应的服务端内存，返回实际解除链接的 Key 数；keys 不能为空。
//
// Cluster 下全部 Key 必须位于同一 Slot。Redis 后台释放不等于调用方异步，命令仍同步等待响应。
func (module *Module) Unlink(ctx context.Context, keys ...string) (int64, error) {
	if err := requireValues(len(keys), "Keys"); err != nil {
		return 0, err
	}
	if err := module.validateSameSlot(keys); err != nil {
		return 0, err
	}
	client, err := module.requireClient(ctx)
	if err != nil {
		return 0, err
	}
	return client.Unlink(ctx, keys...).Result()
}

// Exists 返回 keys 中实际存在的 Key 数；keys 不能为空。
//
// Cluster 下全部 Key 必须位于同一 Slot。重复传入同一 Key 会按 Redis EXISTS 语义重复计数。
func (module *Module) Exists(ctx context.Context, keys ...string) (int64, error) {
	if err := requireValues(len(keys), "Keys"); err != nil {
		return 0, err
	}
	if err := module.validateSameSlot(keys); err != nil {
		return 0, err
	}
	client, err := module.requireClient(ctx)
	if err != nil {
		return 0, err
	}
	return client.Exists(ctx, keys...).Result()
}

// Type 返回 key 的 Redis 类型；不存在时返回 "none" 和 nil。
func (module *Module) Type(ctx context.Context, key string) (string, error) {
	client, err := module.requireClient(ctx)
	if err != nil {
		return "", err
	}
	return client.Type(ctx, key).Result()
}

// Expire 把 key 的过期时间设置为 expiration，返回是否实际应用。
//
// expiration 必须大于 0；不存在 Key 返回 false、nil。
func (module *Module) Expire(ctx context.Context, key string, expiration time.Duration) (bool, error) {
	if expiration <= 0 {
		return false, invalidArgument("redismodule Expiration 必须大于 0")
	}
	client, err := module.requireClient(ctx)
	if err != nil {
		return false, err
	}
	return client.Expire(ctx, key, expiration).Result()
}

// ExpireAt 把 key 的过期时间设置为绝对时间 expiration，返回是否实际应用。
//
// expiration 不能为零值；过去时间会按 Redis 语义立即删除存在的 Key。
func (module *Module) ExpireAt(ctx context.Context, key string, expiration time.Time) (bool, error) {
	if expiration.IsZero() {
		return false, invalidArgument("redismodule ExpireAt 时间不能为空")
	}
	client, err := module.requireClient(ctx)
	if err != nil {
		return false, err
	}
	return client.ExpireAt(ctx, key, expiration).Result()
}

// Persist 移除 key 的过期时间，返回是否实际移除；不存在或本来持久的 Key 返回 false、nil。
func (module *Module) Persist(ctx context.Context, key string) (bool, error) {
	client, err := module.requireClient(ctx)
	if err != nil {
		return false, err
	}
	return client.Persist(ctx, key).Result()
}

// TTL 返回 key 的秒精度剩余时间。
//
// 持久 Key 返回 TTLNoExpiration，不存在返回 TTLKeyNotFound；方法不会把两者转换成错误。
func (module *Module) TTL(ctx context.Context, key string) (time.Duration, error) {
	client, err := module.requireClient(ctx)
	if err != nil {
		return 0, err
	}
	return client.TTL(ctx, key).Result()
}

// PTTL 返回 key 的毫秒精度剩余时间。
//
// 持久 Key 返回 PTTLNoExpiration，不存在返回 PTTLKeyNotFound；方法不会把两者转换成错误。
func (module *Module) PTTL(ctx context.Context, key string) (time.Duration, error) {
	client, err := module.requireClient(ctx)
	if err != nil {
		return 0, err
	}
	return client.PTTL(ctx, key).Result()
}

// Rename 原子重命名 key 为 newKey；源 Key 不存在时返回 Redis 错误。
//
// Cluster 下两个 Key 必须位于同一 Slot。
func (module *Module) Rename(ctx context.Context, key, newKey string) error {
	if err := module.validateSameSlot([]string{key, newKey}); err != nil {
		return err
	}
	client, err := module.requireClient(ctx)
	if err != nil {
		return err
	}
	return client.Rename(ctx, key, newKey).Err()
}

// Scan 从 cursor 开始按 pattern 增量扫描 Key，count 是大于 0 的服务端提示数。
//
// 调用方必须循环到 nextCursor 为 0；单轮数量和顺序不保证。Cluster 返回 ErrUnsupportedMode，避免
// 把单节点结果误认为全量结果。该方法仍可能在单轮产生较大结果，应使用合理 count。
func (module *Module) Scan(ctx context.Context, cursor uint64, pattern string, count int64) ([]string, uint64, error) {
	if count <= 0 {
		return nil, 0, invalidArgument("redismodule Scan Count 必须大于 0")
	}
	if module.isClusterMode() {
		return nil, 0, ErrUnsupportedMode
	}
	client, err := module.requireClient(ctx)
	if err != nil {
		return nil, 0, err
	}
	return client.Scan(ctx, cursor, pattern, count).Result()
}
