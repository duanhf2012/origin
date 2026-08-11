package redismodule

import (
	"fmt"
	"math"
	"strconv"

	"github.com/duanhf2012/origin/v3/errs"
	"github.com/redis/go-redis/v9"
)

func invalidArgument(message string) error { return errs.NewMessage(errs.CodeInvalidArgument, message) }

func requireValues(length int, name string) error {
	if length == 0 {
		return invalidArgument("redismodule " + name + " 不能为空")
	}
	return nil
}

func (module *Module) validateSameSlot(keys []string) error {
	if !module.isClusterMode() || len(keys) < 2 {
		return nil
	}
	slot := redisSlot(keys[0])
	for _, key := range keys[1:] {
		if redisSlot(key) != slot {
			return invalidArgument("redismodule Cluster 多 Key 必须位于同一 Slot")
		}
	}
	return nil
}

func positiveCountAsInt(count int64, name string) (int, error) {
	if count <= 0 || uint64(count) > uint64(^uint(0)>>1) {
		return 0, invalidArgument("redismodule " + name + " Count 超出有效范围")
	}
	return int(count), nil
}

func redisSlot(key string) uint16 {
	data := []byte(hashTag(key))
	var crc uint16
	for _, value := range data {
		crc ^= uint16(value) << 8
		for range 8 {
			if crc&0x8000 != 0 {
				crc = crc<<1 ^ 0x1021
			} else {
				crc <<= 1
			}
		}
	}
	return crc % 16384
}

func hashTag(key string) string {
	start := -1
	for index := 0; index < len(key); index++ {
		if key[index] == '{' {
			start = index + 1
			break
		}
	}
	if start < 0 || start >= len(key) {
		return key
	}
	for index := start; index < len(key); index++ {
		if key[index] == '}' {
			if index > start {
				return key[start:index]
			}
			return key
		}
	}
	return key
}

func optionalStrings(values []any) ([]OptionalString, error) {
	result := make([]OptionalString, len(values))
	for index, value := range values {
		if value == nil {
			continue
		}
		text, ok := value.(string)
		if !ok {
			return nil, fmt.Errorf("redismodule: unexpected bulk string type %T", value)
		}
		result[index] = OptionalString{Value: text, Exists: true}
	}
	return result, nil
}

func redisMembers(members []ScoredMember) ([]redis.Z, error) {
	if err := requireValues(len(members), "Sorted Set Members"); err != nil {
		return nil, err
	}
	result := make([]redis.Z, len(members))
	for index, member := range members {
		if err := validateScore(member.Score); err != nil {
			return nil, err
		}
		result[index] = redis.Z{Score: float64(member.Score), Member: member.Member}
	}
	return result, nil
}

func scoredMembers(values []redis.Z) ([]ScoredMember, error) {
	result := make([]ScoredMember, len(values))
	for index, value := range values {
		score, err := exactScore(value.Score)
		if err != nil {
			return nil, err
		}
		member, ok := value.Member.(string)
		if !ok {
			return nil, fmt.Errorf("redismodule: unexpected sorted set member type %T", value.Member)
		}
		result[index] = ScoredMember{Member: member, Score: score}
	}
	return result, nil
}

func scanScoredMembers(values []string) ([]ScoredMember, error) {
	if len(values)%2 != 0 {
		return nil, fmt.Errorf("redismodule: malformed ZSCAN result")
	}
	result := make([]ScoredMember, 0, len(values)/2)
	for index := 0; index < len(values); index += 2 {
		scoreValue, err := strconv.ParseFloat(values[index+1], 64)
		if err != nil {
			return nil, fmt.Errorf("redismodule: parse sorted set score: %w", err)
		}
		score, err := exactScore(scoreValue)
		if err != nil {
			return nil, err
		}
		result = append(result, ScoredMember{Member: values[index], Score: score})
	}
	return result, nil
}

func exactScore(score float64) (int64, error) {
	if math.IsNaN(score) || math.IsInf(score, 0) || math.Trunc(score) != score ||
		score < float64(MinExactScore) || score > float64(MaxExactScore) {
		return 0, ErrInvalidScore
	}
	return int64(score), nil
}

func validateScore(score int64) error {
	if score < MinExactScore || score > MaxExactScore {
		return ErrInvalidScore
	}
	return nil
}
