package log_test

import (
	"math"
	"testing"

	"github.com/duanhf2012/origin/v3/errs"
	originlog "github.com/duanhf2012/origin/v3/log"
)

func TestDefaultConfig(t *testing.T) {
	t.Parallel()

	config := originlog.DefaultConfig()
	if err := config.Validate(); err != nil {
		t.Fatalf("DefaultConfig().Validate() = %v", err)
	}
	if config.Mode != originlog.AsyncMode {
		t.Fatalf("default mode = %v, want async", config.Mode)
	}
	if !config.Console.Enabled ||
		config.Console.Level != originlog.InfoLevel ||
		config.Console.Format != originlog.TextFormat {
		t.Fatalf("unexpected console defaults: %+v", config.Console)
	}
	if config.File.Enabled {
		t.Fatalf("file output is enabled by default")
	}
	if config.File.Path != "logs/origin.log" {
		t.Fatalf("default file path = %q", config.File.Path)
	}
}

func TestConfigValidation(t *testing.T) {
	t.Parallel()

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

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			var config originlog.Config
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

	level, ok := originlog.ParseLevel("WARN")
	if !ok || level != originlog.WarnLevel {
		t.Fatalf("ParseLevel(WARN) = %v, %v", level, ok)
	}
	if _, ok := originlog.ParseLevel("trace"); ok {
		t.Fatalf("ParseLevel(trace) unexpectedly succeeded")
	}
}
