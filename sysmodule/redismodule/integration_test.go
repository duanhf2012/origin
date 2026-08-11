package redismodule

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	originconfig "github.com/duanhf2012/origin/v3/config"
	"github.com/redis/go-redis/v9"
)

func integrationConfig(t testing.TB) Config {
	t.Helper()
	address := strings.TrimSpace(os.Getenv("ORIGIN_REDIS_TEST_ADDRESS"))
	if address == "" {
		t.Skip("ORIGIN_REDIS_TEST_ADDRESS is not configured")
	}
	return Config{
		Mode: ModeStandalone, Addresses: []string{address}, Username: os.Getenv("ORIGIN_REDIS_TEST_USERNAME"),
		Password: os.Getenv("ORIGIN_REDIS_TEST_PASSWORD"), Database: 0, PoolSize: 16,
		MaxActiveConnections: 16, MaxConcurrentDials: 8, MinIdleConnections: 2,
	}
}

func TestIntegrationPoolExhaustionIsBounded(t *testing.T) {
	current := integrationConfig(t)
	current.PoolSize = 1
	current.MaxActiveConnections = 1
	current.MaxConcurrentDials = 1
	current.MinIdleConnections = 0
	current.PoolTimeout = originconfig.Duration(120 * time.Millisecond)
	module, err := New(current)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err = module.OnStart(ctx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = module.OnStop(context.Background()) })

	key := "pool:{exhaustion}:empty"
	blockCtx, blockCancel := context.WithCancel(context.Background())
	blocked := make(chan error, 1)
	go func() {
		blocked <- module.Client().BLPop(blockCtx, 5*time.Second, key).Err()
	}()
	deadline := time.Now().Add(2 * time.Second)
	for module.Client().PoolStats().TotalConns == 0 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	time.Sleep(30 * time.Millisecond) // 给 BLPOP 留出占用唯一连接的时间。

	started := time.Now()
	_, err = module.Get(context.Background(), "pool:{exhaustion}:probe")
	elapsed := time.Since(started)
	blockCancel()
	<-blocked
	if !errors.Is(err, redis.ErrPoolTimeout) {
		t.Fatalf("expected bounded pool timeout, got %v", err)
	}
	if elapsed < 80*time.Millisecond || elapsed > time.Second {
		t.Fatalf("unexpected pool wait bound: %s", elapsed)
	}
}

func startIntegrationModule(t *testing.T) *Module {
	t.Helper()
	module, err := New(integrationConfig(t))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err = module.OnStart(ctx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		stopCtx, stopCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer stopCancel()
		if stopErr := module.OnStop(stopCtx); stopErr != nil {
			t.Errorf("stop Redis Module: %v", stopErr)
		}
	})
	return module
}

