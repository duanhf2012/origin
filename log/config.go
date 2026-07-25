package log

import (
	"fmt"
	"math"
	"time"

	"github.com/duanhf2012/origin/v3/errs"
)

const (
	eventQueueSize          = 10000
	reliableWriteTimeout    = time.Second
	defaultFileMaxSizeMB    = int64(512)
	defaultFileMaxAgeDays   = 14
	defaultFileMaxFileCount = 30
)

// Mode 控制日志调用是否等待日志协程完成写入。
type Mode uint8

const (
	ModeInvalid Mode = iota
	AsyncMode
	SyncMode
)

// Format 是内置日志输出格式。
type Format string

const (
	JSONFormat Format = "json"
	TextFormat Format = "text"
)

// Timezone 是按自然日滚动使用的时区。
type Timezone string

const (
	LocalTime Timezone = "Local"
	UTCTime   Timezone = "UTC"
)

// ConsoleConfig 配置控制台输出。
type ConsoleConfig struct {
	Enabled bool
	Level   Level
	Format  Format
}

// RotationConfig 配置文件滚动。
type RotationConfig struct {
	MaxSizeMB int64
	ByDate    bool
	Timezone  Timezone
}

// RetentionConfig 配置归档保留。
type RetentionConfig struct {
	MaxAgeDays int
	MaxFiles   int
	Compress   bool
}

// FileConfig 配置活动日志文件及其归档。
type FileConfig struct {
	Enabled   bool
	Level     Level
	Format    Format
	Path      string
	Rotation  RotationConfig
	Retention RetentionConfig
}

// Config 是日志 Runtime 和默认 Handler 的强类型配置。
type Config struct {
	Mode    Mode
	Console ConsoleConfig
	File    FileConfig
}

// DefaultConfig 返回可直接使用的默认配置。
func DefaultConfig() Config {
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
	if err := config.validateRuntime(); err != nil {
		return err
	}
	if !config.Console.Enabled && !config.File.Enabled {
		return invalidConfig("console and file outputs are both disabled")
	}
	if config.Console.Enabled {
		if !config.Console.Level.valid() {
			return invalidConfig("invalid console level")
		}
		if config.Console.Format == "" {
			return invalidConfig("console format is empty")
		}
	}
	if config.File.Enabled {
		if err := config.File.validate(); err != nil {
			return err
		}
	}
	return nil
}

func (config Config) validateRuntime() error {
	if config.Mode != AsyncMode && config.Mode != SyncMode {
		return invalidConfig("invalid log mode")
	}
	return nil
}

func (config FileConfig) validate() error {
	if !config.Level.valid() {
		return invalidConfig("invalid file level")
	}
	if config.Format == "" {
		return invalidConfig("file format is empty")
	}
	if config.Path == "" {
		return invalidConfig("file path is empty")
	}
	if config.Rotation.MaxSizeMB < 0 {
		return invalidConfig("file max size is negative")
	}
	if config.Rotation.MaxSizeMB > math.MaxInt64/(1024*1024) {
		return invalidConfig("file max size is too large")
	}
	if config.Rotation.Timezone != LocalTime && config.Rotation.Timezone != UTCTime {
		return invalidConfig("file timezone must be Local or UTC")
	}
	if config.Retention.MaxAgeDays < 0 {
		return invalidConfig("file max age is negative")
	}
	if int64(config.Retention.MaxAgeDays) > int64(math.MaxInt64)/(int64(24*time.Hour)) {
		return invalidConfig("file max age is too large")
	}
	if config.Retention.MaxFiles < 0 {
		return invalidConfig("file max count is negative")
	}
	return nil
}

func invalidConfig(message string) error {
	return errs.Wrap(errs.CodeInvalidConfig, fmt.Errorf("%s", message))
}
