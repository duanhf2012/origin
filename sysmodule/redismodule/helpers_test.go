package redismodule

import (
	"errors"
	"math"
	"testing"
	"time"
)

func TestOptionalStrings(t *testing.T) {
	t.Parallel()
	values, err := optionalStrings([]any{"", nil, "value"})
	if err != nil {
		t.Fatal(err)
	}
	if !values[0].Exists || values[0].Value != "" || values[1].Exists || values[2].Value != "value" {
		t.Fatalf("unexpected values: %+v", values)
	}
	if _, err = optionalStrings([]any{int64(1)}); err == nil {
		t.Fatal("unexpected type should fail")
	}
}

func TestExactScores(t *testing.T) {
	t.Parallel()
	for _, value := range []float64{float64(MinExactScore), -1, 0, 1, float64(MaxExactScore)} {
		if _, err := exactScore(value); err != nil {
			t.Fatalf("valid score %v: %v", value, err)
		}
	}
	for _, value := range []float64{math.NaN(), math.Inf(1), 1.5, float64(MaxExactScore) * 2} {
		if _, err := exactScore(value); !errors.Is(err, ErrInvalidScore) {
			t.Fatalf("invalid score %v: %v", value, err)
		}
	}
	if err := validateScore(MaxExactScore + 1); !errors.Is(err, ErrInvalidScore) {
		t.Fatal("integer overflow should fail")
	}
}

func TestRedisSlotHashTags(t *testing.T) {
	t.Parallel()
	if redisSlot("player:{1001}:profile") != redisSlot("player:{1001}:session") {
		t.Fatal("same hash tag should share slot")
	}
	if hashTag("a{}b") != "a{}b" || hashTag("plain") != "plain" || hashTag("a{tag}b") != "tag" {
		t.Fatal("hash tag parsing mismatch")
	}
	module := &Module{}
	module.cluster.Store(true)
	if err := module.validateSameSlot([]string{"a:{1}", "b:{1}"}); err != nil {
		t.Fatal(err)
	}
	if err := module.validateSameSlot([]string{"a:{1}", "b:{2}"}); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("expected cross-slot rejection: %v", err)
	}
}

func TestJitterRetryBounds(t *testing.T) {
	t.Parallel()
	strategy := &jitterRetry{state: 1}
	for range 100 {
		value := strategy.NextBackoff()
		if value < 40*time.Millisecond || value > 60*time.Millisecond {
			t.Fatalf("out of range: %s", value)
		}
	}
}