func TestIntegrationStandaloneCommands(t *testing.T) {
	module := startIntegrationModule(t)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if err := module.Client().FlushDB(ctx).Err(); err != nil {
		t.Fatal(err)
	}

	if err := module.Set(ctx, "game:{1001}:name", "Alice", time.Minute); err != nil {
		t.Fatal(err)
	}
	if value, err := module.Get(ctx, "game:{1001}:name"); err != nil || value != "Alice" {
		t.Fatalf("Get: %q %v", value, err)
	}
	if _, err := module.Get(ctx, "missing"); !errors.Is(err, ErrNil) {
		t.Fatalf("missing Get: %v", err)
	}
	if ok, err := module.SetNX(ctx, "game:{1001}:name", "Bob", 0); err != nil || ok {
		t.Fatalf("SetNX: %t %v", ok, err)
	}
	if ok, err := module.SetXX(ctx, "missing", "value", 0); err != nil || ok {
		t.Fatalf("SetXX miss: %t %v", ok, err)
	}
	if ok, err := module.SetXX(ctx, "game:{1001}:name", "Bob", time.Minute); err != nil || !ok {
		t.Fatalf("SetXX hit: %t %v", ok, err)
	}
	if err := module.SetKeepTTL(ctx, "game:{1001}:name", "Alice"); err != nil {
		t.Fatal(err)
	}
	if ttl, err := module.PTTL(ctx, "game:{1001}:name"); err != nil || ttl <= 0 {
		t.Fatalf("PTTL: %s %v", ttl, err)
	}
	if value, err := module.GetEx(ctx, "game:{1001}:name", 2*time.Minute); err != nil || value != "Alice" {
		t.Fatalf("GetEx: %q %v", value, err)
	}
	if data, err := module.GetBytes(ctx, "game:{1001}:name"); err != nil || string(data) != "Alice" {
		t.Fatalf("GetBytes: %q %v", data, err)
	}
	if err := module.MSet(ctx, map[string]any{"game:{1001}:empty": "", "game:{1001}:level": 7}); err != nil {
		t.Fatal(err)
	}
	optional, err := module.MGet(ctx, "game:{1001}:empty", "game:{1001}:missing", "game:{1001}:level")
	if err != nil || !optional[0].Exists || optional[1].Exists || optional[2].Value != "7" {
		t.Fatalf("MGet: %+v %v", optional, err)
	}
	if value, err := module.IncrBy(ctx, "game:{1001}:counter", 5); err != nil || value != 5 {
		t.Fatalf("IncrBy: %d %v", value, err)
	}
	if value, err := module.Decr(ctx, "game:{1001}:counter"); err != nil || value != 4 {
		t.Fatalf("Decr: %d %v", value, err)
	}
	if length, err := module.Append(ctx, "game:{1001}:text", "abc"); err != nil || length != 3 {
		t.Fatalf("Append: %d %v", length, err)
	}
	if err := module.Set(ctx, "game:{1001}:token", "once", time.Minute); err != nil {
		t.Fatal(err)
	}
	if value, err := module.GetDel(ctx, "game:{1001}:token"); err != nil || value != "once" {
		t.Fatalf("GetDel: %q %v", value, err)
	}

	if added, err := module.HSet(ctx, "game:{1001}:player", "name", "Alice"); err != nil || !added {
		t.Fatalf("HSet: %t %v", added, err)
	}
	if count, err := module.HSetMany(ctx, "game:{1001}:player", map[string]any{"level": 7, "zone": "cn"}); err != nil || count != 2 {
		t.Fatalf("HSetMany: %d %v", count, err)
	}
	if value, err := module.HGet(ctx, "game:{1001}:player", "level"); err != nil || value != "7" {
		t.Fatalf("HGet: %q %v", value, err)
	}
	if values, err := module.HMGet(ctx, "game:{1001}:player", "zone", "missing"); err != nil || values[0].Value != "cn" || values[1].Exists {
		t.Fatalf("HMGet: %+v %v", values, err)
	}
	if value, err := module.HIncrBy(ctx, "game:{1001}:player", "level", 1); err != nil || value != 8 {
		t.Fatalf("HIncrBy: %d %v", value, err)
	}
	if values, cursor, err := module.HScan(ctx, "game:{1001}:player", 0, "*", 10); err != nil || cursor != 0 || len(values) != 3 {
		t.Fatalf("HScan: %+v %d %v", values, cursor, err)
	}

	if _, err := module.RPush(ctx, "game:{1001}:match", "a", "b", "c"); err != nil {
		t.Fatal(err)
	}
	if value, err := module.LMove(ctx, "game:{1001}:match", "game:{1001}:processing", ListLeft, ListRight); err != nil || value != "a" {
		t.Fatalf("LMove: %q %v", value, err)
	}
	if removed, err := module.LRem(ctx, "game:{1001}:match", 1, "b"); err != nil || removed != 1 {
		t.Fatalf("LRem: %d %v", removed, err)
	}
	if values, err := module.RPopN(ctx, "game:{1001}:match", 2); err != nil || len(values) != 1 || values[0] != "c" {
		t.Fatalf("RPopN: %+v %v", values, err)
	}

	if _, err := module.SAdd(ctx, "game:{1001}:online", "a", "b", "c"); err != nil {
		t.Fatal(err)
	}
	if flags, err := module.SMIsMember(ctx, "game:{1001}:online", "a", "x"); err != nil || !flags[0] || flags[1] {
		t.Fatalf("SMIsMember: %+v %v", flags, err)
	}
	if moved, err := module.SMove(ctx, "game:{1001}:online", "game:{1001}:offline", "a"); err != nil || !moved {
		t.Fatalf("SMove: %t %v", moved, err)
	}
	if members, cursor, err := module.SScan(ctx, "game:{1001}:online", 0, "*", 10); err != nil || cursor != 0 || len(members) != 2 {
		t.Fatalf("SScan: %+v %d %v", members, cursor, err)
	}

	if added, err := module.ZAdd(ctx, "game:{1001}:rank", ScoredMember{Member: "a", Score: 10}, ScoredMember{Member: "b", Score: 20}); err != nil || added != 2 {
		t.Fatalf("ZAdd: %d %v", added, err)
	}
	if changed, err := module.ZAddXX(ctx, "game:{1001}:rank", ScoredMember{Member: "a", Score: 11}, ScoredMember{Member: "missing", Score: 1}); err != nil || changed != 1 {
		t.Fatalf("ZAddXX: %d %v", changed, err)
	}
	if score, err := module.ZIncrBy(ctx, "game:{1001}:rank", 4, "a"); err != nil || score != 15 {
		t.Fatalf("ZIncrBy: %d %v", score, err)
	}
	if values, err := module.ZRevRangeWithScores(ctx, "game:{1001}:rank", 0, 9); err != nil || len(values) != 2 || values[0].Member != "b" {
		t.Fatalf("ZRevRangeWithScores: %+v %v", values, err)
	}
	if values, err := module.ZRangeByScoreWithScores(ctx, "game:{1001}:rank", 10, 20, 0, 10); err != nil || len(values) != 2 {
		t.Fatalf("ZRangeByScore: %+v %v", values, err)
	}
	if values, cursor, err := module.ZScan(ctx, "game:{1001}:rank", 0, "*", 10); err != nil || cursor != 0 || len(values) != 2 {
		t.Fatalf("ZScan: %+v %d %v", values, cursor, err)
	}
	if err := module.Client().ZAdd(ctx, "game:{1001}:fraction", redis.Z{Member: "x", Score: 1.5}).Err(); err != nil {
		t.Fatal(err)
	}
	if _, err := module.ZScore(ctx, "game:{1001}:fraction", "x"); !errors.Is(err, ErrInvalidScore) {
		t.Fatalf("fractional score accepted: %v", err)
	}

	if previous, err := module.SetBit(ctx, "game:{1001}:sign", 3, true); err != nil || previous {
		t.Fatalf("SetBit: %t %v", previous, err)
	}
	if value, err := module.GetBit(ctx, "game:{1001}:sign", 3); err != nil || !value {
		t.Fatalf("GetBit: %t %v", value, err)
	}
	if count, err := module.BitCount(ctx, "game:{1001}:sign", 0, -1); err != nil || count != 1 {
		t.Fatalf("BitCount: %d %v", count, err)
	}

	commands, err := module.Pipelined(ctx, func(ctx context.Context, pipe redis.Pipeliner) error {
		pipe.Set(ctx, "game:{1001}:pipe:a", "a", time.Minute)
		pipe.Set(ctx, "game:{1001}:pipe:b", "b", time.Minute)
		return nil
	})
	if err != nil || len(commands) != 2 {
		t.Fatalf("Pipelined: %d %v", len(commands), err)
	}
	commands, err = module.TxPipelined(ctx, func(ctx context.Context, pipe redis.Pipeliner) error {
		pipe.Incr(ctx, "game:{1001}:tx:a")
		pipe.Incr(ctx, "game:{1001}:tx:b")
		return nil
	})
	if err != nil || len(commands) != 2 {
		t.Fatalf("TxPipelined: %d %v", len(commands), err)
	}

	script := redis.NewScript(`return redis.call('INCRBY', KEYS[1], ARGV[1])`)
	if value, err := module.RunScript(ctx, script, []string{"game:{1001}:lua"}, 3); err != nil || value.(int64) != 3 {
		t.Fatalf("RunScript: %v %v", value, err)
	}
	watchKey := "game:{1001}:watch"
	if err := module.Set(ctx, watchKey, 1, 0); err != nil {
		t.Fatal(err)
	}
	err = module.Watch(ctx, func(ctx context.Context, tx *redis.Tx) error {
		value, readErr := tx.Get(ctx, watchKey).Int64()
		if readErr != nil {
			return readErr
		}
		if _, changeErr := module.Incr(ctx, watchKey); changeErr != nil {
			return changeErr
		}
		_, txErr := tx.TxPipelined(ctx, func(pipe redis.Pipeliner) error { pipe.Set(ctx, watchKey, value+1, 0); return nil })
		return txErr
	}, watchKey)
	if !errors.Is(err, redis.TxFailedErr) {
		t.Fatalf("expected Watch conflict: %v", err)
	}

	lease, acquired, err := module.TryLock(ctx, "game:{1001}:lock", time.Second)
	if err != nil || !acquired {
		t.Fatalf("TryLock: %t %v", acquired, err)
	}
	if ttl, err := lease.TTL(ctx); err != nil || ttl <= 0 {
		t.Fatalf("Lock TTL: %s %v", ttl, err)
	}
	if _, acquired, err := module.TryLock(ctx, "game:{1001}:lock", time.Second); err != nil || acquired {
		t.Fatalf("contended TryLock: %t %v", acquired, err)
	}
	if err := lease.Refresh(ctx, 2*time.Second); err != nil {
		t.Fatal(err)
	}
	if err := lease.Release(ctx); err != nil {
		t.Fatal(err)
	}
	if err := lease.Release(ctx); !errors.Is(err, ErrLockNotHeld) {
		t.Fatalf("duplicate release: %v", err)
	}
	if err := module.WithLock(ctx, "game:{1001}:with-lock", time.Second, time.Second, func(context.Context) error { return nil }); err != nil {
		t.Fatal(err)
	}

	keys, cursor, err := module.Scan(ctx, 0, "game:{1001}:*", 1000)
	if err != nil || cursor != 0 || len(keys) == 0 {
		t.Fatalf("Scan: %d %d %v", len(keys), cursor, err)
	}
	if value, err := module.Do(ctx, "GET", "game:{1001}:name"); err != nil || value != "Alice" {
		t.Fatalf("Do: %v %v", value, err)
	}
}

