package redismodule

import (
	"errors"
	"time"

	"github.com/bsm/redislock"
	"github.com/duanhf2012/origin/v3/errs"
	"github.com/redis/go-redis/v9"
)

var (
	// ErrNotSetup 表示 Module 尚未通过 New 或 Setup 冻结配置。
	ErrNotSetup = errs.NewMessage(errs.CodeInvalidConfig, "redismodule 尚未完成配置")
	// ErrAlreadySetup 表示同一 Module 已经冻结配置，不能再次 Setup。
	ErrAlreadySetup = errs.NewMessage(errs.CodeInvalidArgument, "redismodule 只能配置一次")
	// ErrNotRunning 表示 Module 尚未启动、启动失败、正在停止或已经停止。
	ErrNotRunning = errs.NewMessage(errs.CodeServiceNotReady, "redismodule 尚未运行")
	// ErrInvalidConfig 表示 Redis 配置无效；可通过 errors.Is 与 errs.ErrInvalidConfig 匹配。
	ErrInvalidConfig = errs.ErrInvalidConfig
	// ErrInvalidArgument 表示方法参数无效；可通过 errors.Is 与 errs.ErrInvalidArgument 匹配。
	ErrInvalidArgument = errs.ErrInvalidArgument
	// ErrUnsupportedMode 表示当前 Redis 拓扑不支持所调用的便利能力。
	ErrUnsupportedMode = errors.New("redismodule: unsupported mode")
	// ErrNil 表示单值读取的 Key、Field、Member 或元素不存在。
	ErrNil = redis.Nil
	// ErrInvalidScore 表示 Sorted Set Score 不是 Redis 双精度格式可精确表达的整数。
	ErrInvalidScore = errors.New("redismodule: invalid integer score")
	// ErrLockNotObtained 表示在给定等待边界内没有获得 Lease Lock。
	ErrLockNotObtained = redislock.ErrNotObtained
	// ErrLockNotHeld 表示 Lease 已过期、所有权改变或已经释放。
	ErrLockNotHeld = redislock.ErrLockNotHeld
)

const (
	// TTLNoExpiration 是 TTL 对存在但没有过期时间的 Key 返回的特殊值。
	TTLNoExpiration = -1 * time.Second
	// TTLKeyNotFound 是 TTL 对不存在 Key 返回的特殊值。
	TTLKeyNotFound = -2 * time.Second
	// PTTLNoExpiration 是 PTTL 对存在但没有过期时间的 Key 返回的特殊值。
	PTTLNoExpiration = -1 * time.Millisecond
	// PTTLKeyNotFound 是 PTTL 对不存在 Key 返回的特殊值。
	PTTLKeyNotFound = -2 * time.Millisecond
	// MinExactScore 是便利层允许的最小精确整数 Score。
	MinExactScore int64 = -(1 << 53)
	// MaxExactScore 是便利层允许的最大精确整数 Score。
	MaxExactScore int64 = 1 << 53
)

// ListSide 表示 List 原子移动操作使用的左端或右端。
type ListSide uint8

const (
	// ListLeft 表示 List 左端。
	ListLeft ListSide = iota
	// ListRight 表示 List 右端。
	ListRight
)

// ScoredMember 是只接受精确 int64 Score 的 Sorted Set 成员。
type ScoredMember struct {
	// Member 是 Sorted Set 成员字符串。
	Member string
	// Score 是范围在 MinExactScore 到 MaxExactScore 之间的整数分数。
	Score int64
}

// OptionalString 表示批量读取中一个与输入位置对应的可选字符串。
//
// Exists 为 false 时 Value 必须为空；Exists 为 true 且 Value 为空表示 Redis 中真实保存了空字符串。
type OptionalString struct {
	// Value 是存在项的字符串值。
	Value string
	// Exists 报告输入位置对应的 Key 或 Field 是否存在。
	Exists bool
}
