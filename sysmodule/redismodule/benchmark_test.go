package redismodule

import (
	"context"
	"testing"
	"time"
)

func BenchmarkOptionalStrings(b *testing.B) {
	// go-redis 的 MGET/HMGET Result 只包含 string 或 nil。
	values := []any{"Alice", nil, "", "30"}
	b.ReportAllocs()
	for b.Loop() {
		result, err := optionalStrings(values)
		if err != nil || len(result) != len(values) {
			b.Fatal(err)
		}
	}
}

func BenchmarkIntegrationGet(b *testing.B) {
	config := integrationConfig(b)
	module, err := New(config)
	if err != nil {
		b.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err = module.OnStart(ctx); err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { _ = module.OnStop(context.Background()) })
	if err = module.Set(context.Background(), "benchmark:{get}:value", "Alice", time.Minute); err != nil {
		b.Fatal(err)
	}

	b.Run("convenience", func(b *testing.B) {
		b.ReportAllocs()
		ctx := context.Background()
		for b.Loop() {
			if _, err := module.Get(ctx, "benchmark:{get}:value"); err != nil {
				b.Fatal(err)
			}
		}
	})
	b.Run("native-client", func(b *testing.B) {
		b.ReportAllocs()
		ctx := context.Background()
		client := module.Client()
		for b.Loop() {
			if _, err := client.Get(ctx, "benchmark:{get}:value").Result(); err != nil {
				b.Fatal(err)
			}
		}
	})
}