func TestIntegrationConcurrentIncrementsAndLocks(t *testing.T) {
	module := startIntegrationModule(t)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	prefix := "origin:redis:concurrent:" + strconv.FormatInt(time.Now().UnixNano(), 10)
	const workers = 16
	var wg sync.WaitGroup
	errorsCh := make(chan error, workers)
	for worker := 0; worker < workers; worker++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := module.Incr(ctx, prefix+":counter"); err != nil {
				errorsCh <- err
				return
			}
			if err := module.WithLock(ctx, prefix+":lock", time.Second, 3*time.Second, func(ctx context.Context) error {
				_, err := module.Incr(ctx, prefix+":locked-counter")
				return err
			}); err != nil {
				errorsCh <- err
			}
		}()
	}
	wg.Wait()
	close(errorsCh)
	for err := range errorsCh {
		t.Error(err)
	}
	for _, key := range []string{prefix + ":counter", prefix + ":locked-counter"} {
		value, err := module.Get(ctx, key)
		if err != nil || value != fmt.Sprint(workers) {
			t.Fatalf("%s=%q: %v", key, value, err)
		}
	}
}

func TestIntegrationCancellationAndFailureBranches(t *testing.T) {
	module := startIntegrationModule(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	prefix := "origin:redis:failure:" + strconv.FormatInt(time.Now().UnixNano(), 10)

	canceled, cancelNow := context.WithCancel(ctx)
	cancelNow()
	if _, err := module.Get(canceled, prefix); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled Get: %v", err)
	}

	callbackErr := errors.New("pipeline callback failed")
	if _, err := module.Pipelined(ctx, func(ctx context.Context, pipe redis.Pipeliner) error {
		pipe.Set(ctx, prefix+":not-sent", "value", 0)
		return callbackErr
	}); !errors.Is(err, callbackErr) {
		t.Fatalf("pipeline callback error: %v", err)
	}
	if _, err := module.Get(ctx, prefix+":not-sent"); !errors.Is(err, ErrNil) {
		t.Fatalf("failed pipeline sent commands: %v", err)
	}

	lease, acquired, err := module.TryLock(ctx, prefix+":lock", time.Second)
	if err != nil || !acquired {
		t.Fatalf("initial lock: %t %v", acquired, err)
	}
	defer lease.Release(context.Background())
	started := time.Now()
	if _, err = module.Lock(ctx, prefix+":lock", time.Second, 80*time.Millisecond); !errors.Is(err, ErrLockNotObtained) {
		t.Fatalf("wait timeout: %v", err)
	}
	if elapsed := time.Since(started); elapsed < 60*time.Millisecond || elapsed > time.Second {
		t.Fatalf("unexpected wait bound: %s", elapsed)
	}
	waitCtx, waitCancel := context.WithCancel(ctx)
	waitCancel()
	if _, err = module.Lock(waitCtx, prefix+":lock", time.Second, time.Second); !errors.Is(err, context.Canceled) {
		t.Fatalf("lock cancellation: %v", err)
	}

	if err := module.Client().ZAdd(ctx, prefix+":fraction", redis.Z{Member: "x", Score: 1.5}).Err(); err != nil {
		t.Fatal(err)
	}
	if _, err := module.ZIncrBy(ctx, prefix+":fraction", 1, "x"); !errors.Is(err, ErrInvalidScore) {
		t.Fatalf("fractional ZIncrBy: %v", err)
	}
	if _, err := module.ZAdd(ctx, prefix+":overflow", ScoredMember{Member: "x", Score: MaxExactScore}); err != nil {
		t.Fatal(err)
	}
	if _, err := module.ZIncrBy(ctx, prefix+":overflow", 1, "x"); !errors.Is(err, ErrInvalidScore) {
		t.Fatalf("overflowing ZIncrBy: %v", err)
	}

	businessErr := errors.New("business failed")
	if err := module.WithLock(ctx, prefix+":with-lock", time.Second, time.Second, func(context.Context) error { return businessErr }); !errors.Is(err, businessErr) {
		t.Fatalf("WithLock callback error: %v", err)
	}
	finalLease, acquired, err := module.TryLock(ctx, prefix+":with-lock", time.Second)
	if err != nil || !acquired {
		t.Fatalf("WithLock did not release: %t %v", acquired, err)
	}
	if err := finalLease.Release(ctx); err != nil {
		t.Fatal(err)
	}
}

