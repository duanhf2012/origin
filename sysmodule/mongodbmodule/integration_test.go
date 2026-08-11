package mongodbmodule_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/duanhf2012/origin/v3/sysmodule/mongodbmodule"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

// TestMongoDBReplicaSetIntegration 在显式提供测试 URI 时验证真实 Replica Set 的完整公共路径。
// 默认跳过，避免普通单元测试意外访问开发者或生产数据库。
func TestMongoDBReplicaSetIntegration(t *testing.T) {
	uri := os.Getenv("ORIGIN_MONGODB_TEST_URI")
	if uri == "" {
		t.Skip("ORIGIN_MONGODB_TEST_URI is not set")
	}
	database := os.Getenv("ORIGIN_MONGODB_TEST_DATABASE")
	if database == "" {
		database = "origin_mongodbmodule_integration"
	}

	module, err := mongodbmodule.New(mongodbmodule.Config{URI: uri, Database: database})
	if err != nil {
		t.Fatal(err)
	}
	startCtx, startCancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer startCancel()
	if err := module.OnStart(startCtx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		stopCtx, stopCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer stopCancel()
		if err := module.OnStop(stopCtx); err != nil {
			t.Errorf("OnStop() error = %v", err)
		}
	})

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	collectionName := fmt.Sprintf("players_%d", time.Now().UnixNano())
	collection := module.Collection(collectionName)
	if collection == nil {
		t.Fatal("Collection() returned nil after start")
	}
	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cleanupCancel()
		if err := collection.Drop(cleanupCtx); err != nil {
			t.Errorf("drop collection: %v", err)
		}
	})

	// 索引、单文档 CRUD 与返回更新后文档使用真正的服务端语义验证。
	if _, err := module.EnsureUniqueIndex(
		ctx,
		collectionName,
		bson.D{{Key: "account_id", Value: 1}},
		options.Index().SetName("account_id_unique"),
	); err != nil {
		t.Fatal(err)
	}
	if _, err := module.EnsureTTLIndex(ctx, collectionName, "expire_at", 0); err != nil {
		t.Fatal(err)
	}
	if _, err := collection.InsertOne(ctx, bson.D{
		{Key: "_id", Value: "player-1"},
		{Key: "account_id", Value: "account-1"},
		{Key: "gold", Value: int64(100)},
	}); err != nil {
		t.Fatal(err)
	}
	var player struct {
		ID   string `bson:"_id"`
		Gold int64  `bson:"gold"`
	}
	if err := collection.FindOneAndUpdate(
		ctx,
		bson.D{{Key: "_id", Value: "player-1"}, {Key: "gold", Value: bson.D{{Key: "$gte", Value: int64(40)}}}},
		bson.D{{Key: "$inc", Value: bson.D{{Key: "gold", Value: int64(-40)}}}},
		options.FindOneAndUpdate().SetReturnDocument(options.After),
	).Decode(&player); err != nil {
		t.Fatal(err)
	}
	if player.Gold != 60 {
		t.Fatalf("gold = %d, want 60", player.Gold)
	}

	// 多 goroutine 争抢同一唯一键只能有一个成功，验证真实连接池并发和服务端唯一性。
	var successes atomic.Int64
	var duplicateErrors atomic.Int64
	successfulID := make(chan string, 1)
	var wait sync.WaitGroup
	for index := range 8 {
		wait.Add(1)
		go func(index int) {
			defer wait.Done()
			_, insertErr := collection.InsertOne(ctx, bson.D{
				{Key: "_id", Value: fmt.Sprintf("duplicate-%d", index)},
				{Key: "account_id", Value: "same-account"},
			})
			switch {
			case insertErr == nil:
				successes.Add(1)
				successfulID <- fmt.Sprintf("duplicate-%d", index)
			case mongo.IsDuplicateKeyError(insertErr):
				duplicateErrors.Add(1)
			default:
				t.Errorf("concurrent InsertOne() error = %v", insertErr)
			}
		}(index)
	}
	wait.Wait()
	if successes.Load() != 1 || duplicateErrors.Load() != 7 {
		t.Fatalf("unique race successes=%d duplicates=%d", successes.Load(), duplicateErrors.Load())
	}
	winner := <-successfulID

	// Session 和事务必须使用 Module 传入的回调 Context，并在同一事务内提交两次更新。
	if err := module.WithSession(ctx, func(sessionCtx context.Context) error {
		return collection.FindOne(sessionCtx, bson.D{{Key: "_id", Value: "player-1"}}).Err()
	}); err != nil {
		t.Fatal(err)
	}
	if err := module.WithTransaction(ctx, func(transactionCtx context.Context) error {
		if _, updateErr := collection.UpdateOne(
			transactionCtx,
			bson.D{{Key: "_id", Value: "player-1"}},
			bson.D{{Key: "$inc", Value: bson.D{{Key: "gold", Value: int64(-10)}}}},
		); updateErr != nil {
			return updateErr
		}
		_, updateErr := collection.UpdateOne(
			transactionCtx,
			bson.D{{Key: "_id", Value: winner}},
			bson.D{{Key: "$inc", Value: bson.D{{Key: "gold", Value: int64(10)}}}},
		)
		return updateErr
	}); err != nil {
		t.Fatal(err)
	}

	canceledCtx, canceled := context.WithCancel(context.Background())
	canceled()
	if err := module.Ping(canceledCtx); !errors.Is(err, context.Canceled) {
		t.Fatalf("Ping(canceled) error = %v", err)
	}
}
