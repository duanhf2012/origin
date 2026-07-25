package zaplog

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/duanhf2012/origin/v3/errs"
	originlog "github.com/duanhf2012/origin/v3/log"
	"go.uber.org/zap/zapcore"
)

func TestConsoleLevelsAreExclusive(t *testing.T) {
	t.Parallel()

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	runtime := newTestRuntime(t, originlog.DefaultConfig(), &stdout, &stderr)

	logger := runtime.Logger()
	logger.Debug("hidden")
	logger.Info("information")
	logger.Error("failure")
	if err := runtime.Close(context.Background()); err != nil {
		t.Fatalf("Close() = %v", err)
	}

	if !strings.Contains(stdout.String(), "information") ||
		strings.Contains(stdout.String(), "failure") ||
		strings.Contains(stdout.String(), "hidden") {
		t.Fatalf("unexpected stdout: %q", stdout.String())
	}
	if !strings.Contains(stderr.String(), "failure") ||
		strings.Contains(stderr.String(), "information") {
		t.Fatalf("unexpected stderr: %q", stderr.String())
	}
}

func TestJSONFieldsAndCaller(t *testing.T) {
	t.Parallel()

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	config := originlog.DefaultConfig()
	config.Console.Format = originlog.JSONFormat
	runtime := newTestRuntime(t, config, &stdout, &stderr)

	runtime.Logger().Info(
		"player",
		originlog.Int64("player_id", 7),
		originlog.Any("profile", map[string]any{"level": 9}),
	)
	if err := runtime.Close(context.Background()); err != nil {
		t.Fatalf("Close() = %v", err)
	}

	var value map[string]any
	if err := json.Unmarshal(bytes.TrimSpace(stdout.Bytes()), &value); err != nil {
		t.Fatalf("decode JSON: %v\n%s", err, stdout.String())
	}
	if value["msg"] != "player" || value["player_id"] != float64(7) {
		t.Fatalf("unexpected JSON fields: %#v", value)
	}
	profile, ok := value["profile"].(map[string]any)
	if !ok || profile["level"] != float64(9) {
		t.Fatalf("Any field is not a JSON object: %#v", value["profile"])
	}
	if caller, _ := value["caller"].(string); !strings.Contains(caller, "zaplog/handler_test.go:") {
		t.Fatalf("caller = %#v", value["caller"])
	}
}

func TestFileAndConsoleLevelsAreIndependent(t *testing.T) {
	t.Parallel()

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	path := filepath.Join(t.TempDir(), "origin.log")
	config := originlog.DefaultConfig()
	config.File.Enabled = true
	config.File.Path = path
	config.File.Format = originlog.JSONFormat
	runtime := newTestRuntime(t, config, &stdout, &stderr)

	runtime.Logger().Debug("debug-file")
	runtime.Logger().Info("info-both")
	if err := runtime.Close(context.Background()); err != nil {
		t.Fatalf("Close() = %v", err)
	}

	if strings.Contains(stdout.String(), "debug-file") ||
		!strings.Contains(stdout.String(), "info-both") {
		t.Fatalf("unexpected stdout: %s", stdout.String())
	}
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile() = %v", err)
	}
	if !strings.Contains(string(content), "debug-file") ||
		!strings.Contains(string(content), "info-both") {
		t.Fatalf("unexpected file: %s", content)
	}
}

func TestConsoleAndFileFormatCombinations(t *testing.T) {
	t.Parallel()

	formats := []originlog.Format{originlog.TextFormat, originlog.JSONFormat}
	for _, consoleFormat := range formats {
		for _, fileFormat := range formats {
			name := string(consoleFormat) + "-" + string(fileFormat)
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				var stdout bytes.Buffer
				var stderr bytes.Buffer
				path := filepath.Join(t.TempDir(), "origin.log")
				config := originlog.DefaultConfig()
				config.Console.Format = consoleFormat
				config.File.Enabled = true
				config.File.Path = path
				config.File.Format = fileFormat
				config.File.Retention.Compress = false
				runtime := newTestRuntime(t, config, &stdout, &stderr)
				runtime.Logger().Info("combination")
				if err := runtime.Close(context.Background()); err != nil {
					t.Fatalf("Close() = %v", err)
				}

				assertFormat(t, consoleFormat, stdout.Bytes())
				content, err := os.ReadFile(path)
				if err != nil {
					t.Fatalf("ReadFile() = %v", err)
				}
				assertFormat(t, fileFormat, content)
			})
		}
	}
}