func TestIntegrationCompleteConvenienceSurface(t *testing.T) {
	module := startIntegrationModule(t)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if err := module.Client().FlushDB(ctx).Err(); err != nil {
		t.Fatal(err)
	}

	if ok, err := module.MSetNX(ctx, map[string]any{"surface:{1}:a": "a", "surface:{1}:b": "b"}); err != nil || !ok {
		t.Fatalf("MSetNX: %t %v", ok, err)
	}
	if count, err := module.Exists(ctx, "surface:{1}:a", "surface:{1}:missing"); err != nil || count != 1 {
		t.Fatalf("Exists: %d %v", count, err)
	}
	if kind, err := module.Type(ctx, "surface:{1}:a"); err != nil || kind != "string" {
		t.Fatalf("Type: %q %v", kind, err)
	}
	if ok, err := module.Expire(ctx, "surface:{1}:a", time.Minute); err != nil || !ok {
		t.Fatalf("Expire: %t %v", ok, err)
	}
	if ttl, err := module.TTL(ctx, "surface:{1}:a"); err != nil || ttl <= 0 {
		t.Fatalf("TTL: %s %v", ttl, err)
	}
	if ok, err := module.Persist(ctx, "surface:{1}:a"); err != nil || !ok {
		t.Fatalf("Persist: %t %v", ok, err)
	}
	if ok, err := module.ExpireAt(ctx, "surface:{1}:a", time.Now().Add(time.Minute)); err != nil || !ok {
		t.Fatalf("ExpireAt: %t %v", ok, err)
	}
	if err := module.Rename(ctx, "surface:{1}:a", "surface:{1}:renamed"); err != nil {
		t.Fatal(err)
	}
	if count, err := module.Unlink(ctx, "surface:{1}:renamed"); err != nil || count != 1 {
		t.Fatalf("Unlink: %d %v", count, err)
	}
	if value, err := module.DecrBy(ctx, "surface:{1}:counter", 3); err != nil || value != -3 {
		t.Fatalf("DecrBy: %d %v", value, err)
	}

	if ok, err := module.HSetNX(ctx, "surface:{1}:hash", "empty", ""); err != nil || !ok {
		t.Fatalf("HSetNX: %t %v", ok, err)
	}
	if data, err := module.HGetBytes(ctx, "surface:{1}:hash", "empty"); err != nil || len(data) != 0 {
		t.Fatalf("HGetBytes: %q %v", data, err)
	}
	if exists, err := module.HExists(ctx, "surface:{1}:hash", "empty"); err != nil || !exists {
		t.Fatalf("HExists: %t %v", exists, err)
	}
	if values, err := module.HGetAll(ctx, "surface:{1}:hash"); err != nil || len(values) != 1 {
		t.Fatalf("HGetAll: %+v %v", values, err)
	}
	if keys, err := module.HKeys(ctx, "surface:{1}:hash"); err != nil || len(keys) != 1 {
		t.Fatalf("HKeys: %+v %v", keys, err)
	}
	if values, err := module.HVals(ctx, "surface:{1}:hash"); err != nil || len(values) != 1 {
		t.Fatalf("HVals: %+v %v", values, err)
	}
	if length, err := module.HLen(ctx, "surface:{1}:hash"); err != nil || length != 1 {
		t.Fatalf("HLen: %d %v", length, err)
	}
	if deleted, err := module.HDel(ctx, "surface:{1}:hash", "empty"); err != nil || deleted != 1 {
		t.Fatalf("HDel: %d %v", deleted, err)
	}

	if _, err := module.LPush(ctx, "surface:{1}:list", "b", "a"); err != nil {
		t.Fatal(err)
	}
	if _, err := module.LPushX(ctx, "surface:{1}:list", "c"); err != nil {
		t.Fatal(err)
	}
	if _, err := module.RPushX(ctx, "surface:{1}:list", "d"); err != nil {
		t.Fatal(err)
	}
	if length, err := module.LLen(ctx, "surface:{1}:list"); err != nil || length != 4 {
		t.Fatalf("LLen: %d %v", length, err)
	}
	if value, err := module.LIndex(ctx, "surface:{1}:list", 0); err != nil || value != "c" {
		t.Fatalf("LIndex: %q %v", value, err)
	}
	if err := module.LSet(ctx, "surface:{1}:list", 0, "z"); err != nil {
		t.Fatal(err)
	}
	if values, err := module.LRange(ctx, "surface:{1}:list", 0, 2); err != nil || len(values) != 3 {
		t.Fatalf("LRange: %+v %v", values, err)
	}
	if err := module.LTrim(ctx, "surface:{1}:list", 0, 2); err != nil {
		t.Fatal(err)
	}
	if value, err := module.LPop(ctx, "surface:{1}:list"); err != nil || value != "z" {
		t.Fatalf("LPop: %q %v", value, err)
	}
	if data, err := module.LPopBytes(ctx, "surface:{1}:list"); err != nil || len(data) == 0 {
		t.Fatalf("LPopBytes: %q %v", data, err)
	}
	if _, err := module.RPush(ctx, "surface:{1}:list", "x", "y"); err != nil {
		t.Fatal(err)
	}
	if value, err := module.RPop(ctx, "surface:{1}:list"); err != nil || value != "y" {
		t.Fatalf("RPop: %q %v", value, err)
	}
	if data, err := module.RPopBytes(ctx, "surface:{1}:list"); err != nil || string(data) != "x" {
		t.Fatalf("RPopBytes: %q %v", data, err)
	}
	if _, err := module.RPush(ctx, "surface:{1}:list", "1", "2"); err != nil {
		t.Fatal(err)
	}
	if values, err := module.LPopN(ctx, "surface:{1}:list", 2); err != nil || len(values) != 2 {
		t.Fatalf("LPopN: %+v %v", values, err)
	}

	if _, err := module.SAdd(ctx, "surface:{1}:set:a", "a", "b", "c"); err != nil {
		t.Fatal(err)
	}
	if _, err := module.SAdd(ctx, "surface:{1}:set:b", "b", "c", "d"); err != nil {
		t.Fatal(err)
	}
	if exists, err := module.SIsMember(ctx, "surface:{1}:set:a", "a"); err != nil || !exists {
		t.Fatalf("SIsMember: %t %v", exists, err)
	}
	if members, err := module.SMembers(ctx, "surface:{1}:set:a"); err != nil || len(members) != 3 {
		t.Fatalf("SMembers: %+v %v", members, err)
	}
	if count, err := module.SCard(ctx, "surface:{1}:set:a"); err != nil || count != 3 {
		t.Fatalf("SCard: %d %v", count, err)
	}
	if values, err := module.SDiff(ctx, "surface:{1}:set:a", "surface:{1}:set:b"); err != nil || len(values) != 1 {
		t.Fatalf("SDiff: %+v %v", values, err)
	}
	if values, err := module.SInter(ctx, "surface:{1}:set:a", "surface:{1}:set:b"); err != nil || len(values) != 2 {
		t.Fatalf("SInter: %+v %v", values, err)
	}
	if values, err := module.SUnion(ctx, "surface:{1}:set:a", "surface:{1}:set:b"); err != nil || len(values) != 4 {
		t.Fatalf("SUnion: %+v %v", values, err)
	}
	if value, err := module.SRandMember(ctx, "surface:{1}:set:a"); err != nil || value == "" {
		t.Fatalf("SRandMember: %q %v", value, err)
	}
	if values, err := module.SRandMemberN(ctx, "surface:{1}:set:a", 2); err != nil || len(values) != 2 {
		t.Fatalf("SRandMemberN: %+v %v", values, err)
	}
	if values, err := module.SPopN(ctx, "surface:{1}:set:a", 1); err != nil || len(values) != 1 {
		t.Fatalf("SPopN: %+v %v", values, err)
	}
	if value, err := module.SPop(ctx, "surface:{1}:set:a"); err != nil || value == "" {
		t.Fatalf("SPop: %q %v", value, err)
	}
	if removed, err := module.SRem(ctx, "surface:{1}:set:b", "d"); err != nil || removed != 1 {
		t.Fatalf("SRem: %d %v", removed, err)
	}

	rankKey := "surface:{1}:rank"
	if _, err := module.ZAdd(ctx, rankKey, ScoredMember{Member: "a", Score: 10}, ScoredMember{Member: "b", Score: 20}, ScoredMember{Member: "c", Score: 30}); err != nil {
		t.Fatal(err)
	}
	if added, err := module.ZAddNX(ctx, rankKey, ScoredMember{Member: "a", Score: 99}, ScoredMember{Member: "d", Score: 40}); err != nil || added != 1 {
		t.Fatalf("ZAddNX: %d %v", added, err)
	}
	if score, err := module.ZScore(ctx, rankKey, "a"); err != nil || score != 10 {
		t.Fatalf("ZScore: %d %v", score, err)
	}
	if rank, err := module.ZRank(ctx, rankKey, "a"); err != nil || rank != 0 {
		t.Fatalf("ZRank: %d %v", rank, err)
	}
	if rank, err := module.ZRevRank(ctx, rankKey, "d"); err != nil || rank != 0 {
		t.Fatalf("ZRevRank: %d %v", rank, err)
	}
	if values, err := module.ZRange(ctx, rankKey, 0, 1); err != nil || len(values) != 2 {
		t.Fatalf("ZRange: %+v %v", values, err)
	}
	if values, err := module.ZRevRange(ctx, rankKey, 0, 1); err != nil || len(values) != 2 {
		t.Fatalf("ZRevRange: %+v %v", values, err)
	}
	if values, err := module.ZRangeWithScores(ctx, rankKey, 0, 3); err != nil || len(values) != 4 {
		t.Fatalf("ZRangeWithScores: %+v %v", values, err)
	}
	if values, err := module.ZRangeByScore(ctx, rankKey, 10, 40, 0, 10); err != nil || len(values) != 4 {
		t.Fatalf("ZRangeByScore: %+v %v", values, err)
	}
	if values, err := module.ZRevRangeByScore(ctx, rankKey, 10, 40, 0, 10); err != nil || len(values) != 4 {
		t.Fatalf("ZRevRangeByScore: %+v %v", values, err)
	}
	if values, err := module.ZRevRangeByScoreWithScores(ctx, rankKey, 10, 40, 0, 10); err != nil || len(values) != 4 {
		t.Fatalf("ZRevRangeByScoreWithScores: %+v %v", values, err)
	}
	if count, err := module.ZCount(ctx, rankKey, 10, 40); err != nil || count != 4 {
		t.Fatalf("ZCount: %d %v", count, err)
	}
	if count, err := module.ZCard(ctx, rankKey); err != nil || count != 4 {
		t.Fatalf("ZCard: %d %v", count, err)
	}
	if removed, err := module.ZRemRangeByRank(ctx, rankKey, 0, 0); err != nil || removed != 1 {
		t.Fatalf("ZRemRangeByRank: %d %v", removed, err)
	}
	if removed, err := module.ZRemRangeByScore(ctx, rankKey, 20, 20); err != nil || removed != 1 {
		t.Fatalf("ZRemRangeByScore: %d %v", removed, err)
	}
	if values, err := module.ZPopMin(ctx, rankKey, 1); err != nil || len(values) != 1 {
		t.Fatalf("ZPopMin: %+v %v", values, err)
	}
	if values, err := module.ZPopMax(ctx, rankKey, 1); err != nil || len(values) != 1 {
		t.Fatalf("ZPopMax: %+v %v", values, err)
	}
	if _, err := module.ZAdd(ctx, rankKey, ScoredMember{Member: "x", Score: 1}); err != nil {
		t.Fatal(err)
	}
	if removed, err := module.ZRem(ctx, rankKey, "x"); err != nil || removed != 1 {
		t.Fatalf("ZRem: %d %v", removed, err)
	}

	if err := module.Set(ctx, "surface:{1}:bits:a", []byte{0x0f}, 0); err != nil {
		t.Fatal(err)
	}
	if err := module.Set(ctx, "surface:{1}:bits:b", []byte{0x33}, 0); err != nil {
		t.Fatal(err)
	}
	if _, err := module.BitOpAnd(ctx, "surface:{1}:bits:and", "surface:{1}:bits:a", "surface:{1}:bits:b"); err != nil {
		t.Fatal(err)
	}
	if _, err := module.BitOpOr(ctx, "surface:{1}:bits:or", "surface:{1}:bits:a", "surface:{1}:bits:b"); err != nil {
		t.Fatal(err)
	}
	if _, err := module.BitOpXor(ctx, "surface:{1}:bits:xor", "surface:{1}:bits:a", "surface:{1}:bits:b"); err != nil {
		t.Fatal(err)
	}
	if _, err := module.BitOpNot(ctx, "surface:{1}:bits:not", "surface:{1}:bits:a"); err != nil {
		t.Fatal(err)
	}

	if err := module.WithClient(ctx, func(ctx context.Context, client redis.UniversalClient) error {
		return client.Set(ctx, "surface:{1}:native", "ok", time.Minute).Err()
	}); err != nil {
		t.Fatal(err)
	}
	lease, acquired, err := module.TryLock(ctx, "surface:{1}:lock", time.Second)
	if err != nil || !acquired || lease.Key() != "surface:{1}:lock" {
		t.Fatalf("Lock Key: %q %t %v", lease.Key(), acquired, err)
	}
	if err := lease.Release(ctx); err != nil {
		t.Fatal(err)
	}
	if deleted, err := module.Del(ctx, "surface:{1}:b", "surface:{1}:native"); err != nil || deleted != 2 {
		t.Fatalf("Del: %d %v", deleted, err)
	}
}

