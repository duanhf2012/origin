package mongodbmodule

import (
	"context"

	"go.mongodb.org/mongo-driver/v2/mongo"
	mongooptions "go.mongodb.org/mongo-driver/v2/mongo/options"
	"go.mongodb.org/mongo-driver/v2/mongo/readpref"
)

// clientRuntime 收窄 Module 与官方 Driver 的耦合面，使生命周期和失败回滚可以稳定单测。
// 该接口保持包内私有，不把测试工厂或替代 Driver 暴露为长期兼容外观。
type clientRuntime interface {
	client() *mongo.Client
	database(string) *mongo.Database
	collection(string, string) *mongo.Collection
	ping(context.Context) error
	disconnect(context.Context) error
	createIndex(context.Context, string, string, mongo.IndexModel) (string, error)
	withSession(context.Context, func(context.Context) error, ...mongooptions.Lister[mongooptions.SessionOptions]) error
	withTransaction(context.Context, func(context.Context) error, ...mongooptions.Lister[mongooptions.TransactionOptions]) error
}

type runtimeFactory func(*mongooptions.ClientOptions) (clientRuntime, error)

type driverRuntime struct {
	clientHandle *mongo.Client
}

func newDriverRuntime(options *mongooptions.ClientOptions) (clientRuntime, error) {
	client, err := mongo.Connect(options)
	if err != nil {
		return nil, err
	}
	return &driverRuntime{clientHandle: client}, nil
}

func (runtime *driverRuntime) client() *mongo.Client { return runtime.clientHandle }

func (runtime *driverRuntime) database(name string) *mongo.Database {
	return runtime.clientHandle.Database(name)
}

func (runtime *driverRuntime) collection(database, name string) *mongo.Collection {
	return runtime.clientHandle.Database(database).Collection(name)
}

func (runtime *driverRuntime) ping(ctx context.Context) error {
	return runtime.clientHandle.Ping(ctx, readpref.Primary())
}

func (runtime *driverRuntime) disconnect(ctx context.Context) error {
	return runtime.clientHandle.Disconnect(ctx)
}

func (runtime *driverRuntime) createIndex(
	ctx context.Context,
	database string,
	collection string,
	model mongo.IndexModel,
) (string, error) {
	return runtime.clientHandle.Database(database).Collection(collection).Indexes().CreateOne(ctx, model)
}

func (runtime *driverRuntime) withSession(
	ctx context.Context,
	fn func(context.Context) error,
	options ...mongooptions.Lister[mongooptions.SessionOptions],
) error {
	// Session 必须在任意回调结果下释放；WithSession 把 SessionContext 注入回调 context。
	session, err := runtime.clientHandle.StartSession(options...)
	if err != nil {
		return err
	}
	defer session.EndSession(ctx)
	return mongo.WithSession(ctx, session, fn)
}

func (runtime *driverRuntime) withTransaction(
	ctx context.Context,
	fn func(context.Context) error,
	options ...mongooptions.Lister[mongooptions.TransactionOptions],
) error {
	// Driver 根据错误标签重试事务或提交，因此不能把回调强制简化为一次执行。
	session, err := runtime.clientHandle.StartSession()
	if err != nil {
		return err
	}
	defer session.EndSession(ctx)
	_, err = session.WithTransaction(ctx, func(transactionCtx context.Context) (any, error) {
		return nil, fn(transactionCtx)
	}, options...)
	return err
}
