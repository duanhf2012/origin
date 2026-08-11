package redismodule

import (
	"context"
	"errors"
	"time"

	"github.com/redis/go-redis/v9"
)

// Set 保存 value；expiration 为 0 表示持久 Key，大于 0 表示 TTL，负数无效。
//
// value 使用 go-redis 支持的基础类型或 []byte；本方法不自动进行 JSON/PB 编码。
func (module *Module) Set(ctx context.Context, key string, value any, expiration time.Duration) error {
	if expiration < 0 {
		return invalidArgument("redismodule Expiration 不能为负数")
	}
	client, err := module.requireClient(ctx)
	if err != nil {
		return err
	}
	return client.Set(ctx, key, value, expiration).Err()
}

// SetNX 仅在 key 不存在时保存 value，返回是否写入成功。
//
// expiration 为 0 表示持久 Key，大于 0 表示 TTL，负数无效。超时不证明写入一定未执行。
func (module *Module) SetNX(ctx context.Context, key string, value any, expiration time.Duration) (bool, error) {
	if expiration < 0 {
		return false, invalidArgument("redismodule Expiration 不能为负数")
	}
	client, err := module.requireClient(ctx)
	if err != nil {
		return false, err
	}
	return client.SetNX(ctx, key, value, expiration).Result()
}

// SetXX 仅在 key 已存在时保存 value，返回是否写入成功。
//
// expiration 为 0 表示持久 Key，大于 0 表示 TTL，负数无效。
func (module *Module) SetXX(ctx context.Context, key string, value any, expiration time.Duration) (bool, error) {
	if expiration < 0 {
		return false, invalidArgument("redismodule Expiration 不能为负数")
	}
	client, err := module.requireClient(ctx)
	if err != nil {
		return false, err
	}
	_, err = client.SetArgs(ctx, key, value, redis.SetArgs{Mode: "XX", TTL: expiration}).Result()
	if errors.Is(err, redis.Nil) {
		return false, nil
	}
	return err == nil, err
}

// SetKeepTTL 修改 key 的值并保留已有 TTL；不存在 Key 会创建持久 Key。
func (module *Module) SetKeepTTL(ctx context.Context, key string, value any) error {
	client, err := module.requireClient(ctx)
	if err != nil {
		return err
	}
	return client.SetArgs(ctx, key, value, redis.SetArgs{KeepTTL: true}).Err()
}

// Get 读取字符串；Key 不存在返回 ErrNil，真实空字符串返回 ""、nil。
func (module *Module) Get(ctx context.Context, key string) (string, error) {
	client, err := module.requireClient(ctx)
	if err != nil {
		return "", err
	}
	return client.Get(ctx, key).Result()
}

// GetBytes 读取独立字节切片；Key 不存在返回 ErrNil。
//
// 返回切片由调用方拥有。大 Value 会产生相应分配，局部极端热路径可评估官方 Client 高级能力。
func (module *Module) GetBytes(ctx context.Context, key string) ([]byte, error) {
	client, err := module.requireClient(ctx)
	if err != nil {
		return nil, err
	}
	return client.Get(ctx, key).Bytes()
}

// GetDel 原子读取并删除 key；不存在返回 ErrNil，适合一次性 Token。
func (module *Module) GetDel(ctx context.Context, key string) (string, error) {
	client, err := module.requireClient(ctx)
	if err != nil {
		return "", err
	}
	return client.GetDel(ctx, key).Result()
}

// GetEx 原子读取并把 TTL 更新为 expiration；expiration 必须大于 0。
//
// Key 不存在返回 ErrNil，适合滑动会话，但每次读取都会产生一次写操作。
func (module *Module) GetEx(ctx context.Context, key string, expiration time.Duration) (string, error) {
	if expiration <= 0 {
		return "", invalidArgument("redismodule GetEx Expiration 必须大于 0")
	}
	client, err := module.requireClient(ctx)
	if err != nil {
		return "", err
	}
	return client.GetEx(ctx, key, expiration).Result()
}

// MGet 按 keys 输入顺序返回可选字符串；keys 不能为空。
//
// 不存在项 Exists=false，真实空字符串 Exists=true。Cluster 下全部 Key 必须位于同一 Slot。
func (module *Module) MGet(ctx context.Context, keys ...string) ([]OptionalString, error) {
	if err := requireValues(len(keys), "Keys"); err != nil {
		return nil, err
	}
	if err := module.validateSameSlot(keys); err != nil {
		return nil, err
	}
	client, err := module.requireClient(ctx)
	if err != nil {
		return nil, err
	}
	values, err := client.MGet(ctx, keys...).Result()
	if err != nil {
		return nil, err
	}
	return optionalStrings(values)
}

// MSet 同步保存 values 中全部 Key/Value；空 Map 无效。
//
// Cluster 下全部 Key 必须位于同一 Slot。该命令是单条 Redis 原子命令，但超时后写入状态仍可能不确定。
func (module *Module) MSet(ctx context.Context, values map[string]any) error {
	if err := requireValues(len(values), "Values"); err != nil {
		return err
	}
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	if err := module.validateSameSlot(keys); err != nil {
		return err
	}
	client, err := module.requireClient(ctx)
	if err != nil {
		return err
	}
	return client.MSet(ctx, values).Err()
}

// MSetNX 仅在 values 中全部 Key 都不存在时保存，返回是否整体写入；空 Map 无效。
//
// Cluster 下全部 Key 必须位于同一 Slot。
func (module *Module) MSetNX(ctx context.Context, values map[string]any) (bool, error) {
	if err := requireValues(len(values), "Values"); err != nil {
		return false, err
	}
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	if err := module.validateSameSlot(keys); err != nil {
		return false, err
	}
	client, err := module.requireClient(ctx)
	if err != nil {
		return false, err
	}
	return client.MSetNX(ctx, values).Result()
}

// Incr 把 key 中的整数原子加一并返回新值；不存在按 0 处理。
func (module *Module) Incr(ctx context.Context, key string) (int64, error) {
	client, err := module.requireClient(ctx)
	if err != nil {
		return 0, err
	}
	return client.Incr(ctx, key).Result()
}

// IncrBy 把 key 中的整数原子增加 increment 并返回新值；increment 可为负数。
func (module *Module) IncrBy(ctx context.Context, key string, increment int64) (int64, error) {
	client, err := module.requireClient(ctx)
	if err != nil {
		return 0, err
	}
	return client.IncrBy(ctx, key, increment).Result()
}

// Decr 把 key 中的整数原子减一并返回新值；不存在按 0 处理。
func (module *Module) Decr(ctx context.Context, key string) (int64, error) {
	client, err := module.requireClient(ctx)
	if err != nil {
		return 0, err
	}
	return client.Decr(ctx, key).Result()
}

// DecrBy 把 key 中的整数原子减少 decrement 并返回新值；decrement 可为负数。
func (module *Module) DecrBy(ctx context.Context, key string, decrement int64) (int64, error) {
	client, err := module.requireClient(ctx)
	if err != nil {
		return 0, err
	}
	return client.DecrBy(ctx, key, decrement).Result()
}

// Append 把 value 追加到字符串 key 并返回追加后的字节长度；不存在 Key 会创建。
func (module *Module) Append(ctx context.Context, key, value string) (int64, error) {
	client, err := module.requireClient(ctx)
	if err != nil {
		return 0, err
	}
	return client.Append(ctx, key, value).Result()
}
