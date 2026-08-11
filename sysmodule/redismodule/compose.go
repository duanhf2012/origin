package redismodule

import (
	"context"

	"github.com/redis/go-redis/v9"
)

// Ping 使用 ctx 检查当前逻辑 Redis 部署。
//
// Cluster 会并发检查当前全部 Primary，调用成本随 Shard 数量增长，不应用于业务热路径。
func (module *Module) Ping(ctx context.Context) error {
	if ctx == nil {
		return ErrInvalidArgument
	}
	if module == nil {
		return ErrNotRunning
	}
	holder := module.runtime.Load()
	if holder == nil {
		return ErrNotRunning
	}
	return holder.runtime.ping(ctx)
}

// Do 执行未进入便利层的普通 Redis 命令，并返回原始 Result；args 不能为空。
//
// 方法在当前 goroutine 同步执行。调用方需理解命令的返回类型、Cluster Slot、幂等性和大结果风险。
func (module *Module) Do(ctx context.Context, args ...any) (any, error) {
	if err := requireValues(len(args), "Command Args"); err != nil {
		return nil, err
	}
	client, err := module.requireClient(ctx)
	if err != nil {
		return nil, err
	}
	return client.Do(ctx, args...).Result()
}

// WithClient 在当前 goroutine 同步执行 fn，并借用官方 UniversalClient。
//
// fn 不能为空；ctx 不能为空且 Module 必须运行。回调不得 Close Client，也不得把 Client 交给
// 失去 Module 生命周期所有权的后台 goroutine。本方法不独占连接且不建立事务。
func (module *Module) WithClient(ctx context.Context, fn func(context.Context, redis.UniversalClient) error) error {
	if fn == nil {
		return invalidArgument("redismodule WithClient Callback 不能为空")
	}
	client, err := module.requireClient(ctx)
	if err != nil {
		return err
	}
	return fn(ctx, client)
}

// Pipelined 在 fn 中收集命令并发送，返回每条命令对象；fn 不能为空。
//
// 回调在当前 goroutine 执行，返回错误时不发送已收集命令。Pipeline 只减少网络往返，不保证原子性；
// Cluster 可按节点拆批，因此不保证跨节点顺序。必须限制命令数和参数总量。
func (module *Module) Pipelined(ctx context.Context, fn func(context.Context, redis.Pipeliner) error) ([]redis.Cmder, error) {
	if fn == nil {
		return nil, invalidArgument("redismodule Pipelined Callback 不能为空")
	}
	client, err := module.requireClient(ctx)
	if err != nil {
		return nil, err
	}
	return client.Pipelined(ctx, func(pipe redis.Pipeliner) error { return fn(ctx, pipe) })
}

// TxPipelined 使用 MULTI/EXEC 执行 fn 收集的命令，返回每条命令对象；fn 不能为空。
//
// 回调在当前 goroutine 执行，返回错误时不提交。EXEC 不提供数据库式运行时错误回滚；Cluster 中涉及的
// Key 必须同 Slot，无法从通用命令对象安全推断全部 Key，CROSSSLOT 由 Redis/Driver 返回。
func (module *Module) TxPipelined(ctx context.Context, fn func(context.Context, redis.Pipeliner) error) ([]redis.Cmder, error) {
	if fn == nil {
		return nil, invalidArgument("redismodule TxPipelined Callback 不能为空")
	}
	client, err := module.requireClient(ctx)
	if err != nil {
		return nil, err
	}
	return client.TxPipelined(ctx, func(pipe redis.Pipeliner) error { return fn(ctx, pipe) })
}

// Watch 对 keys 执行乐观并发控制并同步调用 fn；keys 与 fn 均不能为空。
//
// 冲突保留 redis.TxFailedErr，Module 不自动重试。Cluster 下 keys 必须同 Slot；拓扑恢复可能再次调用
// 回调，因此回调必须可重入且不能直接发送奖励、邮件或执行数据库等不可重复副作用。
func (module *Module) Watch(ctx context.Context, fn func(context.Context, *redis.Tx) error, keys ...string) error {
	if fn == nil {
		return invalidArgument("redismodule Watch Callback 不能为空")
	}
	if err := requireValues(len(keys), "Watch Keys"); err != nil {
		return err
	}
	if err := module.validateSameSlot(keys); err != nil {
		return err
	}
	client, err := module.requireClient(ctx)
	if err != nil {
		return err
	}
	return client.Watch(ctx, func(tx *redis.Tx) error { return fn(ctx, tx) }, keys...)
}

// RunScript 执行复用的官方 script，keys 只放 Redis Key，args 只放普通参数。
//
// script 不能为空；Cluster 下全部 keys 必须同 Slot。go-redis 负责 EVALSHA 与 NOSCRIPT 后 EVAL 回退。
// Lua 在 Redis 内原子执行但会阻塞服务端，脚本和输入必须保持短小有界。
func (module *Module) RunScript(ctx context.Context, script *redis.Script, keys []string, args ...any) (any, error) {
	if script == nil {
		return nil, invalidArgument("redismodule Script 不能为空")
	}
	if err := module.validateSameSlot(keys); err != nil {
		return nil, err
	}
	client, err := module.requireClient(ctx)
	if err != nil {
		return nil, err
	}
	return script.Run(ctx, client, keys, args...).Result()
}
