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

	// 用两个独立 Buffer 替换 stdout/stderr，避免污染测试进程控制台。
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	runtime := newTestRuntime(t, originlog.DefaultConfig(), &stdout, &stderr)

	// Debug 低于默认阈值，Info 应进入 stdout，Error 应进入 stderr。
	logger := runtime.Logger()
	logger.Debug("hidden")
	logger.Info("information")
	logger.Error("failure")
	if err := runtime.Close(context.Background()); err != nil {
		t.Fatalf("Close() = %v", err)
	}

	// 两个控制台 Core 的级别区间必须互斥且完整。
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

	// 把默认控制台改为 JSON，并捕获输出。
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	config := originlog.DefaultConfig()
	config.Console.Format = originlog.JSONFormat
	runtime := newTestRuntime(t, config, &stdout, &stderr)

	// 写入基础字段和 Any 对象字段，然后关闭以排空。
	runtime.Logger().Info(
		"player",
		originlog.Int64("player_id", 7),
		originlog.Any("profile", map[string]any{"level": 9}),
	)
	if err := runtime.Close(context.Background()); err != nil {
		t.Fatalf("Close() = %v", err)
	}

	// 把单行 JSON 解码为通用 Map，验证核心字段。
	var value map[string]any
	if err := json.Unmarshal(bytes.TrimSpace(stdout.Bytes()), &value); err != nil {
		t.Fatalf("decode JSON: %v\n%s", err, stdout.String())
	}
	if value["msg"] != "player" || value["player_id"] != float64(7) {
		t.Fatalf("unexpected JSON fields: %#v", value)
	}
	// Any 字段必须作为嵌套 JSON 对象，而不是转义字符串。
	profile, ok := value["profile"].(map[string]any)
	if !ok || profile["level"] != float64(9) {
		t.Fatalf("Any field is not a JSON object: %#v", value["profile"])
	}
	// Caller 应指向真实测试调用位置。
	if caller, _ := value["caller"].(string); !strings.Contains(caller, "zaplog/handler_test.go:") {
		t.Fatalf("caller = %#v", value["caller"])
	}
}

// TestTextFormatUsesReadableScopeAndKeyValues 防止文本输出回退为 JSON 尾部、显示数值时区，
// 或把多行字符串拆成多条物理日志。
func TestTextFormatUsesReadableScopeAndKeyValues(t *testing.T) {
	t.Parallel()

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	runtime := newTestRuntime(t, originlog.DefaultConfig(), &stdout, &stderr)
	runtime.Logger().WithScope("game-1", "PlayerService").Info(
		"player loaded",
		originlog.Int64("player_id", 7),
		originlog.String("display_name", "Alice Smith"),
		originlog.String("display key", "quoted-key"),
		originlog.String("line", "first\nsecond"),
		originlog.Any("profile", map[string]any{"level": 9}),
	)
	if err := runtime.Close(context.Background()); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	text := strings.TrimSpace(stdout.String())
	if !strings.Contains(text, " INFO [game-1/PlayerService] ") ||
		!strings.Contains(text, " player loaded ") ||
		!strings.Contains(text, "player_id=7") ||
		!strings.Contains(text, `display_name="Alice Smith"`) ||
		!strings.Contains(text, `"display key"=quoted-key`) ||
		!strings.Contains(text, `line="first\nsecond"`) ||
		!strings.Contains(text, `profile={"level":9}`) {
		t.Fatalf("unexpected text output: %q", text)
	}
	if strings.Contains(text, "+0800") || strings.Count(text, "\n") != 0 {
		t.Fatalf("text output contains timezone offset or physical newline: %q", text)
	}
}

// TestTextFormatEncodesAllPublicFieldKinds 固定文本 key=value 对全部公开 Field 类型的编码，
// 防止新增直写 Encoder 时数值退化为字符串、时间破坏单行或字节产生不可打印内容。
func TestTextFormatEncodesAllPublicFieldKinds(t *testing.T) {
	t.Parallel()

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	runtime := newTestRuntime(t, originlog.DefaultConfig(), &stdout, &stderr)
	runtime.Logger().Info(
		"field kinds",
		originlog.String("string", "value"),
		originlog.Bool("bool", true),
		originlog.Int("int", -1),
		originlog.Int32("int32", -2),
		originlog.Int64("int64", -3),
		originlog.Uint("uint", 1),
		originlog.Uint32("uint32", 2),
		originlog.Uint64("uint64", 3),
		originlog.Float32("float32", 1.25),
		originlog.Float64("float64", 2.5),
		originlog.Duration("duration", 1500*time.Millisecond),
		originlog.Time("event_time", time.Date(2026, 8, 8, 10, 20, 31, 123, time.UTC)),
		originlog.Bytes("bytes", []byte{0, 1}),
		originlog.Err(errors.New("disk full")),
		originlog.Any("object", map[string]int{"x": 10}),
	)
	if err := runtime.Close(context.Background()); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	text := stdout.String()
	for _, want := range []string{
		"string=value", "bool=true", "int=-1", "int32=-2", "int64=-3",
		"uint=1", "uint32=2", "uint64=3", "float32=1.25", "float64=2.5",
		"duration=1.5s", `event_time="2026-08-08T10:20:31.000000123Z"`,
		"bytes=AAE=", `error="disk full"`, `object={"x":10}`,
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("text output missing %q: %q", want, text)
		}
	}
}

