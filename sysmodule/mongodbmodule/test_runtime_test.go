package mongodbmodule

import (
	"context"
	"errors"
	"sync"

	"go.mongodb.org/mongo-driver/v2/mongo"
	mongooptions "go.mongodb.org/mongo-driver/v2/mongo/options"
)

type contextMarker struct{}

// fakeRuntime 记录 Module 对 Driver 边界的调用，测试生命周期、选项和回调而不依赖外部 MongoDB。
type fakeRuntime struct {
	mu sync.Mutex

	clientHandle   *mongo.Client
	pingErr        error
	disconnectErr  error
	createIndexErr error
	createFailAt   int
	sessionErr     error
	transactionErr error

	pingCalls       int
	disconnectCalls int
	created         []mongo.IndexModel
	collections     []string
	sessionCalls    int
	transactionRuns int
}

func newFakeRuntime() *fakeRuntime {
	client, _ := mongo.Connect(mongooptions.Client().ApplyURI("mongodb://127.0.0.1:27017"))
	return &fakeRuntime{clientHandle: client}
}

func (runtime *fakeRuntime) client() *mongo.Client { return runtime.clientHandle }

func (runtime *fakeRuntime) database(name string) *mongo.Database {
	return runtime.clientHandle.Database(name)
}

func (runtime *fakeRuntime) collection(database, name string) *mongo.Collection {
	runtime.mu.Lock()
	runtime.collections = append(runtime.collections, database+"."+name)
	runtime.mu.Unlock()
	return runtime.clientHandle.Database(database).Collection(name)
}

func (runtime *fakeRuntime) ping(context.Context) error {
	runtime.mu.Lock()
	defer runtime.mu.Unlock()
	runtime.pingCalls++
	return runtime.pingErr
}

func (runtime *fakeRuntime) disconnect(context.Context) error {
	runtime.mu.Lock()
	defer runtime.mu.Unlock()
	runtime.disconnectCalls++
	return runtime.disconnectErr
}

func (runtime *fakeRuntime) createIndex(
	_ context.Context,
	_ string,
	collection string,
	model mongo.IndexModel,
) (string, error) {
	runtime.mu.Lock()
	defer runtime.mu.Unlock()
	runtime.created = append(runtime.created, model)
	runtime.collections = append(runtime.collections, collection)
	if runtime.createFailAt > 0 && len(runtime.created) == runtime.createFailAt {
		return "", runtime.createIndexErr
	}
	return "index_" + collection + "_" + string(rune('0'+len(runtime.created))), nil
}

func (runtime *fakeRuntime) withSession(
	ctx context.Context,
	fn func(context.Context) error,
	_ ...mongooptions.Lister[mongooptions.SessionOptions],
) error {
	runtime.mu.Lock()
	runtime.sessionCalls++
	runtime.mu.Unlock()
	if runtime.sessionErr != nil {
		return runtime.sessionErr
	}
	return fn(context.WithValue(ctx, contextMarker{}, "session"))
}

func (runtime *fakeRuntime) withTransaction(
	ctx context.Context,
	fn func(context.Context) error,
	_ ...mongooptions.Lister[mongooptions.TransactionOptions],
) error {
	if runtime.transactionErr != nil {
		return runtime.transactionErr
	}
	// 执行两次模拟 Driver 的合法重试，确保 Module 不错误地假定回调只执行一次。
	for range 2 {
		runtime.mu.Lock()
		runtime.transactionRuns++
		runtime.mu.Unlock()
		if err := fn(context.WithValue(ctx, contextMarker{}, "transaction")); err != nil {
			return err
		}
	}
	return nil
}

func configuredTestModule(runtime *fakeRuntime) *Module {
	module, err := New(
		Config{URI: "mongodb://127.0.0.1:27017/?directConnection=true", Database: "game"},
		withRuntimeFactoryForTest(func(*mongooptions.ClientOptions) (clientRuntime, error) {
			return runtime, nil
		}),
	)
	if err != nil {
		panic(err)
	}
	return module
}

var errFake = errors.New("fake driver error")