func TestIntegrationAdditionalTopologies(t *testing.T) {
	tests := []struct {
		name                  string
		mode                  Mode
		addressEnv, masterEnv string
	}{
		{name: "sentinel", mode: ModeSentinel, addressEnv: "ORIGIN_REDIS_SENTINEL_ADDRESSES", masterEnv: "ORIGIN_REDIS_SENTINEL_MASTER"},
		{name: "cluster", mode: ModeCluster, addressEnv: "ORIGIN_REDIS_CLUSTER_ADDRESSES"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			raw := strings.TrimSpace(os.Getenv(test.addressEnv))
			if raw == "" {
				t.Skip(test.addressEnv + " is not configured")
			}
			current := Config{Mode: test.mode, Addresses: strings.Split(raw, ","), Password: os.Getenv("ORIGIN_REDIS_TEST_PASSWORD"), PoolSize: 8, MaxActiveConnections: 8, MaxConcurrentDials: 4}
			if test.mode == ModeSentinel {
				current.Sentinel.MasterName = os.Getenv(test.masterEnv)
			}
			module, err := New(current)
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			if err = module.OnStart(ctx); err != nil {
				t.Fatal(err)
			}
			defer module.OnStop(context.Background())
			key := "origin:topology:{integration}:" + test.name
			if err = module.Set(ctx, key, "ok", time.Minute); err != nil {
				t.Fatal(err)
			}
			if value, getErr := module.Get(ctx, key); getErr != nil || value != "ok" {
				t.Fatalf("Get: %q %v", value, getErr)
			}
			if test.mode == ModeCluster {
				if _, _, scanErr := module.Scan(ctx, 0, "*", 10); !errors.Is(scanErr, ErrUnsupportedMode) {
					t.Fatalf("Cluster Scan: %v", scanErr)
				}
				if _, crossErr := module.Del(ctx, "a:{1}", "b:{2}"); !errors.Is(crossErr, ErrInvalidArgument) {
					t.Fatalf("cross slot: %v", crossErr)
				}
			}
		})
	}
}