func TestCustomEncoderIsInstanceScoped(t *testing.T) {
	t.Parallel()

	var calls atomic.Uint64
	factory := func(config zapcore.EncoderConfig) (zapcore.Encoder, error) {
		calls.Add(1)
		return zapcore.NewJSONEncoder(config), nil
	}

	for range 2 {
		var stdout bytes.Buffer
		var stderr bytes.Buffer
		config := originlog.DefaultConfig()
		config.Console.Format = "ecs"
		runtime, err := New(
			config,
			WithEncoder("ecs", factory),
			withConsoleWriters(&stdout, &stderr),
		)
		if err != nil {
			t.Fatalf("New() = %v", err)
		}
		runtime.Logger().Info("custom")
		if err := runtime.Close(context.Background()); err != nil {
			t.Fatalf("Close() = %v", err)
		}
	}
	if calls.Load() != 4 {
		t.Fatalf("factory calls = %d, want 4", calls.Load())
	}

	config := originlog.DefaultConfig()
	config.Console.Format = "ecs"
	if _, err := NewHandler(config); !errs.IsCode(err, errs.CodeInvalidConfig) {
		t.Fatalf("unregistered encoder error = %v", err)
	}
}

func TestEncoderValidation(t *testing.T) {
	t.Parallel()

	factory := func(config zapcore.EncoderConfig) (zapcore.Encoder, error) {
		return zapcore.NewJSONEncoder(config), nil
	}
	failedFactory := func(zapcore.EncoderConfig) (zapcore.Encoder, error) {
		return nil, errors.New("factory failed")
	}
	tests := []struct {
		name    string
		options []Option
		format  originlog.Format
	}{
		{name: "reserved", options: []Option{WithEncoder("json", factory)}},
		{name: "nil factory", options: []Option{WithEncoder("ecs", nil)}},
		{
			name:    "duplicate",
			options: []Option{WithEncoder("ecs", factory), WithEncoder("ecs", factory)},
		},
		{name: "factory error", options: []Option{WithEncoder("ecs", failedFactory)}, format: "ecs"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			config := originlog.DefaultConfig()
			if test.format != "" {
				config.Console.Format = test.format
			}
			if _, err := NewHandler(config, test.options...); !errs.IsCode(err, errs.CodeInvalidConfig) {
				t.Fatalf("NewHandler() error = %v, want CodeInvalidConfig", err)
			}
		})
	}
}

func TestOutputOpenFailure(t *testing.T) {
	t.Parallel()

	config := originlog.DefaultConfig()
	config.Console.Enabled = false
	config.File.Enabled = true
	config.File.Path = t.TempDir()
	if _, err := NewHandler(config); !errs.IsCode(err, errs.CodeLogOutputFailed) {
		t.Fatalf("NewHandler() error = %v, want CodeLogOutputFailed", err)
	}
}

func newTestRuntime(
	t *testing.T,
	config originlog.Config,
	stdout, stderr *bytes.Buffer,
) *originlog.Runtime {
	t.Helper()
	runtime, err := New(config, withConsoleWriters(stdout, stderr))
	if err != nil {
		t.Fatalf("New() = %v", err)
	}
	return runtime
}

func assertFormat(t *testing.T, format originlog.Format, content []byte) {
	t.Helper()
	if format == originlog.JSONFormat {
		var value map[string]any
		if err := json.Unmarshal(bytes.TrimSpace(content), &value); err != nil {
			t.Fatalf("invalid JSON output: %v\n%s", err, content)
		}
		if value["msg"] != "combination" {
			t.Fatalf("unexpected JSON output: %#v", value)
		}
		return
	}
	if !strings.Contains(string(content), "INFO") ||
		!strings.Contains(string(content), "combination") {
		t.Fatalf("unexpected Text output: %s", content)
	}
}

func BenchmarkJSONEncoding(b *testing.B) {
	config := originlog.DefaultConfig()
	config.Console.Format = originlog.JSONFormat
	handler, err := NewHandler(config, withConsoleWriters(io.Discard, io.Discard))
	if err != nil {
		b.Fatal(err)
	}
	defer handler.Close()
	record := originlog.Record{
		Time:    time.Now(),
		Level:   originlog.InfoLevel,
		Message: "message",
		Caller:  originlog.Caller{File: "service/player.go", Line: 10},
	}
	fields := []originlog.Field{
		originlog.Int64("player_id", 7),
		originlog.Bool("online", true),
	}

	b.ReportAllocs()
	for b.Loop() {
		if err := handler.Write(record, fields); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkTextEncoding(b *testing.B) {
	config := originlog.DefaultConfig()
	handler, err := NewHandler(config, withConsoleWriters(io.Discard, io.Discard))
	if err != nil {
		b.Fatal(err)
	}
	defer handler.Close()
	record := originlog.Record{
		Time:    time.Now(),
		Level:   originlog.InfoLevel,
		Message: "message",
		Caller:  originlog.Caller{File: "service/player.go", Line: 10},
	}
	fields := []originlog.Field{
		originlog.Int64("player_id", 7),
		originlog.Bool("online", true),
	}

	b.ReportAllocs()
	for b.Loop() {
		if err := handler.Write(record, fields); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkFieldConversion(b *testing.B) {
	fields := []originlog.Field{
		originlog.String("service", "PlayerService"),
		originlog.Int64("player_id", 7),
		originlog.Bool("online", true),
		originlog.Duration("latency", time.Millisecond),
	}
	converted := make([]zapcore.Field, len(fields))

	b.ReportAllocs()
	for b.Loop() {
		for index, field := range fields {
			converted[index] = toZapField(field)
		}
	}
}
