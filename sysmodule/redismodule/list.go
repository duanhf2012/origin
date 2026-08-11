package redismodule

import "context"

// LPush 从左端插入 values，返回操作后列表长度；values 不能为空。
func (module *Module) LPush(ctx context.Context, key string, values ...any) (int64, error) {
	if err := requireValues(len(values), "List Values"); err != nil {
		return 0, err
	}
	client, err := module.requireClient(ctx)
	if err != nil {
		return 0, err
	}
	return client.LPush(ctx, key, values...).Result()
}

// LPushX 仅在列表已存在时从左端插入 values，返回操作后长度；values 不能为空。
func (module *Module) LPushX(ctx context.Context, key string, values ...any) (int64, error) {
	if err := requireValues(len(values), "List Values"); err != nil {
		return 0, err
	}
	client, err := module.requireClient(ctx)
	if err != nil {
		return 0, err
	}
	return client.LPushX(ctx, key, values...).Result()
}

// RPush 从右端插入 values，返回操作后列表长度；values 不能为空。
func (module *Module) RPush(ctx context.Context, key string, values ...any) (int64, error) {
	if err := requireValues(len(values), "List Values"); err != nil {
		return 0, err
	}
	client, err := module.requireClient(ctx)
	if err != nil {
		return 0, err
	}
	return client.RPush(ctx, key, values...).Result()
}

// RPushX 仅在列表已存在时从右端插入 values，返回操作后长度；values 不能为空。
func (module *Module) RPushX(ctx context.Context, key string, values ...any) (int64, error) {
	if err := requireValues(len(values), "List Values"); err != nil {
		return 0, err
	}
	client, err := module.requireClient(ctx)
	if err != nil {
		return 0, err
	}
	return client.RPushX(ctx, key, values...).Result()
}

// LPop 从左端弹出一个字符串；列表不存在或为空返回 ErrNil。
func (module *Module) LPop(ctx context.Context, key string) (string, error) {
	client, err := module.requireClient(ctx)
	if err != nil {
		return "", err
	}
	return client.LPop(ctx, key).Result()
}

// LPopBytes 从左端弹出一个独立字节切片；列表不存在或为空返回 ErrNil。
func (module *Module) LPopBytes(ctx context.Context, key string) ([]byte, error) {
	client, err := module.requireClient(ctx)
	if err != nil {
		return nil, err
	}
	return client.LPop(ctx, key).Bytes()
}

// LPopN 从左端最多弹出 count 个元素；count 必须大于 0。
//
// 列表不存在或为空返回空切片、nil。该方法适合有界批量领取，但 List 不提供可靠队列确认语义。
func (module *Module) LPopN(ctx context.Context, key string, count int64) ([]string, error) {
	countValue, err := positiveCountAsInt(count, "LPopN")
	if err != nil {
		return nil, err
	}
	client, err := module.requireClient(ctx)
	if err != nil {
		return nil, err
	}
	return client.LPopCount(ctx, key, countValue).Result()
}

// RPop 从右端弹出一个字符串；列表不存在或为空返回 ErrNil。
func (module *Module) RPop(ctx context.Context, key string) (string, error) {
	client, err := module.requireClient(ctx)
	if err != nil {
		return "", err
	}
	return client.RPop(ctx, key).Result()
}

// RPopBytes 从右端弹出一个独立字节切片；列表不存在或为空返回 ErrNil。
func (module *Module) RPopBytes(ctx context.Context, key string) ([]byte, error) {
	client, err := module.requireClient(ctx)
	if err != nil {
		return nil, err
	}
	return client.RPop(ctx, key).Bytes()
}

// RPopN 从右端最多弹出 count 个元素；count 必须大于 0。
//
// 列表不存在或为空返回空切片、nil。调用方必须限制 count，避免大响应。
func (module *Module) RPopN(ctx context.Context, key string, count int64) ([]string, error) {
	countValue, err := positiveCountAsInt(count, "RPopN")
	if err != nil {
		return nil, err
	}
	client, err := module.requireClient(ctx)
	if err != nil {
		return nil, err
	}
	return client.RPopCount(ctx, key, countValue).Result()
}

// LIndex 返回 index 位置的元素；index 可为负数，越界或 Key 不存在返回 ErrNil。
func (module *Module) LIndex(ctx context.Context, key string, index int64) (string, error) {
	client, err := module.requireClient(ctx)
	if err != nil {
		return "", err
	}
	return client.LIndex(ctx, key, index).Result()
}

// LSet 设置 index 位置的元素；index 可为负数，Key 不存在或越界返回 Redis 错误。
func (module *Module) LSet(ctx context.Context, key string, index int64, value any) error {
	client, err := module.requireClient(ctx)
	if err != nil {
		return err
	}
	return client.LSet(ctx, key, index, value).Err()
}

// LRange 返回包含 start 和 stop 的索引区间；负索引按 Redis 语义从尾部计算。
//
// Key 不存在返回空切片、nil。调用方必须设置有界范围，禁止在线热路径使用 0、-1 读取未知大列表。
func (module *Module) LRange(ctx context.Context, key string, start, stop int64) ([]string, error) {
	client, err := module.requireClient(ctx)
	if err != nil {
		return nil, err
	}
	return client.LRange(ctx, key, start, stop).Result()
}

// LLen 返回列表长度；Key 不存在返回 0、nil。
func (module *Module) LLen(ctx context.Context, key string) (int64, error) {
	client, err := module.requireClient(ctx)
	if err != nil {
		return 0, err
	}
	return client.LLen(ctx, key).Result()
}

// LTrim 只保留包含 start 和 stop 的索引区间；负索引按 Redis 语义计算。
func (module *Module) LTrim(ctx context.Context, key string, start, stop int64) error {
	client, err := module.requireClient(ctx)
	if err != nil {
		return err
	}
	return client.LTrim(ctx, key, start, stop).Err()
}

// LRem 删除与 value 相等的元素并返回删除数。
//
// count>0 从左侧最多删除 count 个，count<0 从右侧最多删除绝对值个，count=0 删除全部匹配项。
func (module *Module) LRem(ctx context.Context, key string, count int64, value any) (int64, error) {
	client, err := module.requireClient(ctx)
	if err != nil {
		return 0, err
	}
	return client.LRem(ctx, key, count, value).Result()
}

// LMove 原子地把 source 一端的一个元素移动到 destination 指定端并返回该元素。
//
// source 为空返回 ErrNil；from/to 只能是 ListLeft 或 ListRight。Cluster 下两个 Key 必须同 Slot。
func (module *Module) LMove(ctx context.Context, source, destination string, from, to ListSide) (string, error) {
	fromText, ok := listSideText(from)
	if !ok {
		return "", invalidArgument("redismodule LMove From 无效")
	}
	toText, ok := listSideText(to)
	if !ok {
		return "", invalidArgument("redismodule LMove To 无效")
	}
	if err := module.validateSameSlot([]string{source, destination}); err != nil {
		return "", err
	}
	client, err := module.requireClient(ctx)
	if err != nil {
		return "", err
	}
	return client.LMove(ctx, source, destination, fromText, toText).Result()
}

func listSideText(side ListSide) (string, bool) {
	switch side {
	case ListLeft:
		return "LEFT", true
	case ListRight:
		return "RIGHT", true
	default:
		return "", false
	}
}
