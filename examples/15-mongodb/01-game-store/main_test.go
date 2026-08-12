package main

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/duanhf2012/origin/v3/sysmodule/mongodbmodule"
	"go.mongodb.org/mongo-driver/v2/bson"
)

// TestGrantRewardOnceIsRepeatable 使用真实 Replica Set 锁定教程的幂等奖励语义：同一奖励
// 连续执行两次时，第二次必须快速返回“未新增”，且玩家金币只能增加一次。
func TestGrantRewardOnceIsRepeatable(t *testing.T) {
	uri := os.Getenv("ORIGIN_MONGODB_TEST_URI")
	if uri == "" {
		t.Skip("ORIGIN_MONGODB_TEST_URI is not set")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	database := fmt.Sprintf("origin_game_example_test_%d", time.Now().UnixNano())
	module, err := mongodbmodule.New(mongodbmodule.Config{URI: uri, Database: database})
	if err != nil {
		t.Fatal(err)
	}
	if err := module.OnStart(ctx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cleanupCancel()
		_ = module.Database().Drop(cleanupCtx)
		_ = module.Module.OnStop(cleanupCtx)
	})

	_, err = module.Collection("players").InsertOne(ctx, Player{ID: "player-1", ServerID: "s1", Gold: 100})
	if err != nil {
		t.Fatal(err)
	}
	first, err := grantRewardOnce(ctx, module, "reward-repeat", "player-1", 50)
	if err != nil || !first {
		t.Fatalf("first GrantRewardOnce() = %t, %v", first, err)
	}
	second, err := grantRewardOnce(ctx, module, "reward-repeat", "player-1", 50)
	if err != nil || second {
		t.Fatalf("second GrantRewardOnce() = %t, %v", second, err)
	}

	var player Player
	if err := module.Collection("players").FindOne(ctx, bson.D{{Key: "_id", Value: "player-1"}}).Decode(&player); err != nil {
		t.Fatal(err)
	}
	if player.Gold != 150 {
		t.Fatalf("player gold = %d, want 150", player.Gold)
	}
}
