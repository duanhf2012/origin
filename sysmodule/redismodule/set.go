package redismodule

import "context"

// SAdd 添加 members，返回实际新增成员数；members 不能为空。
func (module *Module) SAdd(ctx context.Context, key string, members ...any) (int64, error) {
	if err := requireValues(len(members), "Set Members"); err != nil {
		return 0, err
	}
	client, err := module.requireClient(ctx)
	if err != nil {
		return 0, err
	}
	return client.SAdd(ctx, key, members...).Result()
}

// SRem 删除 members，返回实际删除成员数；members 不能为空。
func (module *Module) SRem(ctx context.Context, key string, members ...any) (int64, error) {
	if err := requireValues(len(members), "Set Members"); err != nil {
		return 0, err
	}
	client, err := module.requireClient(ctx)
	if err != nil {
		return 0, err
	}
	return client.SRem(ctx, key, members...).Result()
}

// SIsMember 报告 member 是否存在；Key 不存在返回 false、nil。
func (module *Module) SIsMember(ctx context.Context, key string, member any) (bool, error) {
	client, err := module.requireClient(ctx)
	if err != nil {
		return false, err
	}
	return client.SIsMember(ctx, key, member).Result()
}

// SMIsMember 按 members 输入顺序返回存在状态；members 不能为空。
func (module *Module) SMIsMember(ctx context.Context, key string, members ...any) ([]bool, error) {
	if err := requireValues(len(members), "Set Members"); err != nil {
		return nil, err
	}
	client, err := module.requireClient(ctx)
	if err != nil {
		return nil, err
	}
	return client.SMIsMember(ctx, key, members...).Result()
}

// SMembers 一次返回全部成员；Key 不存在返回空切片、nil。
//
// 只应用于规模明确的小集合；大集合使用 SScan，避免大响应和分配。
func (module *Module) SMembers(ctx context.Context, key string) ([]string, error) {
	client, err := module.requireClient(ctx)
	if err != nil {
		return nil, err
	}
	return client.SMembers(ctx, key).Result()
}

// SCard 返回集合成员数；Key 不存在返回 0、nil。
func (module *Module) SCard(ctx context.Context, key string) (int64, error) {
	client, err := module.requireClient(ctx)
	if err != nil {
		return 0, err
	}
	return client.SCard(ctx, key).Result()
}

// SPop 随机移除并返回一个成员；集合不存在或为空返回 ErrNil。
func (module *Module) SPop(ctx context.Context, key string) (string, error) {
	client, err := module.requireClient(ctx)
	if err != nil {
		return "", err
	}
	return client.SPop(ctx, key).Result()
}

// SPopN 随机移除并返回最多 count 个成员；count 必须大于 0。
//
// 集合不存在返回空切片、nil。count 必须保持有界。
func (module *Module) SPopN(ctx context.Context, key string, count int64) ([]string, error) {
	if count <= 0 {
		return nil, invalidArgument("redismodule SPopN Count 必须大于 0")
	}
	client, err := module.requireClient(ctx)
	if err != nil {
		return nil, err
	}
	return client.SPopN(ctx, key, count).Result()
}

// SRandMember 随机返回一个成员但不删除；集合不存在或为空返回 ErrNil。
func (module *Module) SRandMember(ctx context.Context, key string) (string, error) {
	client, err := module.requireClient(ctx)
	if err != nil {
		return "", err
	}
	return client.SRandMember(ctx, key).Result()
}

// SRandMemberN 随机返回 count 个成员但不删除；count 必须大于 0。
//
// Redis 的正 count 结果不包含重复成员；集合不存在返回空切片、nil。
func (module *Module) SRandMemberN(ctx context.Context, key string, count int64) ([]string, error) {
	if count <= 0 {
		return nil, invalidArgument("redismodule SRandMemberN Count 必须大于 0")
	}
	client, err := module.requireClient(ctx)
	if err != nil {
		return nil, err
	}
	return client.SRandMemberN(ctx, key, count).Result()
}

// SMove 原子移动一个 member，返回源集合是否包含并移动了该成员。
//
// Cluster 下 source 与 destination 必须位于同一 Slot。
func (module *Module) SMove(ctx context.Context, source, destination string, member any) (bool, error) {
	if err := module.validateSameSlot([]string{source, destination}); err != nil {
		return false, err
	}
	client, err := module.requireClient(ctx)
	if err != nil {
		return false, err
	}
	return client.SMove(ctx, source, destination, member).Result()
}

// SDiff 返回第一个集合相对其余集合的差集；keys 不能为空。
//
// Key 不存在按空集合处理。结果可能很大；Cluster 下全部 Key 必须同 Slot。
func (module *Module) SDiff(ctx context.Context, keys ...string) ([]string, error) {
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
	return client.SDiff(ctx, keys...).Result()
}

// SInter 返回全部集合的交集；keys 不能为空。
//
// 结果可能很大；Cluster 下全部 Key 必须位于同一 Slot。
func (module *Module) SInter(ctx context.Context, keys ...string) ([]string, error) {
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
	return client.SInter(ctx, keys...).Result()
}

// SUnion 返回全部集合的并集；keys 不能为空。
//
// 结果可能很大；Cluster 下全部 Key 必须位于同一 Slot。
func (module *Module) SUnion(ctx context.Context, keys ...string) ([]string, error) {
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
	return client.SUnion(ctx, keys...).Result()
}

// SScan 从 cursor 开始按 pattern 增量扫描成员，count 是大于 0 的服务端提示数。
//
// 调用方必须循环到 nextCursor 为 0；单轮数量和顺序不保证。
func (module *Module) SScan(ctx context.Context, key string, cursor uint64, pattern string, count int64) ([]string, uint64, error) {
	if count <= 0 {
		return nil, 0, invalidArgument("redismodule SScan Count 必须大于 0")
	}
	client, err := module.requireClient(ctx)
	if err != nil {
		return nil, 0, err
	}
	return client.SScan(ctx, key, cursor, pattern, count).Result()
}
