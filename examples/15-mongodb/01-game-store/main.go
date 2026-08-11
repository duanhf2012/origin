// 本示例展示 MongoDB Module 在游戏玩家存储中的三层用法：官方 Collection、Module 便利层和 Origin Await。
package main

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/duanhf2012/origin/v3/application"
	"github.com/duanhf2012/origin/v3/service"
	"github.com/duanhf2012/origin/v3/sysmodule/mongodbmodule"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

var app = application.New()

// Player 是示例中的玩家持久化模型；计数和货币统一使用 int64，避免浮点精度问题。
type Player struct {
	ID        string    `bson:"_id"`
	ServerID  string    `bson:"server_id"`
	Name      string    `bson:"name"`
	Level     int64     `bson:"level"`
	Gold      int64     `bson:"gold"`
	Version   int64     `bson:"version"`
	UpdatedAt time.Time `bson:"updated_at"`
}

// RewardLedger 使用业务奖励 ID 作为唯一键，使重复投递不会重复发放奖励。
type RewardLedger struct {
	ID        string    `bson:"_id"`
	PlayerID  string    `bson:"player_id"`
	Amount    int64     `bson:"amount"`
	CreatedAt time.Time `bson:"created_at"`
}

// GameMongoModule 把 MongoDB 生命周期与全部游戏存储方法集中在一个业务 Module 中。
type GameMongoModule struct {
	mongodbmodule.Module
}

// OnInit 只读取并冻结配置，不建立网络连接。
func (module *GameMongoModule) OnInit() error {
	var config mongodbmodule.Config
	if err := module.GetServiceConfigStrict("mongodb", &config); err != nil {
		return err
	}
	return module.Setup(config)
}

// OnStart 先连接并探活，再创建业务需要的索引；任一索引失败都会回滚 Client。
func (module *GameMongoModule) OnStart(ctx context.Context) error {
	if err := module.Module.OnStart(ctx); err != nil {
		return err
	}
	if err := module.ensureSchema(ctx); err != nil {
		return errors.Join(err, module.Module.OnStop(ctx))
	}
	return nil
}

func (module *GameMongoModule) ensureSchema(ctx context.Context) error {
	// 服务器内等级榜按 level 降序、_id 升序稳定分页；唯一奖励 ID 由 _id 自带唯一索引保证。
	_, err := module.EnsureIndex(
		ctx,
		"players",
		bson.D{{Key: "server_id", Value: 1}, {Key: "level", Value: -1}, {Key: "_id", Value: 1}},
		options.Index().SetName("server_level_player"),
	)
	if err != nil {
		return err
	}
	_, err = module.EnsureIndex(
		ctx,
		"reward_ledgers",
		bson.D{{Key: "player_id", Value: 1}, {Key: "created_at", Value: -1}},
		options.Index().SetName("player_reward_time"),
	)
	return err
}

// UpsertPlayer 展示“不存在则插入，存在则更新”，且不额外读取文档。
func (module *GameMongoModule) UpsertPlayer(ctx context.Context, player Player) error {
	player.UpdatedAt = time.Now().UTC()
	_, err := module.Collection("players").UpdateOne(
		ctx,
		bson.D{{Key: "_id", Value: player.ID}},
		bson.D{
			{Key: "$set", Value: bson.D{
				{Key: "server_id", Value: player.ServerID},
				{Key: "name", Value: player.Name},
				{Key: "level", Value: player.Level},
				{Key: "gold", Value: player.Gold},
				{Key: "updated_at", Value: player.UpdatedAt},
			}},
			{Key: "$setOnInsert", Value: bson.D{{Key: "version", Value: int64(1)}}},
		},
		options.UpdateOne().SetUpsert(true),
	)
	return err
}

// UpsertAndGet 展示“不存在则插入，存在则更新，并原子返回更新后的文档”。
func (module *GameMongoModule) UpsertAndGet(
	ctx context.Context,
	playerID string,
	name string,
) (Player, error) {
	var result Player
	err := module.Collection("players").FindOneAndUpdate(
		ctx,
		bson.D{{Key: "_id", Value: playerID}},
		bson.D{
			{Key: "$set", Value: bson.D{{Key: "name", Value: name}, {Key: "updated_at", Value: time.Now().UTC()}}},
			{Key: "$setOnInsert", Value: bson.D{
				{Key: "server_id", Value: "s1"},
				{Key: "level", Value: int64(1)},
				{Key: "gold", Value: int64(0)},
				{Key: "version", Value: int64(1)},
			}},
		},
		options.FindOneAndUpdate().SetUpsert(true).SetReturnDocument(options.After),
	).Decode(&result)
	return result, err
}

