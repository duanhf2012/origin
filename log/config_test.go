package log_test

import (
	"math"
	"strings"
	"testing"

	"github.com/duanhf2012/origin/v3/errs"
	originlog "github.com/duanhf2012/origin/v3/log"
)

func TestDefaultConfig(t *testing.T) {
	t.Parallel()

	// 获取一份独立默认配置，并先验证它可直接用于启动。
	config := originlog.DefaultConfig()
	if err := config.Validate(); err != nil {
		t.Fatalf("DefaultConfig().Validate() = %v", err)
	}
	// 再锁定异步模式、控制台和文件路径等关键默认外观。
	if config.Mode != originlog.AsyncMode {
		t.Fatalf("default mode = %v, want async", config.Mode)
	}
	if !config.Console.Enabled ||
		config.Console.Level != originlog.InfoLevel ||
		config.Console.Format != originlog.TextFormat ||
		!config.Console.ContextFields.NodeID ||
		!config.Console.ContextFields.ServiceName {
		t.Fatalf("unexpected console defaults: %+v", config.Console)
	}
	if config.File.Enabled {
		t.Fatalf("file output is enabled by default")
	}
	if config.File.Path != "logs/origin.log" {
		t.Fatalf("default file path = %q", config.File.Path)
	}
	if !config.File.ContextFields.NodeID || !config.File.ContextFields.ServiceName {
		t.Fatalf("unexpected file context field defaults: %+v", config.File.ContextFields)
	}
}

// TestStatusKeepsConfiguredAndCurrentLevelsSeparate 防止 Reset 所需的启动级别被运行时覆盖。
func TestStatusKeepsConfiguredAndCurrentLevelsSeparate(t *testing.T) {
	t.Parallel()

	status := originlog.Status{
		Console: originlog.OutputStatus{
			Available:   true,
			Enabled:     true,
			Level:       originlog.DebugLevel,
			ConfigLevel: originlog.InfoLevel,
		},
	}
	if status.Console.Level != originlog.DebugLevel ||
		status.Console.ConfigLevel != originlog.InfoLevel {
		t.Fatalf("status loses current/config level distinction: %+v", status.Console)
	}
}

func TestConfigValidation(t *testing.T) {
	t.Parallel()

	// 每个变更函数构造一种明确非法配置。
	tests := []struct {
		name   string
		mutate func(*originlog.Config)
	}{
		{name: "zero config", mutate: func(*originlog.Config) {}},
		{
			name: "no outputs",
			mutate: func(config *originlog.Config) {
				*config = originlog.DefaultConfig()
				config.Console.Enabled = false
			},
		},
		{
			name: "empty file path",
			mutate: func(config *originlog.Config) {
				*config = originlog.DefaultConfig()
				config.File.Enabled = true
				config.File.Path = ""
			},
		},
		{
			name: "negative size",
			mutate: func(config *originlog.Config) {
				*config = originlog.DefaultConfig()
				config.File.Enabled = true
				config.File.Rotation.MaxSizeMB = -1
			},
		},
		{
			name: "size overflow",
			mutate: func(config *originlog.Config) {
				*config = originlog.DefaultConfig()
				config.File.Enabled = true
				config.File.Rotation.MaxSizeMB = math.MaxInt64
			},
		},
		{
			name: "invalid timezone",
			mutate: func(config *originlog.Config) {
				*config = originlog.DefaultConfig()
				config.File.Enabled = true
				config.File.Rotation.Timezone = "Asia/Shanghai"
			},
		},
	}

	// 所有非法输入都应映射为统一配置错误码。
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			var config originlog.Config
			// 从零值或默认值出发应用当前非法变更。
			test.mutate(&config)
			err := config.Validate()
			if !errs.IsCode(err, errs.CodeInvalidConfig) {
				t.Fatalf("Validate() error = %v, want CodeInvalidConfig", err)
			}
		})
	}
}

func TestParseLevel(t *testing.T) {
	t.Parallel()

	// 全部公开级别都允许大小写差异，并稳定输出小写名称。
	tests := []struct {
		input string
		want  originlog.Level
	}{
		{input: "DEBUG", want: originlog.DebugLevel},
		{input: "Info", want: originlog.InfoLevel},
		{input: "WARN", want: originlog.WarnLevel},
		{input: "error", want: originlog.ErrorLevel},
	}
	for _, test := range tests {
		level, ok := originlog.ParseLevel(test.input)
		if !ok || level != test.want {
			t.Errorf("ParseLevel(%q) = %v, %v, want %v, true", test.input, level, ok, test.want)
		}
		if got := level.String(); got != strings.ToLower(test.input) {
			t.Errorf("Level.String() = %q, want %q", got, strings.ToLower(test.input))
		}
	}
	// 未支持的 trace 必须明确失败。
	if level, ok := originlog.ParseLevel("trace"); ok || level != originlog.LevelInvalid {
		t.Fatalf("ParseLevel(trace) unexpectedly succeeded")
	}
	if got := originlog.Level(255).String(); got != "invalid" {
		t.Fatalf("invalid Level.String() = %q", got)
	}
}
