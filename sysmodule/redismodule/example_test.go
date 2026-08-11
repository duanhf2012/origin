package redismodule_test

import (
	"context"
	"errors"
	"time"

	"github.com/duanhf2012/origin/v3/sysmodule/redismodule"
	"github.com/redis/go-redis/v9"
)

func ExampleModule_Scan() {
	// SCAN 每次只返回一页；必须持续使用返回的 cursor，直到 cursor == 0。
	scanAll := func(ctx context.Context, module *redismodule.Module) ([]string, error) {
		var cursor uint64
		var keys []string
		for {
			page, next, err := module.Scan(ctx, cursor, "player:*", 200)
			if err != nil {
				return nil, err
			}
			keys = append(keys, page...)
			cursor = next
			if cursor == 0 {
				return keys, nil
			}
		}
	}
	_ = scanAll
}

func ExampleModule_HScan() {
	// HSCAN、SSCAN 和 ZSCAN 同样是游标分页；count 只是 Redis 的工作量提示。
	scanHash := func(ctx context.Context, module *redismodule.Module, key string) error {
		var cursor uint64
		for {
			fields, next, err := module.HScan(ctx, key, cursor, "item:*", 100)
			if err != nil {
				return err
			}
			_ = fields // 处理本页 field/value。
			cursor = next
			if cursor == 0 {
				return nil
			}
		}
	}
	_ = scanHash
}

func ExampleModule_MGet() {
	readPlayers := func(ctx context.Context, module *redismodule.Module) error {
		values, err := module.MGet(ctx, "player:{1001}:name", "player:{1001}:guild")
		if err != nil {
			return err
		}
		// Exists 能区分“不存在”和“存在但值为空字符串”。
		if values[1].Exists {
			_ = values[1].Value
		}
		return nil
	}
	_ = readPlayers
}

func ExampleModule_LMove() {
	claimJob := func(ctx context.Context, module *redismodule.Module) (string, error) {
		// 将任务从待处理队列原子移动到处理中队列，避免先 Pop 再 Push 的丢失窗口。
		return module.LMove(ctx, "queue:{match}:ready", "queue:{match}:working", redismodule.ListLeft, redismodule.ListRight)
	}
	_ = claimJob
}

func ExampleModule_ZRangeByScore() {
	loadTier := func(ctx context.Context, module *redismodule.Module) ([]string, error) {
		// 分数、边界、偏移和数量均为 int64；便利层不接受会丢精度的 float64 分数。
		return module.ZRangeByScore(ctx, "rank:{season-8}", 1000, 1999, 0, 100)
	}
	_ = loadTier
}

func ExampleModule_Pipelined() {
	writeSnapshot := func(ctx context.Context, module *redismodule.Module) error {
		_, err := module.Pipelined(ctx, func(ctx context.Context, pipe redis.Pipeliner) error {
			pipe.Set(ctx, "player:{1001}:name", "Alice", time.Hour)
			pipe.HSet(ctx, "player:{1001}:state", "level", 30)
			return nil
		})
		return err
	}
	_ = writeSnapshot
}

func ExampleModule_Watch() {
	update := func(ctx context.Context, module *redismodule.Module) error {
		key := "player:{1001}:currency"
		// Watch 冲突返回 redis.TxFailedErr；重试次数和退避应由业务限定。
		return module.Watch(ctx, func(ctx context.Context, tx *redis.Tx) error {
			current, err := tx.Get(ctx, key).Int64()
			if err != nil && !errors.Is(err, redis.Nil) {
				return err
			}
			_, err = tx.TxPipelined(ctx, func(pipe redis.Pipeliner) error {
				pipe.Set(ctx, key, current+10, 0)
				return nil
			})
			return err
		}, key)
	}
	_ = update
}

func ExampleModule_RunScript() {
	grantOnce := redis.NewScript(`
		if redis.call("SET", KEYS[1], "1", "NX", "EX", ARGV[1]) then
			return redis.call("INCRBY", KEYS[2], ARGV[2])
		end
		return 0
	`)
	run := func(ctx context.Context, module *redismodule.Module) (any, error) {
		// Cluster 下所有 Key 使用相同 {playerID}，保证落入同一 Slot。
		return module.RunScript(ctx, grantOnce,
			[]string{"reward:{1001}:mail-9", "currency:{1001}:gold"}, 86400, 100)
	}
	_ = run
}

func ExampleModule_TryLock() {
	rebuild := func(ctx context.Context, module *redismodule.Module) error {
		lease, acquired, err := module.TryLock(ctx, "lock:{guild-7}:cache", 3*time.Second)
		if err != nil || !acquired {
			return err // 未获得锁时走旧缓存或稍后重试。
		}
		defer lease.Release(context.WithoutCancel(ctx))
		return nil
	}
	_ = rebuild
}

func ExampleModule_WithLock() {
	settle := func(ctx context.Context, module *redismodule.Module) error {
		return module.WithLock(ctx, "lock:{match-88}:settle", 5*time.Second, time.Second,
			func(ctx context.Context) error {
				// Lease 只能减少并发，奖励等关键写入仍需业务幂等键或数据库约束兜底。
				return nil
			})
	}
	_ = settle
}