// SpendGold 通过过滤条件与 $inc 在单文档内原子扣减金币，避免“先查余额再扣款”的竞态。
func (module *GameMongoModule) SpendGold(ctx context.Context, playerID string, amount int64) (bool, error) {
	if amount <= 0 {
		return false, fmt.Errorf("amount must be positive")
	}
	result, err := module.Collection("players").UpdateOne(
		ctx,
		bson.D{{Key: "_id", Value: playerID}, {Key: "gold", Value: bson.D{{Key: "$gte", Value: amount}}}},
		bson.D{{Key: "$inc", Value: bson.D{{Key: "gold", Value: -amount}, {Key: "version", Value: int64(1)}}}},
	)
	return err == nil && result.ModifiedCount == 1, err
}

// RenameWithVersion 使用 version 实现乐观锁；false 表示文档不存在或版本已经变化。
func (module *GameMongoModule) RenameWithVersion(
	ctx context.Context,
	playerID string,
	expectedVersion int64,
	name string,
) (bool, error) {
	result, err := module.Collection("players").UpdateOne(
		ctx,
		bson.D{{Key: "_id", Value: playerID}, {Key: "version", Value: expectedVersion}},
		bson.D{
			{Key: "$set", Value: bson.D{{Key: "name", Value: name}, {Key: "updated_at", Value: time.Now().UTC()}}},
			{Key: "$inc", Value: bson.D{{Key: "version", Value: int64(1)}}},
		},
	)
	return err == nil && result.ModifiedCount == 1, err
}

// ListPlayers 返回有上限、稳定排序的玩家列表；生产分页还应保存最后一个复合排序键。
func (module *GameMongoModule) ListPlayers(ctx context.Context, serverID string, limit int64) ([]Player, error) {
	if limit <= 0 || limit > 100 {
		return nil, fmt.Errorf("limit must be in [1,100]")
	}
	cursor, err := module.Collection("players").Find(
		ctx,
		bson.D{{Key: "server_id", Value: serverID}},
		options.Find().SetSort(bson.D{{Key: "level", Value: -1}, {Key: "_id", Value: 1}}).SetLimit(limit),
	)
	if err != nil {
		return nil, err
	}
	defer cursor.Close(ctx)
	var players []Player
	if err := cursor.All(ctx, &players); err != nil {
		return nil, err
	}
	return players, nil
}

// RaiseLevels 使用 BulkWrite 把多个独立写操作合并为一次网络往返；它本身不是事务。
func (module *GameMongoModule) RaiseLevels(ctx context.Context, playerIDs []string) error {
	models := make([]mongo.WriteModel, 0, len(playerIDs))
	for _, playerID := range playerIDs {
		models = append(models, mongo.NewUpdateOneModel().
			SetFilter(bson.D{{Key: "_id", Value: playerID}}).
			SetUpdate(bson.D{{Key: "$inc", Value: bson.D{{Key: "level", Value: int64(1)}}}}))
	}
	if len(models) == 0 {
		return nil
	}
	_, err := module.Collection("players").BulkWrite(ctx, models, options.BulkWrite().SetOrdered(false))
	return err
}

// GrantRewardOnce 使用事务和奖励唯一键确保同一业务奖励不会重复增加金币。
func (module *GameMongoModule) GrantRewardOnce(
	ctx context.Context,
	rewardID string,
	playerID string,
	amount int64,
) (bool, error) {
	granted := false
	err := module.WithTransaction(ctx, func(transactionCtx context.Context) error {
		_, err := module.Collection("reward_ledgers").InsertOne(transactionCtx, RewardLedger{
			ID: rewardID, PlayerID: playerID, Amount: amount, CreatedAt: time.Now().UTC(),
		})
		if mongo.IsDuplicateKeyError(err) {
			return nil
		}
		if err != nil {
			return err
		}
		result, err := module.Collection("players").UpdateOne(
			transactionCtx,
			bson.D{{Key: "_id", Value: playerID}},
			bson.D{{Key: "$inc", Value: bson.D{{Key: "gold", Value: amount}}}},
		)
		if err != nil {
			return err
		}
		if result.MatchedCount != 1 {
			return fmt.Errorf("player %s not found", playerID)
		}
		granted = true
		return nil
	})
	return granted, err
}

