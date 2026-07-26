package log

import (
	"fmt"
	"math"
	"time"

	"github.com/duanhf2012/origin/v3/errs"
)

const (
	// eventQueueSize 固定异步日志队列容量；M1 不暴露容易误解的队列配置。
	eventQueueSize = 10000
	// reliableWriteTimeout 限制 ErrorStack 等可靠日志最多阻塞调用方一秒。
	reliableWriteTimeout = time.Second
	// 以下值只用于生成完整默认配置，不在校验阶段静默填充。
	defaultFileMaxSizeMB    = int64(512)
	defaultFileMaxAgeDays   = 14
	defaultFileMaxFileCount = 30
)

// Mode 控制日志调用是否等待日志协程完成写入。
type Mode uint8

const (
	// ModeInvalid 是零值占位，校验阶段会拒绝。
	ModeInvalid Mode = iota
	// AsyncMode 在队列满时丢弃普通日志，避免阻塞业务协程。
	AsyncMode
	// SyncMode 等待队列空间和单条日志写完，便于开发期即时观察。
	SyncMode
)

// Format 是内置日志输出格式。
type Format string

const (
	// JSONFormat 输出适合日志平台采集的单行 JSON。
	JSONFormat Format = "json"
	// TextFormat 输出适合终端阅读的文本格式。
	TextFormat Format = "text"
)

// Timezone 是按自然日滚动使用的时区。
type Timezone string

const (
	// LocalTime 按操作系统本地时区判断自然日边界。
	LocalTime Timezone = "Local"
	// UTCTime 按 UTC 判断自然日边界。
	UTCTime Timezone = "UTC"
)

// ConsoleConfig 配置控制台输出。
type ConsoleConfig struct {
	// Enabled 控制是否建立控制台输出 Core。
	Enabled bool
	// Level 是控制台接收的最低日志级别。
	Level Level
	// Format 选择内置 text 或 json Encoder。
	Format Format
}

// RotationConfig 配置文件滚动。
type RotationConfig struct {
	// MaxSizeMB 大于零时按活动文件大小滚动，零表示关闭大小滚动。
	MaxSizeMB int64
	// ByDate 控制是否在自然日变化时滚动。
	ByDate bool
	// Timezone 只影响日期边界和归档文件时间。
	Timezone Timezone
}

// RetentionConfig 配置归档保留。
type RetentionConfig struct {
	// MaxAgeDays 大于零时删除超过自然日数的归档，零表示不限时间。
	MaxAgeDays int
	// MaxFiles 大于零时只保留最新的指定数量归档，零表示不限数量。
	MaxFiles int
	// Compress 控制维护协程是否把旧归档压缩为 gzip。
	Compress bool
}

// FileConfig 配置活动日志文件及其归档。
type FileConfig struct {
	// Enabled 控制是否建立文件输出 Core。
	Enabled bool
	// Level 是文件接收的最低日志级别。
	Level Level
	// Format 选择内置或自定义 Encoder。
	Format Format
	// Path 是当前活动日志文件路径，保留 .log 扩展名。
	Path string
	// Rotation 定义触发新归档的条件。
	Rotation RotationConfig
	// Retention 定义异步归档清理和压缩规则。
	Retention RetentionConfig
}

// Config 是日志 Runtime 和默认 Handler 的强类型配置。
type Config struct {
	// Mode 决定普通日志调用是否等待日志协程。
	Mode Mode
	// Console 和 File 可以使用不同级别与格式。
	Console ConsoleConfig
	File    FileConfig
}

// DefaultConfig 返回可直接使用的默认配置。
func DefaultConfig() Config {
	// 每次返回独立值，调用方可以修改而不会污染其他 Runtime。
	return Config{
		Mode: AsyncMode,
		Console: ConsoleConfig{
			Enabled: true,
			Level:   InfoLevel,
			Format:  TextFormat,
		},
		File: FileConfig{
			Enabled: false,
			Level:   DebugLevel,
			Format:  TextFormat,
			Path:    "logs/origin.log",
			Rotation: RotationConfig{
				MaxSizeMB: defaultFileMaxSizeMB,
				ByDate:    true,
				Timezone:  LocalTime,
			},
			Retention: RetentionConfig{
				MaxAgeDays: defaultFileMaxAgeDays,
				MaxFiles:   defaultFileMaxFileCount,
				Compress:   true,
			},
		},
	}
}

// Validate 校验默认 Zap Handler 使用的完整配置。
func (config Config) Validate() error {
	// 先校验核心 Runtime 需要的模式，避免 Handler 创建后才失败。
	if err := config.validateRuntime(); err != nil {
		return err
	}
	// 默认 Handler 至少需要一个输出端，否则日志会被静默吞掉。
	if !config.Console.Enabled && !config.File.Enabled {
		return invalidConfig("console and file outputs are both disabled")
	}
	// 只校验已经开启的控制台，关闭时允许其余字段保留默认值。
	if config.Console.Enabled {
		if !config.Console.Level.valid() {
			return invalidConfig("invalid console level")
		}
		if config.Console.Format == "" {
			return invalidConfig("console format is empty")
		}
	}
	// 文件输出包含滚动和保留边界，交给 FileConfig 集中校验。
	if config.File.Enabled {
		if err := config.File.validate(); err != nil {
			return err
		}
	}
	// 所有启用输出均合法后，配置才可以用于创建 Handler。
	return nil
}

// validateRuntime 只校验日志核心使用的字段，允许自定义 Handler 跳过输出配置。
func (config Config) validateRuntime() error {
	// Runtime 只支持明确的同步或异步模式，拒绝零值和未知扩展值。
	if config.Mode != AsyncMode && config.Mode != SyncMode {
		return invalidConfig("invalid log mode")
	}
	return nil
}

// validate 校验文件输出、滚动和保留配置的范围。
func (config FileConfig) validate() error {
	// 先校验直接决定输出行为的级别、格式和活动文件路径。
	if !config.Level.valid() {
		return invalidConfig("invalid file level")
	}
	if config.Format == "" {
		return invalidConfig("file format is empty")
	}
	if config.Path == "" {
		return invalidConfig("file path is empty")
	}
	// 大小滚动允许用零关闭，但不能为负数或在换算字节时溢出。
	if config.Rotation.MaxSizeMB < 0 {
		return invalidConfig("file max size is negative")
	}
	if config.Rotation.MaxSizeMB > math.MaxInt64/(1024*1024) {
		return invalidConfig("file max size is too large")
	}
	// 即使关闭日期滚动也要求时区合法，使配置含义始终确定。
	if config.Rotation.Timezone != LocalTime && config.Rotation.Timezone != UTCTime {
		return invalidConfig("file timezone must be Local or UTC")
	}
	// 保留天数允许用零表示不限，并提前验证转为 time.Duration 的边界。
	if config.Retention.MaxAgeDays < 0 {
		return invalidConfig("file max age is negative")
	}
	if int64(config.Retention.MaxAgeDays) > int64(math.MaxInt64)/(int64(24*time.Hour)) {
		return invalidConfig("file max age is too large")
	}
	// 归档数量同样以零表示不限，负数没有有效语义。
	if config.Retention.MaxFiles < 0 {
		return invalidConfig("file max count is negative")
	}
	return nil
}

// invalidConfig 把日志配置错误统一映射为 Origin 配置错误码。
func invalidConfig(message string) error {
	// 保留 cause 形态，使调用方可以统一使用 errs.CodeOf 判断。
	return errs.Wrap(errs.CodeInvalidConfig, fmt.Errorf("%s", message))
}
