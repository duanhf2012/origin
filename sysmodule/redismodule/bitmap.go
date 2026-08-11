package redismodule

import (
	"context"

	"github.com/redis/go-redis/v9"
)

// SetBit 把 key 中 offset 位设置为 value，返回修改前的位值。
//
// offset 单位是 Bit 且必须非负；value 使用 bool 避免 0/1 魔法值。
func (module *Module) SetBit(ctx context.Context, key string, offset int64, value bool) (bool, error) {
	if offset < 0 {
		return false, invalidArgument("redismodule Bit Offset 不能为负数")
	}
	client, err := module.requireClient(ctx)
	if err != nil {
		return false, err
	}
	bit := int(0)
	if value {
		bit = 1
	}
	previous, err := client.SetBit(ctx, key, offset, bit).Result()
	return previous == 1, err
}

// GetBit 返回 key 中 offset 位；offset 单位是 Bit 且必须非负。
//
// Key 不存在或超出当前字符串范围返回 false、nil。
func (module *Module) GetBit(ctx context.Context, key string, offset int64) (bool, error) {
	if offset < 0 {
		return false, invalidArgument("redismodule Bit Offset 不能为负数")
	}
	client, err := module.requireClient(ctx)
	if err != nil {
		return false, err
	}
	value, err := client.GetBit(ctx, key, offset).Result()
	return value == 1, err
}

// BitCount 返回包含 startByte 和 endByte 的字节范围内置位数量。
//
// 范围单位固定为 Byte，可使用 Redis 负索引语义；调用方应限制范围以控制服务端扫描成本。
func (module *Module) BitCount(ctx context.Context, key string, startByte, endByte int64) (int64, error) {
	client, err := module.requireClient(ctx)
	if err != nil {
		return 0, err
	}
	return client.BitCount(ctx, key, &redis.BitCount{Start: startByte, End: endByte, Unit: "BYTE"}).Result()
}

// BitOpAnd 对 keys 执行按位 AND 写入 destination，返回目标字符串字节长度；keys 不能为空。
//
// Cluster 下 destination 与全部源 Key 必须位于同一 Slot。
func (module *Module) BitOpAnd(ctx context.Context, destination string, keys ...string) (int64, error) {
	return module.bitOp(ctx, "AND", destination, keys...)
}

// BitOpOr 对 keys 执行按位 OR 写入 destination，返回目标字符串字节长度；keys 不能为空。
//
// Cluster 下 destination 与全部源 Key 必须位于同一 Slot。
func (module *Module) BitOpOr(ctx context.Context, destination string, keys ...string) (int64, error) {
	return module.bitOp(ctx, "OR", destination, keys...)
}

// BitOpXor 对 keys 执行按位 XOR 写入 destination，返回目标字符串字节长度；keys 不能为空。
//
// Cluster 下 destination 与全部源 Key 必须位于同一 Slot。
func (module *Module) BitOpXor(ctx context.Context, destination string, keys ...string) (int64, error) {
	return module.bitOp(ctx, "XOR", destination, keys...)
}

// BitOpNot 对 source 执行按位 NOT 写入 destination，返回目标字符串字节长度。
//
// Cluster 下 source 与 destination 必须位于同一 Slot。
func (module *Module) BitOpNot(ctx context.Context, destination, source string) (int64, error) {
	if err := module.validateSameSlot([]string{destination, source}); err != nil {
		return 0, err
	}
	client, err := module.requireClient(ctx)
	if err != nil {
		return 0, err
	}
	return client.BitOpNot(ctx, destination, source).Result()
}

func (module *Module) bitOp(ctx context.Context, operation, destination string, keys ...string) (int64, error) {
	if err := requireValues(len(keys), "BitOp Source Keys"); err != nil {
		return 0, err
	}
	allKeys := make([]string, 0, len(keys)+1)
	allKeys = append(allKeys, destination)
	allKeys = append(allKeys, keys...)
	if err := module.validateSameSlot(allKeys); err != nil {
		return 0, err
	}
	client, err := module.requireClient(ctx)
	if err != nil {
		return 0, err
	}
	switch operation {
	case "AND":
		return client.BitOpAnd(ctx, destination, keys...).Result()
	case "OR":
		return client.BitOpOr(ctx, destination, keys...).Result()
	default:
		return client.BitOpXor(ctx, destination, keys...).Result()
	}
}