// TransferGold 在一个事务内完成两个玩家之间的金币转移；回调不执行任何外部副作用。
func (module *GameMongoModule) TransferGold(
	ctx context.Context,
	fromPlayerID string,
	toPlayerID string,
	amount int64,
) error {
	return module.WithTransaction(ctx, func(transactionCtx context.Context) error {
		debit, err := module.Collection("players").UpdateOne(
			transactionCtx,
			bson.D{{Key: "_id", Value: fromPlayerID}, {Key: "gold", Value: bson.D{{Key: "$gte", Value: amount}}}},
			bson.D{{Key: "$inc", Value: bson.D{{Key: "gold", Value: -amount}}}},
		)
		if err != nil {
			return err
		}
		if debit.ModifiedCount != 1 {
			return fmt.Errorf("source player missing or has insufficient gold")
		}
		credit, err := module.Collection("players").UpdateOne(
			transactionCtx,
			bson.D{{Key: "_id", Value: toPlayerID}},
			bson.D{{Key: "$inc", Value: bson.D{{Key: "gold", Value: amount}}}},
		)
		if err != nil {
			return err
		}
		if credit.ModifiedCount != 1 {
			return fmt.Errorf("target player not found")
		}
		return nil
	})
}

// DeletePlayer 明确组合服务器与玩家 ID，避免只按宽泛业务字段误删多行。
func (module *GameMongoModule) DeletePlayer(ctx context.Context, serverID, playerID string) (bool, error) {
	result, err := module.Collection("players").DeleteOne(
		ctx,
		bson.D{{Key: "_id", Value: playerID}, {Key: "server_id", Value: serverID}},
	)
	return err == nil && result.DeletedCount == 1, err
}

// RunDemo 串联高频游戏存储场景；调用方必须为本次 I/O 提供明确的 context 预算。
func (module *GameMongoModule) RunDemo(ctx context.Context) error {
	for _, player := range []Player{
		{ID: "player-1", ServerID: "s1", Name: "Alice", Level: 10, Gold: 1000},
		{ID: "player-2", ServerID: "s1", Name: "Bob", Level: 8, Gold: 500},
	} {
		if err := module.UpsertPlayer(ctx, player); err != nil {
			return err
		}
	}
	updated, err := module.UpsertAndGet(ctx, "player-1", "Alice-Origin")
	if err != nil {
		return err
	}
	renamed, err := module.RenameWithVersion(ctx, updated.ID, updated.Version, "Alice-v2")
	if err != nil || !renamed {
		return fmt.Errorf("optimistic rename: renamed=%v: %w", renamed, err)
	}
	spent, err := module.SpendGold(ctx, "player-1", 100)
	if err != nil || !spent {
		return fmt.Errorf("spend gold: spent=%v: %w", spent, err)
	}
	if err := module.RaiseLevels(ctx, []string{"player-1", "player-2"}); err != nil {
		return err
	}
	if _, err := module.GrantRewardOnce(ctx, "reward-demo-1", "player-1", 50); err != nil {
		return err
	}
	if err := module.TransferGold(ctx, "player-1", "player-2", 20); err != nil {
		return err
	}
	players, err := module.ListPlayers(ctx, "s1", 20)
	if err != nil {
		return err
	}
	module.Logger().Info(fmt.Sprintf("MongoDB demo completed: players=%d", len(players)))
	return nil
}

// GameStoreService 只负责装配业务 Module，并用 Await 释放阻塞数据库 I/O 期间的 Service 执行权。
type GameStoreService struct {
	service.Service
	mongo *GameMongoModule
}

func (target *GameStoreService) OnInit() error {
	target.mongo = &GameMongoModule{}
	return target.AddModule(target.mongo)
}

func (target *GameStoreService) OnStart(context.Context) error {
	if id := target.AfterFunc(100*time.Millisecond, func(ctx context.Context, _ service.TimerID) {
		err := target.Await(ctx, func(waitCtx context.Context) error {
			return target.mongo.RunDemo(waitCtx)
		})
		if err != nil {
			target.Logger().Error("MongoDB demo failed: " + err.Error())
		}
	}); id == service.InvalidTimerID {
		return fmt.Errorf("schedule MongoDB demo failed")
	}
	return nil
}

func init() { app.Setup(&GameStoreService{}) }

func main() { app.Start() }
