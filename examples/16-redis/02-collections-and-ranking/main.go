// 本示例展示 Redis Hash、Set、List、整数 Sorted Set 和 Bitmap 的游戏用法。
package main

import (
	"context"
	"fmt"
	"time"

	"github.com/duanhf2012/origin/v3/application"
	"github.com/duanhf2012/origin/v3/service"
	"github.com/duanhf2012/origin/v3/sysmodule/redismodule"
)

var app = application.New()

// GameCollectionModule 集中管理集合 Key 和有界读取规则。
type GameCollectionModule struct{ redismodule.Module }

func (module *GameCollectionModule) OnInit() error {
	var current redismodule.Config
	if err := module.GetServiceConfigStrict("redis", &current); err != nil {
		return err
	}
	return module.Setup(current)
}

// scanHash 展示必须循环 Cursor；count 只是提示，不是单轮硬上限。
func (module *GameCollectionModule) scanHash(ctx context.Context, key string) (map[string]string, error) {
	result := make(map[string]string)
	var cursor uint64
	for {
		values, next, err := module.HScan(ctx, key, cursor, "*", 50)
		if err != nil {
			return nil, err
		}
		for field, value := range values {
			result[field] = value
		}
		cursor = next
		if cursor == 0 {
			return result, nil
		}
	}
}

// scanSet 展示大集合使用 SScan，而不是一次 SMembers。
func (module *GameCollectionModule) scanSet(ctx context.Context, key string) ([]string, error) {
	var result []string
	var cursor uint64
	for {
		members, next, err := module.SScan(ctx, key, cursor, "*", 50)
		if err != nil {
			return nil, err
		}
		result = append(result, members...)
		cursor = next
		if cursor == 0 {
			return result, nil
		}
	}
}

func (module *GameCollectionModule) RunDemo(ctx context.Context) error {
	playerKey := "dev:player:{1001}:fields"
	if _, err := module.HSetMany(ctx, playerKey, map[string]any{"name": "Alice", "level": int64(18), "gold": int64(900)}); err != nil {
		return err
	}
	if _, err := module.HIncrBy(ctx, playerKey, "gold", -100); err != nil {
		return err
	}
	fields, err := module.scanHash(ctx, playerKey)
	if err != nil || fields["gold"] != "800" {
		return fmt.Errorf("scan player hash: %+v: %w", fields, err)
	}

	onlineKey := "dev:server:{s1}:online"
	if _, err := module.SAdd(ctx, onlineKey, "1001", "1002", "1003"); err != nil {
		return err
	}
	online, err := module.scanSet(ctx, onlineKey)
	if err != nil || len(online) != 3 {
		return fmt.Errorf("scan online players: %d: %w", len(online), err)
	}

	matchKey := "dev:match:{s1}:candidates"
	if _, err := module.RPush(ctx, matchKey, "1001", "1002", "1003"); err != nil {
		return err
	}
	matched, err := module.LPopN(ctx, matchKey, 2)
	if err != nil || len(matched) != 2 {
		return fmt.Errorf("bounded match pop: %+v: %w", matched, err)
	}
	// List 没有确认、重放和消费者组；关键任务队列应使用 Streams 或 Kafka。

	rankKey := "dev:season:{2026}:score"
	if _, err := module.ZAdd(ctx, rankKey,
		redismodule.ScoredMember{Member: "1001", Score: 1200},
		redismodule.ScoredMember{Member: "1002", Score: 1500},
		redismodule.ScoredMember{Member: "1003", Score: 900},
	); err != nil {
		return err
	}
	if _, err := module.ZIncrBy(ctx, rankKey, 100, "1001"); err != nil {
		return err
	}
	top, err := module.ZRevRangeWithScores(ctx, rankKey, 0, 9)
	if err != nil || len(top) != 3 || top[0].Member != "1002" {
		return fmt.Errorf("integer rank: %+v: %w", top, err)
	}
	// 同分先到、多字段组合等规则属于业务层，应使用自己的 Lua/Key 结构实现。

	signKey := "dev:sign:{1001}:202608"
	for _, dayOffset := range []int64{0, 1, 7} {
		if _, err := module.SetBit(ctx, signKey, dayOffset, true); err != nil {
			return err
		}
	}
	if days, err := module.BitCount(ctx, signKey, 0, -1); err != nil || days != 3 {
		return fmt.Errorf("sign days=%d: %w", days, err)
	}
	module.Logger().Info("Redis collections/ranking demo completed")
	return nil
}

type CollectionService struct {
	service.Service
	collections *GameCollectionModule
}

func (target *CollectionService) OnInit() error {
	target.collections = &GameCollectionModule{}
	return target.AddModule(target.collections)
}

func (target *CollectionService) OnStart(context.Context) error {
	if id := target.AfterFunc(100*time.Millisecond, func(ctx context.Context, _ service.TimerID) {
		if err := target.Await(ctx, func(waitCtx context.Context) error { return target.collections.RunDemo(waitCtx) }); err != nil {
			target.Logger().Error("Redis collections/ranking demo failed: " + err.Error())
		}
	}); id == service.InvalidTimerID {
		return fmt.Errorf("schedule Redis demo failed")
	}
	return nil
}

func init() { app.Setup(&CollectionService{}) }
func main() { app.Start() }
