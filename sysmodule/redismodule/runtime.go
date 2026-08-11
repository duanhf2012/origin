package redismodule

import (
	"context"

	"github.com/redis/go-redis/v9"
)

type clientRuntime interface {
	client() redis.UniversalClient
	ping(context.Context) error
	close() error
}

type runtimeFactory func(*redis.UniversalOptions, Mode, []redis.Hook) (clientRuntime, error)

type driverRuntime struct {
	clientHandle redis.UniversalClient
	mode         Mode
}

func newDriverRuntime(options *redis.UniversalOptions, mode Mode, hooks []redis.Hook) (clientRuntime, error) {
	client := redis.NewUniversalClient(options)
	for _, hook := range hooks {
		client.AddHook(hook)
	}
	return &driverRuntime{clientHandle: client, mode: mode}, nil
}

func (runtime *driverRuntime) client() redis.UniversalClient { return runtime.clientHandle }

func (runtime *driverRuntime) ping(ctx context.Context) error {
	if runtime.mode != ModeCluster {
		return runtime.clientHandle.Ping(ctx).Err()
	}
	cluster, ok := runtime.clientHandle.(*redis.ClusterClient)
	if !ok {
		return ErrUnsupportedMode
	}
	return cluster.ForEachMaster(ctx, func(ctx context.Context, client *redis.Client) error {
		return client.Ping(ctx).Err()
	})
}

func (runtime *driverRuntime) close() error { return runtime.clientHandle.Close() }
