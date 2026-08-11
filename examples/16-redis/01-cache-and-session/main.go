// 本示例展示 Redis Module 的 PB 玩家缓存、滑动会话和一次性 Token。
package main

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/duanhf2012/origin/v3/application"
	"github.com/duanhf2012/origin/v3/service"
	"github.com/duanhf2012/origin/v3/sysmodule/redismodule"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/structpb"
)

var app = application.New()

// PlayerCacheModule 组合 Redis 生命周期，并集中管理玩家缓存与会话 Key。
type PlayerCacheModule struct{ redismodule.Module }

// OnInit 只读取和冻结配置；此时不会连接 Redis。
func (module *PlayerCacheModule) OnInit() error {
	var current redismodule.Config
	if err := module.GetServiceConfigStrict("redis", &current); err != nil {
		return err
	}
	return module.Setup(current)
}

func playerKey(playerID int64) string  { return fmt.Sprintf("dev:player:{%d}:profile", playerID) }
func sessionKey(playerID int64) string { return fmt.Sprintf("dev:player:{%d}:session", playerID) }

// SavePlayer 使用 Protobuf 编码玩家缓存；基础 Module 不决定业务序列化格式。
func (module *PlayerCacheModule) SavePlayer(ctx context.Context, playerID int64, player *structpb.Struct) error {
	data, err := proto.Marshal(player)
	if err != nil {
		return fmt.Errorf("marshal player %d: %w", playerID, err)
	}
	return module.Set(ctx, playerKey(playerID), data, 15*time.Minute)
}

// LoadPlayer 区分缓存 Miss 与损坏数据；真实项目在 Miss 时从数据库回源。
func (module *PlayerCacheModule) LoadPlayer(ctx context.Context, playerID int64) (*structpb.Struct, bool, error) {
	data, err := module.GetBytes(ctx, playerKey(playerID))
	if errors.Is(err, redismodule.ErrNil) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	player := &structpb.Struct{}
	if err := proto.Unmarshal(data, player); err != nil {
		return nil, false, fmt.Errorf("decode cached player %d: %w", playerID, err)
	}
	return player, true, nil
}

// TouchSession 原子读取会话并把 TTL 滑动续期为 30 分钟。
func (module *PlayerCacheModule) TouchSession(ctx context.Context, playerID int64) (string, error) {
	return module.GetEx(ctx, sessionKey(playerID), 30*time.Minute)
}

// ConsumeLoginToken 原子读取并删除 Token，防止同一个 Token 被重复使用。
func (module *PlayerCacheModule) ConsumeLoginToken(ctx context.Context, tokenID string) (string, error) {
	return module.GetDel(ctx, "dev:login-token:{"+tokenID+"}")
}

// RunDemo 串联成功、Miss、空字符串和损坏缓存分支。
func (module *PlayerCacheModule) RunDemo(ctx context.Context) error {
	// structpb 的 Number 使用 float64；示例把精确等级写成字符串，真实项目应在自己的 .proto 中使用 int64。
	player, err := structpb.NewStruct(map[string]any{"name": "Alice", "level": "18"})
	if err != nil {
		return err
	}
	if err := module.SavePlayer(ctx, 1001, player); err != nil {
		return err
	}
	loaded, hit, err := module.LoadPlayer(ctx, 1001)
	if err != nil || !hit {
		return fmt.Errorf("load cached player: hit=%v: %w", hit, err)
	}
	if _, hit, err := module.LoadPlayer(ctx, 9999); err != nil || hit {
		return fmt.Errorf("cache miss: hit=%v: %w", hit, err)
	}

	if err := module.MSet(ctx, map[string]any{
		"dev:summary:{1001}:name":  loaded.Fields["name"].GetStringValue(),
		"dev:summary:{1001}:guild": "",
	}); err != nil {
		return err
	}
	summaries, err := module.MGet(ctx, "dev:summary:{1001}:name", "dev:summary:{1001}:guild", "dev:summary:{1001}:missing")
	if err != nil || !summaries[1].Exists || summaries[2].Exists {
		return fmt.Errorf("optional summaries: %+v: %w", summaries, err)
	}

	if err := module.Set(ctx, sessionKey(1001), "session-1001", 30*time.Minute); err != nil {
		return err
	}
	if _, err := module.TouchSession(ctx, 1001); err != nil {
		return err
	}
	if ttl, err := module.PTTL(ctx, sessionKey(1001)); err != nil || ttl <= 0 {
		return fmt.Errorf("session ttl %s: %w", ttl, err)
	}

	if err := module.Set(ctx, "dev:login-token:{token-1}", "player-1001", time.Minute); err != nil {
		return err
	}
	if _, err := module.ConsumeLoginToken(ctx, "token-1"); err != nil {
		return err
	}
	if _, err := module.ConsumeLoginToken(ctx, "token-1"); !errors.Is(err, redismodule.ErrNil) {
		return fmt.Errorf("token should be single-use: %w", err)
	}

	if err := module.Set(ctx, playerKey(1002), []byte("broken protobuf"), time.Minute); err != nil {
		return err
	}
	if _, _, err := module.LoadPlayer(ctx, 1002); err == nil {
		return fmt.Errorf("broken cache should fail decoding")
	}
	module.Logger().Info("Redis cache/session demo completed")
	return nil
}

// CacheService 只负责装配与调度；Redis I/O 在 Await Worker 中执行。
type CacheService struct {
	service.Service
	cache *PlayerCacheModule
}

func (target *CacheService) OnInit() error {
	target.cache = &PlayerCacheModule{}
	return target.AddModule(target.cache)
}

func (target *CacheService) OnStart(context.Context) error {
	if id := target.AfterFunc(100*time.Millisecond, func(ctx context.Context, _ service.TimerID) {
		if err := target.Await(ctx, func(waitCtx context.Context) error { return target.cache.RunDemo(waitCtx) }); err != nil {
			target.Logger().Error("Redis cache/session demo failed: " + err.Error())
		}
	}); id == service.InvalidTimerID {
		return fmt.Errorf("schedule Redis demo failed")
	}
	return nil
}

func init() { app.Setup(&CacheService{}) }
func main() { app.Start() }