// TestContextFieldMasksAreIndependent 固定 Console 的 node_id/service_name 四种显示组合。
func TestContextFieldMasksAreIndependent(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		node        bool
		service     bool
		wantNode    bool
		wantService bool
	}{
		{name: "both", node: true, service: true, wantNode: true, wantService: true},
		{name: "node", node: true, service: false, wantNode: true},
		{name: "service", node: false, service: true, wantService: true},
		{name: "neither", node: false, service: false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			var stdout bytes.Buffer
			var stderr bytes.Buffer
			config := originlog.DefaultConfig()
			config.Console.Format = originlog.JSONFormat
			config.Console.ContextFields.NodeID = test.node
			config.Console.ContextFields.ServiceName = test.service
			runtime := newTestRuntime(t, config, &stdout, &stderr)
			runtime.Logger().WithScope("game-1", "PlayerService").Info("masked")
			if err := runtime.Close(context.Background()); err != nil {
				t.Fatalf("Close() error = %v", err)
			}
			var value map[string]any
			if err := json.Unmarshal(bytes.TrimSpace(stdout.Bytes()), &value); err != nil {
				t.Fatalf("decode JSON: %v: %s", err, stdout.String())
			}
			_, nodeExists := value["node_id"]
			_, serviceExists := value["service_name"]
			if nodeExists != test.wantNode || serviceExists != test.wantService {
				t.Fatalf("masked JSON = %#v", value)
			}
			if timestamp, _ := value["time"].(string); !strings.HasSuffix(timestamp, "Z") {
				t.Fatalf("JSON time = %#v, want UTC Z", value["time"])
			}
		})
	}
}

// TestRuntimeOutputControlIsIndependent 防止修改 Console 时连带改变 File，或 Reset 恢复到
// 硬编码 Info 而不是各输出端自己的启动配置。
func TestRuntimeOutputControlIsIndependent(t *testing.T) {
	t.Parallel()

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	path := filepath.Join(t.TempDir(), "origin.log")
	config := originlog.DefaultConfig()
	config.Mode = originlog.SyncMode
	config.File.Enabled = true
	config.File.Path = path
	config.File.Level = originlog.WarnLevel
	raw, err := NewHandler(config, withConsoleWriters(&stdout, &stderr))
	if err != nil {
		t.Fatalf("NewHandler() error = %v", err)
	}
	controller, ok := raw.(originlog.Controller)
	if !ok {
		t.Fatal("default handler does not implement log.Controller")
	}
	runtime, err := originlog.NewRuntime(config, raw)
	if err != nil {
		t.Fatalf("NewRuntime() error = %v", err)
	}
	t.Cleanup(func() { _ = runtime.Close(context.Background()) })

	if err := controller.SetConsoleLevel(originlog.DebugLevel); err != nil {
		t.Fatalf("SetConsoleLevel() error = %v", err)
	}
	if err := controller.SetFileLevel(originlog.ErrorLevel); err != nil {
		t.Fatalf("SetFileLevel() error = %v", err)
	}
	runtime.Logger().Debug("console-debug")
	if err := controller.SetConsoleEnabled(false); err != nil {
		t.Fatalf("SetConsoleEnabled(false) error = %v", err)
	}
	runtime.Logger().Error("file-only-error")
	if err := controller.ResetConsoleLevel(); err != nil {
		t.Fatalf("ResetConsoleLevel() error = %v", err)
	}
	if err := controller.SetConsoleEnabled(true); err != nil {
		t.Fatalf("SetConsoleEnabled(true) error = %v", err)
	}
	if err := controller.ResetFileLevel(); err != nil {
		t.Fatalf("ResetFileLevel() error = %v", err)
	}
	if err := controller.SetFileEnabled(false); err != nil {
		t.Fatalf("SetFileEnabled(false) error = %v", err)
	}
	runtime.Logger().Warn("console-only-warn")
	if err := controller.SetFileEnabled(true); err != nil {
		t.Fatalf("SetFileEnabled(true) error = %v", err)
	}
	runtime.Logger().Warn("both-warn")
	status := controller.Status()
	if status.Console.Level != originlog.InfoLevel ||
		status.File.Level != originlog.WarnLevel ||
		!status.Console.Enabled || !status.File.Enabled {
		t.Fatalf("status after reset = %+v", status)
	}
	if err := runtime.Close(context.Background()); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	if !strings.Contains(stdout.String(), "console-debug") ||
		strings.Contains(stdout.String(), "file-only-error") ||
		!strings.Contains(stdout.String(), "console-only-warn") ||
		!strings.Contains(stdout.String(), "both-warn") {
		t.Fatalf("console output = %q", stdout.String())
	}
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	if strings.Contains(string(content), "console-debug") ||
		!strings.Contains(string(content), "file-only-error") ||
		strings.Contains(string(content), "console-only-warn") ||
		!strings.Contains(string(content), "both-warn") {
		t.Fatalf("file output = %q", content)
	}
}

