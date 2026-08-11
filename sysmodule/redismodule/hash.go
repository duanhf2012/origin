package redismodule

import "context"

// HSet 保存一个 Field，返回该 Field 是否为新增。
//
// value 使用 go-redis 支持的基础类型或 []byte；本方法不进行结构体映射或序列化。
func (module *Module) HSet(ctx context.Context, key, field string, value any) (bool, error) {
	client, err := module.requireClient(ctx)
	if err != nil {
		return false, err
	}
	added, err := client.HSet(ctx, key, field, value).Result()
	return added > 0, err
}

// HSetMany 保存 values 中多个 Field，返回实际新增的 Field 数；空 Map 无效。
func (module *Module) HSetMany(ctx context.Context, key string, values map[string]any) (int64, error) {
	if err := requireValues(len(values), "Hash Values"); err != nil {
		return 0, err
	}
	client, err := module.requireClient(ctx)
	if err != nil {
		return 0, err
	}
	return client.HSet(ctx, key, values).Result()
}

// HSetNX 仅在 field 不存在时保存 value，返回是否写入成功。
func (module *Module) HSetNX(ctx context.Context, key, field string, value any) (bool, error) {
	client, err := module.requireClient(ctx)
	if err != nil {
		return false, err
	}
	return client.HSetNX(ctx, key, field, value).Result()
}

// HGet 读取 field 字符串；Key 或 Field 不存在返回 ErrNil，真实空字符串返回 ""、nil。
func (module *Module) HGet(ctx context.Context, key, field string) (string, error) {
	client, err := module.requireClient(ctx)
	if err != nil {
		return "", err
	}
	return client.HGet(ctx, key, field).Result()
}

// HGetBytes 读取 field 的独立字节切片；Key 或 Field 不存在返回 ErrNil。
func (module *Module) HGetBytes(ctx context.Context, key, field string) ([]byte, error) {
	client, err := module.requireClient(ctx)
	if err != nil {
		return nil, err
	}
	return client.HGet(ctx, key, field).Bytes()
}

// HMGet 按 fields 输入顺序返回可选字符串；fields 不能为空。
//
// 不存在项 Exists=false，真实空字符串 Exists=true。结果长度与输入长度相同。
func (module *Module) HMGet(ctx context.Context, key string, fields ...string) ([]OptionalString, error) {
	if err := requireValues(len(fields), "Hash Fields"); err != nil {
		return nil, err
	}
	client, err := module.requireClient(ctx)
	if err != nil {
		return nil, err
	}
	values, err := client.HMGet(ctx, key, fields...).Result()
	if err != nil {
		return nil, err
	}
	return optionalStrings(values)
}

// HGetAll 一次返回 key 的全部 Field；不存在返回空 Map、nil。
//
// 大 Hash 可能造成大响应、分配和 Service 延迟尖峰，线上规模不明确时使用 HScan。
func (module *Module) HGetAll(ctx context.Context, key string) (map[string]string, error) {
	client, err := module.requireClient(ctx)
	if err != nil {
		return nil, err
	}
	return client.HGetAll(ctx, key).Result()
}

// HExists 报告 field 是否存在；Key 不存在返回 false、nil。
func (module *Module) HExists(ctx context.Context, key, field string) (bool, error) {
	client, err := module.requireClient(ctx)
	if err != nil {
		return false, err
	}
	return client.HExists(ctx, key, field).Result()
}

// HDel 删除 fields，返回实际删除的 Field 数；fields 不能为空。
func (module *Module) HDel(ctx context.Context, key string, fields ...string) (int64, error) {
	if err := requireValues(len(fields), "Hash Fields"); err != nil {
		return 0, err
	}
	client, err := module.requireClient(ctx)
	if err != nil {
		return 0, err
	}
	return client.HDel(ctx, key, fields...).Result()
}

// HLen 返回 Field 数；Key 不存在返回 0、nil。
func (module *Module) HLen(ctx context.Context, key string) (int64, error) {
	client, err := module.requireClient(ctx)
	if err != nil {
		return 0, err
	}
	return client.HLen(ctx, key).Result()
}

// HKeys 一次返回全部 Field 名；Key 不存在返回空切片、nil。
//
// 大 Hash 应使用 HScan，避免一次读取无界结果。
func (module *Module) HKeys(ctx context.Context, key string) ([]string, error) {
	client, err := module.requireClient(ctx)
	if err != nil {
		return nil, err
	}
	return client.HKeys(ctx, key).Result()
}

// HVals 一次返回全部 Field 值；Key 不存在返回空切片、nil。
//
// 返回值不携带 Field 名且可能很大，大 Hash 应使用 HScan。
func (module *Module) HVals(ctx context.Context, key string) ([]string, error) {
	client, err := module.requireClient(ctx)
	if err != nil {
		return nil, err
	}
	return client.HVals(ctx, key).Result()
}

// HIncrBy 原子增加 field 中的整数并返回新值；不存在 Field 按 0 处理。
func (module *Module) HIncrBy(ctx context.Context, key, field string, increment int64) (int64, error) {
	client, err := module.requireClient(ctx)
	if err != nil {
		return 0, err
	}
	return client.HIncrBy(ctx, key, field, increment).Result()
}

// HScan 从 cursor 开始按 pattern 增量扫描 Field，count 是大于 0 的服务端提示数。
//
// 调用方必须循环到 nextCursor 为 0；单轮数量与顺序不保证。结果 Map 只表示本轮数据。
func (module *Module) HScan(ctx context.Context, key string, cursor uint64, pattern string, count int64) (map[string]string, uint64, error) {
	if count <= 0 {
		return nil, 0, invalidArgument("redismodule HScan Count 必须大于 0")
	}
	client, err := module.requireClient(ctx)
	if err != nil {
		return nil, 0, err
	}
	values, next, err := client.HScan(ctx, key, cursor, pattern, count).Result()
	if err != nil {
		return nil, 0, err
	}
	if len(values)%2 != 0 {
		return nil, 0, invalidArgument("redismodule HScan 返回格式无效")
	}
	result := make(map[string]string, len(values)/2)
	for index := 0; index < len(values); index += 2 {
		result[values[index]] = values[index+1]
	}
	return result, next, nil
}