func TestIntegrationSentinelSurvivesFailover(t *testing.T) {
	if os.Getenv("ORIGIN_REDIS_SENTINEL_FAILOVER") != "1" {
		t.Skip("ORIGIN_REDIS_SENTINEL_FAILOVER is not enabled")
	}
	addresses := strings.Split(strings.TrimSpace(os.Getenv("ORIGIN_REDIS_SENTINEL_ADDRESSES")), ",")
	masterName := strings.TrimSpace(os.Getenv("ORIGIN_REDIS_SENTINEL_MASTER"))
	if len(addresses) == 0 || addresses[0] == "" || masterName == "" {
		t.Fatal("Sentinel integration environment is incomplete")
	}

	current := Config{
		Mode: ModeSentinel, Addresses: addresses, PoolSize: 4,
		MaxActiveConnections: 4, MaxConcurrentDials: 4,
		Sentinel: SentinelConfig{MasterName: masterName},
	}
	module, err := New(current)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	if err = module.OnStart(ctx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = module.OnStop(context.Background()) })

	sentinel := redis.NewSentinelClient(&redis.Options{Addr: addresses[0]})
	t.Cleanup(func() { _ = sentinel.Close() })
	before, err := sentinel.GetMasterAddrByName(ctx, masterName).Result()
	if err != nil || len(before) != 2 {
		t.Fatalf("read master before failover: %v %v", before, err)
	}
	key := "origin:sentinel:{failover}:probe"
	if err = module.Set(ctx, key, "before", time.Minute); err != nil {
		t.Fatal(err)
	}
	if err = sentinel.Failover(ctx, masterName).Err(); err != nil {
		t.Fatal(err)
	}

	var after []string
	for ctx.Err() == nil {
		after, err = sentinel.GetMasterAddrByName(ctx, masterName).Result()
		if err == nil && len(after) == 2 && (after[0] != before[0] || after[1] != before[1]) {
			break
		}
		time.Sleep(200 * time.Millisecond)
	}
	if ctx.Err() != nil {
		t.Fatalf("wait for Sentinel master change: %v", ctx.Err())
	}

	for ctx.Err() == nil {
		err = module.Set(ctx, key, "after", time.Minute)
		if err == nil {
			var value string
			value, err = module.Get(ctx, key)
			if err == nil && value == "after" {
				return
			}
		}
		time.Sleep(200 * time.Millisecond)
	}
	t.Fatalf("Module did not recover after Sentinel failover: %v", err)
}