// TestRuntimeOutputControlRejectsInvalidAndUnavailable 防止关闭配置被运行时隐式创建资源，
// 并固定非法级别的参数错误分类。
func TestRuntimeOutputControlRejectsInvalidAndUnavailable(t *testing.T) {
	t.Parallel()

	raw, err := NewHandler(originlog.DefaultConfig(), withConsoleWriters(io.Discard, io.Discard))
	if err != nil {
		t.Fatalf("NewHandler() error = %v", err)
	}
	defer raw.Close()
	controller := raw.(originlog.Controller)
	if err := controller.SetConsoleLevel(originlog.LevelInvalid); !errs.IsCode(err, errs.CodeInvalidArgument) {
		t.Fatalf("SetConsoleLevel(invalid) error = %v", err)
	}
	if err := controller.SetFileEnabled(true); !errs.IsCode(err, errs.CodeLogOutputUnavailable) {
		t.Fatalf("SetFileEnabled(true) error = %v", err)
	}
	if err := raw.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	if err := controller.SetConsoleEnabled(true); !errs.IsCode(err, errs.CodeLogClosed) {
		t.Fatalf("SetConsoleEnabled after Close error = %v", err)
	}
}

func TestFileAndConsoleLevelsAreIndependent(t *testing.T) {
	t.Parallel()

	// 控制台保留默认 Info，文件启用默认 Debug 并选择 JSON。
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	path := filepath.Join(t.TempDir(), "origin.log")
	config := originlog.DefaultConfig()
	config.File.Enabled = true
	config.File.Path = path
	config.File.Format = originlog.JSONFormat
	runtime := newTestRuntime(t, config, &stdout, &stderr)

	// Debug 只应进入文件，Info 同时进入控制台和文件。
	runtime.Logger().Debug("debug-file")
	runtime.Logger().Info("info-both")
	if err := runtime.Close(context.Background()); err != nil {
		t.Fatalf("Close() = %v", err)
	}

	if strings.Contains(stdout.String(), "debug-file") ||
		!strings.Contains(stdout.String(), "info-both") {
		t.Fatalf("unexpected stdout: %s", stdout.String())
	}
	// 读取真实文件验证两个级别均存在。
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

	// 形成 text/json 的 2×2 输出格式组合矩阵。
	formats := []originlog.Format{originlog.TextFormat, originlog.JSONFormat}
	for _, consoleFormat := range formats {
		for _, fileFormat := range formats {
			name := string(consoleFormat) + "-" + string(fileFormat)
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				// 每个子测试使用独立控制台缓冲和活动文件。
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
				// 写入同一消息并关闭，随后分别按各自格式验证。
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

	// 工厂计数用于确认 stdout/stderr 各创建一个独立 Encoder。
	var calls atomic.Uint64
	factory := func(config zapcore.EncoderConfig) (zapcore.Encoder, error) {
		calls.Add(1)
		return zapcore.NewJSONEncoder(config), nil
	}

	// 连续构造两个实例，每个实例显式注册同名自定义格式。
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
	// 两个实例乘两个 Console Core，应调用四次工厂。
	if calls.Load() != 4 {
		t.Fatalf("factory calls = %d, want 4", calls.Load())
	}

	// 新实例未传 Option 时不能看到此前注册，证明没有全局注册表。
	config := originlog.DefaultConfig()
	config.Console.Format = "ecs"
	if _, err := NewHandler(config); !errs.IsCode(err, errs.CodeInvalidConfig) {
		t.Fatalf("unregistered encoder error = %v", err)
	}
}

func TestEncoderValidation(t *testing.T) {
	t.Parallel()

	// 准备成功和显式失败的两个工厂。
	factory := func(config zapcore.EncoderConfig) (zapcore.Encoder, error) {
		return zapcore.NewJSONEncoder(config), nil
	}
	failedFactory := func(zapcore.EncoderConfig) (zapcore.Encoder, error) {
		return nil, errors.New("factory failed")
	}
	// 表格覆盖保留名、nil、重复和工厂执行失败。
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

	// 所有选项错误都应在 Handler 构造阶段返回配置错误码。
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

	// 把文件 Path 指向目录，强制 rotate.Writer 打开活动文件失败。
	config := originlog.DefaultConfig()
	config.Console.Enabled = false
	config.File.Enabled = true
	config.File.Path = t.TempDir()
	// 外部 I/O 失败应分类为日志输出错误，而非配置语法错误。
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
	// 通过内部测试 Option 替换控制台 Writer 并构造完整 Runtime。
	runtime, err := New(config, withConsoleWriters(stdout, stderr))
	if err != nil {
		t.Fatalf("New() = %v", err)
	}
	// 生命周期由调用测试负责关闭。
	return runtime
}

func assertFormat(t *testing.T, format originlog.Format, content []byte) {
	t.Helper()
	// JSON 分支要求可解码且 msg 字段正确。
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
	// Text 分支要求可读级别和消息均存在。
	if !strings.Contains(string(content), "INFO") ||
		!strings.Contains(string(content), "combination") {
		t.Fatalf("unexpected Text output: %s", content)
	}
}

func BenchmarkJSONEncoding(b *testing.B) {
	// 构造写入 io.Discard 的 JSON Handler，排除终端和磁盘成本。
	config := originlog.DefaultConfig()
	config.Console.Format = originlog.JSONFormat
	handler, err := NewHandler(config, withConsoleWriters(io.Discard, io.Discard))
	if err != nil {
		b.Fatal(err)
	}
	defer handler.Close()
	// Record 和字段在计时前固定，循环只测转换和 Zap 编码。
	record := originlog.Record{
		Time:    time.Now(),
		Level:   originlog.InfoLevel,
		Message: "message",
		Caller:  originlog.Caller{File: "service/player.go", Line: 10},
	}
	fields := []originlog.Field{
		originlog.String("node_id", "game-1"),
		originlog.String("service_name", "PlayerService"),
		originlog.Int64("player_id", 7),
		originlog.Bool("online", true),
	}

	// 报告每次 Handler.Write 的分配。
	b.ReportAllocs()
	for b.Loop() {
		if err := handler.Write(record, fields); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkTextEncoding(b *testing.B) {
	// 构造写入 io.Discard 的默认文本 Handler。
	config := originlog.DefaultConfig()
	handler, err := NewHandler(config, withConsoleWriters(io.Discard, io.Discard))
	if err != nil {
		b.Fatal(err)
	}
	defer handler.Close()
	// 使用与 JSON 基准相同 Record 和归属字段，覆盖输出端掩码与直接文本编码成本。
	record := originlog.Record{
		Time:    time.Now(),
		Level:   originlog.InfoLevel,
		Message: "message",
		Caller:  originlog.Caller{File: "service/player.go", Line: 10},
	}
	fields := []originlog.Field{
		originlog.String("node_id", "game-1"),
		originlog.String("service_name", "PlayerService"),
		originlog.Int64("player_id", 7),
		originlog.Bool("online", true),
	}

	// 循环只测字段掩码、文本编码和 Writer 调用。
	b.ReportAllocs()
	for b.Loop() {
		if err := handler.Write(record, fields); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkFieldConversion(b *testing.B) {
	// 样本覆盖字符串、整数、布尔和时长四种常见字段。
	fields := []originlog.Field{
		originlog.String("service", "PlayerService"),
		originlog.Int64("player_id", 7),
		originlog.Bool("online", true),
		originlog.Duration("latency", time.Millisecond),
	}
	// 预分配目标切片，隔离单字段转换成本。
	converted := make([]zapcore.Field, len(fields))

	// 每轮按原顺序转换全部字段。
	b.ReportAllocs()
	for b.Loop() {
		for index, field := range fields {
			converted[index] = toZapField(field)
		}
	}
}
