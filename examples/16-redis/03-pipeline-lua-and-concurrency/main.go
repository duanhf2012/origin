// 本示例展示 Redis Pipeline、Watch 和 Lua 在游戏并发场景中的边界。
package main

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/duanhf2012/origin/v3/application"
	"github.com/duanhf2012/origin/v3/service"
	"github.com/duanhf2012/origin/v3/sysmodule/redismodule"
	"github.com/redis/go-redis/v9"
)

var app = application.New()

// 奖励记录与钱包 Key 使用同一个 {playerID} Hash Tag，保证 Cluster 下位于同一 Slot。
var grantRewardScript = redis.NewScript(`
if redis.call('EXISTS', KEYS[1]) == 1 then return 0 end
redis.call('HINCRBY', KEYS[2], 'gold', ARGV[1])
redis.call('SET', KEYS[1], '1', 'EX', ARGV[2])
return 1
`)

// AtomicGameModule 集中管理批处理、乐观并发和业务 Lua。
type AtomicGameModule struct{ redismodule.Module }

func (module *AtomicGameModule) OnInit() error {
	var current redismodule.Config
	if err := module.GetServiceConfigStrict("redis", &current); err != nil {
		return err
	}
	return module.Setup(current)
}

// BatchLoad 使用 Pipeline 减少独立读取的网络往返；批次不是原子事务。
func (module *AtomicGameModule) BatchLoad(ctx context.Context, playerID int64) error {
	prefix := fmt.Sprintf("dev:player:{%d}", playerID)
	commands, err := module.Pipelined(ctx, func(ctx context.Context, pipe redis.Pipeliner) error {
		pipe.Get(ctx, prefix+":profile")
		pipe.HGet(ctx, prefix+":wallet", "gold")
		pipe.SCard(ctx, prefix+":friends")
		return nil
	})
	if err != nil && !errors.Is(err, redis.Nil) {
		return err
	}
	// Pipeline 可能只有部分命令失败；生产代码必须逐个检查 commands[i].Err()。
	if len(commands) != 3 {
		return fmt.Errorf("unexpected pipeline result count: %d", len(commands))
	}
	return nil
}

// RenameWithVersion 使用 Watch 与有界重试更新玩家名；回调可被再次执行，不能包含外部副作用。
func (module *AtomicGameModule) RenameWithVersion(ctx context.Context, playerID int64, name string) error {
	key := fmt.Sprintf("dev:player:{%d}:versioned-name", playerID)
	for attempt := 0; attempt < 3; attempt++ {
		err := module.Watch(ctx, func(ctx context.Context, tx *redis.Tx) error {
			version, err := tx.HGet(ctx, key, "version").Int64()
			if errors.Is(err, redis.Nil) {
				version, err = 0, nil
			}
			if err != nil {
				return err
			}
			_, err = tx.TxPipelined(ctx, func(pipe redis.Pipeliner) error {
				pipe.HSet(ctx, key, "name", name, "version", version+1)
				return nil
			})
			return err
		}, key)
		if err == nil {
			return nil
		}
		if !errors.Is(err, redis.TxFailedErr) {
			return err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(time.Duration(attempt+1) * 10 * time.Millisecond):
		}
	}
	return fmt.Errorf("rename conflict after bounded retries: %w", redis.TxFailedErr)
}

// GrantRewardOnce 在 Redis 内原子检查奖励 ID 并增加金币；返回本次是否真正发放。
func (module *AtomicGameModule) GrantRewardOnce(ctx context.Context, playerID int64, rewardID string, gold int64) (bool, error) {
	ledgerKey := fmt.Sprintf("dev:player:{%d}:reward:%s", playerID, rewardID)
	walletKey := fmt.Sprintf("dev:player:{%d}:wallet", playerID)
	value, err := module.RunScript(ctx, grantRewardScript, []string{ledgerKey, walletKey}, gold, int64((24*time.Hour)/time.Second))
	if err != nil {
		return false, err
	}
	granted, ok := value.(int64)
	if !ok {
		return false, fmt.Errorf("unexpected Lua result %T", value)
	}
	return granted == 1, nil
}

func (module *AtomicGameModule) RunDemo(ctx context.Context) error {
	if err := module.Set(ctx, "dev:player:{1001}:profile", "Alice", time.Minute); err != nil {
		return err
	}
	if _, err := module.HSetMany(ctx, "dev:player:{1001}:wallet", map[string]any{"gold": int64(100)}); err != nil {
		return err
	}
	if err := module.BatchLoad(ctx, 1001); err != nil {
		return err
	}
	if err := module.RenameWithVersion(ctx, 1001, "Alice-Origin"); err != nil {
		return err
	}
	first, err := module.GrantRewardOnce(ctx, 1001, "daily-20260811", 50)
	if err != nil || !first {
		return fmt.Errorf("first reward: granted=%v: %w", first, err)
	}
	second, err := module.GrantRewardOnce(ctx, 1001, "daily-20260811", 50)
	if err != nil || second {
		return fmt.Errorf("duplicate reward: granted=%v: %w", second, err)
	}
	module.Logger().Info("Redis pipeline/lua/concurrency demo completed")
	return nil
}

type AtomicService struct {
	service.Service
	atomic *AtomicGameModule
}

func (target *AtomicService) OnInit() error {
	target.atomic = &AtomicGameModule{}
	return target.AddModule(target.atomic)
}

func (target *AtomicService) OnStart(context.Context) error {
	if id := target.AfterFunc(100*time.Millisecond, func(ctx context.Context, _ service.TimerID) {
		if err := target.Await(ctx, func(waitCtx context.Context) error { return target.atomic.RunDemo(waitCtx) }); err != nil {
			target.Logger().Error("Redis pipeline/lua/concurrency demo failed: " + err.Error())
		}
	}); id == service.InvalidTimerID {
		return fmt.Errorf("schedule Redis demo failed")
	}
	return nil
}

func init() { app.Setup(&AtomicService{}) }
func main() { app.Start() }
