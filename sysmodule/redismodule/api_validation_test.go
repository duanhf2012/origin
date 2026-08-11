package redismodule

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
)

func TestPublicArgumentValidation(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	module := &Module{config: Config{Mode: ModeStandalone}}
	invalid := []func() error{
		func() error { _, err := module.Del(ctx); return err },
		func() error { _, err := module.Unlink(ctx); return err },
		func() error { _, err := module.Exists(ctx); return err },
		func() error { _, err := module.Expire(ctx, "k", 0); return err },
		func() error { _, err := module.ExpireAt(ctx, "k", time.Time{}); return err },
		func() error { _, _, err := module.Scan(ctx, 0, "*", 0); return err },
		func() error { return module.Set(ctx, "k", "v", -time.Second) },
		func() error { _, err := module.SetNX(ctx, "k", "v", -time.Second); return err },
		func() error { _, err := module.SetXX(ctx, "k", "v", -time.Second); return err },
		func() error { _, err := module.GetEx(ctx, "k", 0); return err },
		func() error { _, err := module.MGet(ctx); return err },
		func() error { return module.MSet(ctx, nil) },
		func() error { _, err := module.MSetNX(ctx, nil); return err },
		func() error { _, err := module.HSetMany(ctx, "k", nil); return err },
		func() error { _, err := module.HMGet(ctx, "k"); return err },
		func() error { _, err := module.HDel(ctx, "k"); return err },
		func() error { _, _, err := module.HScan(ctx, "k", 0, "*", 0); return err },
		func() error { _, err := module.LPush(ctx, "k"); return err },
		func() error { _, err := module.LPushX(ctx, "k"); return err },
		func() error { _, err := module.RPush(ctx, "k"); return err },
		func() error { _, err := module.RPushX(ctx, "k"); return err },
		func() error { _, err := module.LPopN(ctx, "k", 0); return err },
		func() error { _, err := module.RPopN(ctx, "k", 0); return err },
		func() error { _, err := module.LMove(ctx, "a", "b", ListSide(9), ListLeft); return err },
		func() error { _, err := module.LMove(ctx, "a", "b", ListLeft, ListSide(9)); return err },
		func() error { _, err := module.SAdd(ctx, "k"); return err },
		func() error { _, err := module.SRem(ctx, "k"); return err },
		func() error { _, err := module.SMIsMember(ctx, "k"); return err },
		func() error { _, err := module.SPopN(ctx, "k", 0); return err },
		func() error { _, err := module.SRandMemberN(ctx, "k", 0); return err },
		func() error { _, err := module.SDiff(ctx); return err },
		func() error { _, err := module.SInter(ctx); return err },
		func() error { _, err := module.SUnion(ctx); return err },
		func() error { _, _, err := module.SScan(ctx, "k", 0, "*", 0); return err },
		func() error { _, err := module.ZAdd(ctx, "k"); return err },
		func() error { _, err := module.ZAddNX(ctx, "k", ScoredMember{Score: MaxExactScore + 1}); return err },
		func() error { _, err := module.ZAddXX(ctx, "k", ScoredMember{Score: MinExactScore - 1}); return err },
		func() error { _, err := module.ZIncrBy(ctx, "k", MaxExactScore+1, "m"); return err },
		func() error { _, err := module.ZRem(ctx, "k"); return err },
		func() error { _, err := module.ZRangeByScore(ctx, "k", 2, 1, 0, 1); return err },
		func() error { _, err := module.ZRangeByScore(ctx, "k", 1, 2, -1, 1); return err },
		func() error { _, err := module.ZRangeByScore(ctx, "k", 1, 2, 0, 0); return err },
		func() error { _, err := module.ZCount(ctx, "k", 2, 1); return err },
		func() error { _, err := module.ZRemRangeByScore(ctx, "k", 2, 1); return err },
		func() error { _, err := module.ZPopMin(ctx, "k", 0); return err },
		func() error { _, err := module.ZPopMax(ctx, "k", 0); return err },
		func() error { _, _, err := module.ZScan(ctx, "k", 0, "*", 0); return err },
		func() error { _, err := module.SetBit(ctx, "k", -1, true); return err },
		func() error { _, err := module.GetBit(ctx, "k", -1); return err },
		func() error { _, err := module.BitOpAnd(ctx, "d"); return err },
		func() error { _, err := module.Do(ctx); return err },
		func() error { return module.WithClient(ctx, nil) },
		func() error { _, err := module.Pipelined(ctx, nil); return err },
		func() error { _, err := module.TxPipelined(ctx, nil); return err },
		func() error { return module.Watch(ctx, nil, "k") },
		func() error { return module.Watch(ctx, func(context.Context, *redis.Tx) error { return nil }) },
		func() error { _, err := module.RunScript(ctx, nil, nil); return err },
		func() error { _, _, err := module.TryLock(ctx, "", time.Second); return err },
		func() error { _, _, err := module.TryLock(ctx, "k", 0); return err },
		func() error { _, err := module.Lock(ctx, "", time.Second, time.Second); return err },
		func() error { _, err := module.Lock(ctx, "k", 0, time.Second); return err },
		func() error { return module.WithLock(ctx, "k", time.Second, time.Second, nil) },
	}
	for index, call := range invalid {
		if err := call(); !errors.Is(err, ErrInvalidArgument) && !errors.Is(err, ErrInvalidScore) {
			t.Fatalf("case %d: expected argument error, got %v", index, err)
		}
	}
}

func TestNilLockValidation(t *testing.T) {
	t.Parallel()
	var lock *Lock
	if lock.Key() != "" {
		t.Fatal("nil lock key should be empty")
	}
	if _, err := lock.TTL(context.Background()); !errors.Is(err, ErrLockNotHeld) {
		t.Fatal(err)
	}
	if err := lock.Refresh(context.Background(), time.Second); !errors.Is(err, ErrLockNotHeld) {
		t.Fatal(err)
	}
	if err := lock.Release(context.Background()); !errors.Is(err, ErrLockNotHeld) {
		t.Fatal(err)
	}
	lock = &Lock{}
	if _, err := lock.TTL(nil); !errors.Is(err, ErrInvalidArgument) {
		t.Fatal(err)
	}
	if err := lock.Refresh(context.Background(), 0); !errors.Is(err, ErrInvalidArgument) {
		t.Fatal(err)
	}
	if err := lock.Release(nil); !errors.Is(err, ErrInvalidArgument) {
		t.Fatal(err)
	}
}