func TestIntegrationTLSAndACL(t *testing.T) {
	address := strings.TrimSpace(os.Getenv("ORIGIN_REDIS_SECURE_ADDRESS"))
	caFile := strings.TrimSpace(os.Getenv("ORIGIN_REDIS_SECURE_CA_FILE"))
	username := os.Getenv("ORIGIN_REDIS_SECURE_USERNAME")
	password := os.Getenv("ORIGIN_REDIS_SECURE_PASSWORD")
	if address == "" || caFile == "" || username == "" || password == "" {
		t.Skip("secure Redis integration environment is not configured")
	}
	current := Config{
		Mode: ModeStandalone, Addresses: []string{address}, Username: username, Password: password,
		TLS: true, TLSCAFile: caFile, PoolSize: 2, MaxActiveConnections: 2, MaxConcurrentDials: 2,
	}
	module, err := New(current)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err = module.OnStart(ctx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = module.OnStop(context.Background()) })
	if err = module.Set(ctx, "secure:{integration}:probe", "ok", time.Minute); err != nil {
		t.Fatal(err)
	}
	if value, getErr := module.Get(ctx, "secure:{integration}:probe"); getErr != nil || value != "ok" {
		t.Fatalf("secure Get: %q %v", value, getErr)
	}

	bad := current
	bad.Password += "-wrong"
	badModule, err := New(bad)
	if err != nil {
		t.Fatal(err)
	}
	if err = badModule.OnStart(ctx); err == nil {
		_ = badModule.OnStop(context.Background())
		t.Fatal("invalid ACL credential unexpectedly started")
	}
	if badModule.Client() != nil {
		t.Fatal("failed start published a Client")
	}
}
