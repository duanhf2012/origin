package mongodbmodule_test

import (
	"context"
	"time"

	"github.com/duanhf2012/origin/v3/sysmodule/mongodbmodule"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

// ExampleModule_Collection 展示最常用的官方 Collection 链式 CRUD 外观。
func ExampleModule_Collection() {
	var module *mongodbmodule.Module
	var ctx context.Context
	var player struct {
		ID string `bson:"_id"`
	}

	// 实际调用应放在 Module 启动成功后的业务路径，并传入有 deadline 的 ctx。
	_ = module.Collection("players").
		FindOne(ctx, bson.D{{Key: "_id", Value: "player-1"}}).
		Decode(&player)
}

// ExampleModule_EnsureUniqueIndex 展示启动阶段创建有序复合唯一索引。
func ExampleModule_EnsureUniqueIndex() {
	var module *mongodbmodule.Module
	var ctx context.Context
	_, _ = module.EnsureUniqueIndex(
		ctx,
		"players",
		bson.D{{Key: "server_id", Value: 1}, {Key: "name", Value: 1}},
		options.Index().SetName("server_player_name"),
	)
}

// ExampleModule_EnsureTTLIndex 展示按文档时间字段过期的 TTL 索引。
func ExampleModule_EnsureTTLIndex() {
	var module *mongodbmodule.Module
	var ctx context.Context
	_, _ = module.EnsureTTLIndex(ctx, "sessions", "expire_at", 0*time.Second)
}

// ExampleModule_WithTransaction 展示事务回调必须始终使用 transactionCtx。
func ExampleModule_WithTransaction() {
	var module *mongodbmodule.Module
	var ctx context.Context
	_ = module.WithTransaction(ctx, func(transactionCtx context.Context) error {
		_, err := module.Collection("players").UpdateOne(
			transactionCtx,
			bson.D{{Key: "_id", Value: "player-1"}},
			bson.D{{Key: "$inc", Value: bson.D{{Key: "gold", Value: int64(100)}}}},
		)
		return err
	})
}
