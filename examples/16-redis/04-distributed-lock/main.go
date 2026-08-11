// 本示例展示 Redis Lease Lock 在游戏任务协调中的正确边界。
package main

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/duanhf2012/origin/v3/application"
	"github.com/duanhf2012/origin/v3/service"
	"github.com/duanhf2012/origin/v3/sysmodule/redismodule"
)

var app = application.New()

// GameLockModule 把锁 Key、等待边界和最终幂等条件集中在业务边界。
type GameLockModule struct{ redismodule.Module }

func (module *GameLockModule) OnInit() error {
	var current redismodule.Config
	if err := module.GetServiceConfigStrict("redis", &current); err != nil {
		return err
	}
	return module.Setup(current)
}

// RebuildPlayerCache 使用 TryLock 抑制缓存击穿；未获得锁时本实例不重复回源。
func (module *GameLockModule) RebuildPlayerCache(ctx context.Context, playerID int64) (bool, error) {
	key := fmt.Sprintf("dev:player:{%d}:cache-rebuild-lock", playerID)
	lease, acquired, err := module.TryLock(ctx, key, 3*time.Second)
	if err != nil || !acquired {
		return false, err
	}
	defer func() {
		cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), time.Second)
		defer cancel()
		_ = lease.Release(cleanup)
	}()
	// 真实项目在这里回源数据库，并再次校验回源结果是否仍可写入缓存。
	return true, module.Set(ctx, fmt.Sprintf("dev:player:{%d}:profile", playerID), "rebuilt", 15*time.Minute)
}

// SettleMatch 使用短 Lease 协调实例，同时用 SetNX 结算记录承担最终幂等约束。
func (module *GameLockModule) SettleMatch(ctx context.Context, matchID string) error {
	lockKey := "dev:match:{" + matchID + "}:settle-lock"
	ledgerKey := "dev:match:{" + matchID + "}:settled"
	return module.WithLock(ctx, lockKey, 2*time.Second, 300*time.Millisecond, func(ctx context.Context) error {
		created, err := module.SetNX(ctx, ledgerKey, "settled", 24*time.Hour)
		if err != nil {
			return err
		}
		if !created {
			return nil
		}
		// 真正结算仍应使用数据库唯一结算 ID、事务或 Outbox，不能只相信 Lease。
		return nil
	})
}

// RefreshRanking 表示跨服定时任务抢占；未获得锁时本实例跳过本轮。
func (module *GameLockModule) RefreshRanking(ctx context.Context, season string) (bool, error) {
	ran := false
	err := module.WithLock(ctx, "dev:season:{"+season+"}:refresh-lock", 5*time.Second, 200*time.Millisecond, func(context.Context) error {
		ran = true
		return nil
	})
	if errors.Is(err, redismodule.ErrLockNotObtained) {
		return false, nil
	}
	return ran, err
}

// RunLongTask 显式刷新 Lease；Refresh 失败后立即停止受保护操作。
func (module *GameLockModule) RunLongTask(ctx context.Context, taskID string) error {
	lease, err := module.Lock(ctx, "dev:task:{"+taskID+"}:lock", 300*time.Millisecond, time.Second)
	if err != nil {
		return err
	}
	defer func() {
		cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), time.Second)
		defer cancel()
		_ = lease.Release(cleanup)
	}()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(100 * time.Millisecond):
	}
	if err := lease.Refresh(ctx, 300*time.Millisecond); err != nil {
		return fmt.Errorf("refresh task lease: %w", err)
	}
	return nil
}

func (module *GameLockModule) RunDemo(ctx context.Context) error {
	keys := []string{"dev:player:{1001}:cache-rebuild-lock", "dev:match:{match-1}:settle-lock", "dev:match:{match-1}:settled"}
	for _, key := range keys {
		if _, err := module.Del(ctx, key); err != nil {
			return err
		}
	}

	blocker, acquired, err := module.TryLock(ctx, "dev:player:{1001}:cache-rebuild-lock", time.Second)
	if err != nil || !acquired {
		return fmt.Errorf("prepare contention: %w", err)
	}
	rebuilt, err := module.RebuildPlayerCache(ctx, 1001)
	if err != nil || rebuilt {
		return fmt.Errorf("contended rebuild: rebuilt=%v: %w", rebuilt, err)
	}
	if err := blocker.Release(ctx); err != nil {
		return err
	}
	if rebuilt, err = module.RebuildPlayerCache(ctx, 1001); err != nil || !rebuilt {
		return fmt.Errorf("cache rebuild: rebuilt=%v: %w", rebuilt, err)
	}

	if err := module.SettleMatch(ctx, "match-1"); err != nil {
		return err
	}
	if err := module.SettleMatch(ctx, "match-1"); err != nil {
		return err
	} // 幂等记录防止重复结算。
	if ran, err := module.RefreshRanking(ctx, "2026-s1"); err != nil || !ran {
		return fmt.Errorf("ranking refresh: ran=%v: %w", ran, err)
	}
	if err := module.RunLongTask(ctx, "snapshot-1"); err != nil {
		return err
	}

	expired, acquired, err := module.TryLock(ctx, "dev:task:{expired}:lock", 50*time.Millisecond)
	if err != nil || !acquired {
		return fmt.Errorf("obtain expiring lock: %w", err)
	}
	time.Sleep(80 * time.Millisecond)
	if err := expired.Release(ctx); !errors.Is(err, redismodule.ErrLockNotHeld) {
		return fmt.Errorf("expired lease should not release: %w", err)
	}
	module.Logger().Info("Redis distributed-lock demo completed")
	return nil
}

type LockService struct {
	service.Service
	locks *GameLockModule
}

func (target *LockService) OnInit() error {
	target.locks = &GameLockModule{}
	return target.AddModule(target.locks)
}

func (target *LockService) OnStart(context.Context) error {
	if id := target.AfterFunc(100*time.Millisecond, func(ctx context.Context, _ service.TimerID) {
		if err := target.Await(ctx, func(waitCtx context.Context) error { return target.locks.RunDemo(waitCtx) }); err != nil {
			target.Logger().Error("Redis distributed-lock demo failed: " + err.Error())
		}
	}); id == service.InvalidTimerID {
		return fmt.Errorf("schedule Redis demo failed")
	}
	return nil
}

func init() { app.Setup(&LockService{}) }
func main() { app.Start() }
