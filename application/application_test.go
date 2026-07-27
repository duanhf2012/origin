package application

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/duanhf2012/origin/v3/command"
	"github.com/duanhf2012/origin/v3/errs"
	originlog "github.com/duanhf2012/origin/v3/log"
	"github.com/duanhf2012/origin/v3/node"
	"github.com/duanhf2012/origin/v3/service"
)

// lifecycleTestService 允许测试通过 NodeID 制造确定的启动失败。
type lifecycleTestService struct {
	service.Service
	started bool
	stopped bool
}

func (target *lifecycleTestService) OnInit() error {
	if target.NodeID() == "bad-1" {
		return testInitFailure
	}
	return nil
}

func (target *lifecycleTestService) OnStart(context.Context) error {
	target.started = true
	return nil
}

func (target *lifecycleTestService) OnStop(context.Context) error {
	target.stopped = true
	return nil
}

var testInitFailure = errors.New("test init failure")

// silentHandler 避免单元测试污染控制台，并记录 Runtime 确实完成 Close。
type silentHandler struct {
	closed atomic.Bool
	writes atomic.Uint64
}

func (*silentHandler) Enabled(originlog.Level) bool { return true }
func (handler *silentHandler) Write(originlog.Record, []originlog.Field) error {
	handler.writes.Add(1)
	return nil
}
func (*silentHandler) Sync() error { return nil }
func (handler *silentHandler) Close() error {
	handler.closed.Store(true)
	return nil
}

func TestApplicationRunsSelectedNodesAndStopsInPlace(t *testing.T) {
	directory := writeApplicationConfig(t, `
buffer_pool:
  track_usage: true
nodes:
  - id: gateway-1
    services:
      - lifecycleTestService
      - scene-1:lifecycleTestService
  - id: ignored-1
    services:
      - lifecycleTestService
`)
	handler := &silentHandler{}
	app := New(Options{
		LogHandlerFactory: func(originlog.Config) (originlog.Handler, error) {
			return handler, nil
		},
	})
	app.Setup(&lifecycleTestService{})

	runCtx, cancel := context.WithCancel(context.Background())
	result := make(chan error, 1)
	go func() {
		result <- app.run(runCtx, command.StartRequest{
			AppName:   "lifecycle-test",
			ConfigDir: directory,
			NodeIDs:   []string{"gateway-1"},
		})
	}()
	waitForState(t, app, StateRunning)

	// 同一模板在一个 Node 中生成两个运行身份不同、地址也不同的实例。
	current, ok := app.Node("gateway-1")
	if !ok {
		t.Fatal("未找到 gateway-1")
	}
	first, ok := current.Service("lifecycleTestService")
	if !ok {
		t.Fatal("未找到普通 Service")
	}
	second, ok := current.Service("scene-1")
	if !ok {
		t.Fatal("未找到模板 Service")
	}
	if first == second {
		t.Fatal("两个配置实例错误地共享同一指针")
	}
	if len(app.Nodes()) != 1 {
		t.Fatalf("命令行筛选后 Nodes() 数量 = %d", len(app.Nodes()))
	}

	cancel()
	if err := <-result; err != nil {
		t.Fatalf("run() error = %v", err)
	}
	if app.State() != StateStopped {
		t.Fatalf("State() = %v", app.State())
	}
	if !first.(*lifecycleTestService).stopped ||
		!second.(*lifecycleTestService).stopped {
		t.Fatal("正常停止没有调用全部 OnStop")
	}
	if !handler.closed.Load() {
		t.Fatal("日志 Handler 没有关闭")
	}
}

func TestApplicationInitFailureRollsBackPreviousNode(t *testing.T) {
	directory := writeApplicationConfig(t, `
nodes:
  - id: good-1
    services: [lifecycleTestService]
  - id: bad-1
    services: [lifecycleTestService]
  - id: later-1
    services: [lifecycleTestService]
`)
	app := newSilentApplication()
	app.Setup(&lifecycleTestService{})
	err := app.run(context.Background(), command.StartRequest{
		AppName:   "failure-test",
		ConfigDir: directory,
	})
	if !errors.Is(err, testInitFailure) {
		t.Fatalf("run() error = %v", err)
	}
	if app.State() != StateFailed {
		t.Fatalf("State() = %v", app.State())
	}

	// 已成功 Node 必须回滚；OnInit 失败 Node 不进入 OnStop。
	good, _ := app.Node("good-1")
	goodService, _ := good.Service("lifecycleTestService")
	if !goodService.(*lifecycleTestService).stopped {
		t.Fatal("此前成功 Node 没有回滚")
	}
	bad, _ := app.Node("bad-1")
	badService, _ := bad.Service("lifecycleTestService")
	if badService.(*lifecycleTestService).stopped {
		t.Fatal("OnInit 失败 Service 不应调用 OnStop")
	}
	// 失败 Node 之后已经完成装配但尚未启动的 Node 也必须回收内部资源。
	later, _ := app.Node("later-1")
	if later.State() != node.StateFailed {
		t.Fatalf("未启动 Node 回滚后 State = %v，期望 Failed", later.State())
	}
	laterService, _ := later.Service("lifecycleTestService")
	if laterService.(*lifecycleTestService).stopped {
		t.Fatal("尚未 OnStart 的后续 Service 不应调用 OnStop")
	}
}

func TestApplicationStopCancelsRunningLifecycle(t *testing.T) {
	directory := writeApplicationConfig(t, `
nodes:
  - id: game-1
    services: [lifecycleTestService]
`)
	app := newSilentApplication()
	app.Setup(&lifecycleTestService{})
	result := make(chan error, 1)
	go func() {
		result <- app.run(context.Background(), command.StartRequest{
			AppName:   "stop-test",
			ConfigDir: directory,
		})
	}()
	waitForState(t, app, StateRunning)

	stopCtx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := app.Stop(stopCtx); err != nil {
		t.Fatalf("Stop() error = %v", err)
	}
	if err := <-result; err != nil {
		t.Fatalf("run() error = %v", err)
	}
	if err := app.Stop(context.Background()); err != nil {
		t.Fatalf("重复 Stop() error = %v", err)
	}
}

func TestApplicationCommandRunnerIntegration(t *testing.T) {
	directory := writeApplicationConfig(t, `
nodes:
  - id: game-1
    services: [lifecycleTestService]
`)
	pidDirectory := t.TempDir()
	app := newSilentApplication()
	app.Setup(&lifecycleTestService{})
	ctx, cancel := context.WithCancel(context.Background())
	result := make(chan struct {
		code command.ExitCode
		err  error
	}, 1)
	go func() {
		var stdout bytes.Buffer
		var stderr bytes.Buffer
		code, err := app.execute(ctx, []string{
			"start",
			"--app-name", "m7-integration",
			"--config", directory,
			"--pid-dir", pidDirectory,
		}, command.Options{
			Stdout: &stdout,
			Stderr: &stderr,
		})
		result <- struct {
			code command.ExitCode
			err  error
		}{code: code, err: err}
	}()
	waitForState(t, app, StateRunning)
	cancel()
	execution := <-result
	if execution.code != command.ExitSuccess || execution.err != nil {
		t.Fatalf("execute() = (%v, %v)", execution.code, execution.err)
	}
}

func TestLoadConfigRejectsFutureFrameworkSection(t *testing.T) {
	directory := writeApplicationConfig(t, `
rpc:
  transport: tcp
nodes:
  - id: game-1
    services: [lifecycleTestService]
`)
	_, err := loadConfig(directory)
	if !errs.IsCode(err, errs.CodeInvalidConfig) {
		t.Fatalf("loadConfig() error = %v", err)
	}
}

func TestLoadConfigSchedulerDefaultsAndOverrides(t *testing.T) {
	directory := writeApplicationConfig(t, `
nodes:
  - id: default-1
    services: [lifecycleTestService]
  - id: custom-1
    scheduler:
      max_tasks: 1234
      max_await_tasks: 321
      default_await_timeout: 3s
    services: [lifecycleTestService]
`)
	loaded, err := loadConfig(directory)
	if err != nil {
		t.Fatalf("loadConfig() error = %v", err)
	}

	// 省略 scheduler 使用统一默认值；显式配置则完整转换为运行时 time.Duration。
	if loaded.nodes[0].Scheduler != service.DefaultSchedulerConfig() {
		t.Fatalf("默认 Scheduler = %+v", loaded.nodes[0].Scheduler)
	}
	custom := loaded.nodes[1].Scheduler
	if custom.MaxTasks != 1234 || custom.MaxAwaitTasks != 321 ||
		custom.DefaultAwaitTimeout != 3*time.Second {
		t.Fatalf("自定义 Scheduler = %+v", custom)
	}
}

func TestLoadConfigSchedulerPartialOverrideAndValidation(t *testing.T) {
	partialDirectory := writeApplicationConfig(t, `
nodes:
  - id: game-1
    scheduler:
      default_await_timeout: 2s
    services: [lifecycleTestService]
`)
	loaded, err := loadConfig(partialDirectory)
	if err != nil {
		t.Fatalf("partial loadConfig() error = %v", err)
	}
	if loaded.nodes[0].Scheduler.MaxTasks != service.DefaultMaxTasks ||
		loaded.nodes[0].Scheduler.MaxAwaitTasks != service.DefaultMaxAwaitTasks ||
		loaded.nodes[0].Scheduler.DefaultAwaitTimeout != 2*time.Second {
		t.Fatalf("部分覆盖 Scheduler = %+v", loaded.nodes[0].Scheduler)
	}

	// 零容量、Await 超过总任务、零超时和未知字段都必须在配置加载阶段拒绝。
	for _, content := range []string{
		`nodes:
  - id: game-1
    scheduler: {max_tasks: 0}
    services: [lifecycleTestService]
`,
		`nodes:
  - id: game-1
    scheduler: {max_tasks: 10, max_await_tasks: 11}
    services: [lifecycleTestService]
`,
		`nodes:
  - id: game-1
    scheduler: {default_await_timeout: 0s}
    services: [lifecycleTestService]
`,
		`nodes:
  - id: game-1
    scheduler: {unknown: 1}
    services: [lifecycleTestService]
`,
	} {
		directory := writeApplicationConfig(t, content)
		if _, err := loadConfig(directory); !errs.IsCode(err, errs.CodeInvalidConfig) {
			t.Fatalf("非法 scheduler loadConfig() error = %v", err)
		}
	}
}

func TestCatalogRejectsNonZeroTemplate(t *testing.T) {
	app := New()
	app.Setup(&lifecycleTestService{started: true})
	err := app.catalog.freeze()
	if !errs.IsCode(err, errs.CodeInvalidArgument) {
		t.Fatalf("非零模板 error = %v", err)
	}
}

func TestDecodeLogConfigFullConfiguration(t *testing.T) {
	configured, err := decodeLogConfig(map[string]any{
		"mode": "sync",
		"console": map[string]any{
			"enabled": true,
			"level":   "debug",
			"format":  "json",
		},
		"file": map[string]any{
			"enabled": true,
			"level":   "warn",
			"format":  "text",
			"path":    "logs/game.log",
			"rotation": map[string]any{
				"max_size": "4M",
				"by_date":  false,
				"timezone": "UTC",
			},
			"retention": map[string]any{
				"max_age":   "48h",
				"max_files": 7,
				"compress":  false,
			},
		},
	})
	if err != nil {
		t.Fatalf("decodeLogConfig() error = %v", err)
	}
	if configured.Mode != originlog.SyncMode ||
		configured.Console.Level != originlog.DebugLevel ||
		configured.Console.Format != originlog.JSONFormat {
		t.Fatalf("控制台配置 = %+v", configured.Console)
	}
	if configured.File.Rotation.MaxSizeMB != 4 ||
		configured.File.Rotation.Timezone != originlog.UTCTime ||
		configured.File.Retention.MaxAgeDays != 2 {
		t.Fatalf("文件配置 = %+v", configured.File)
	}
}

func TestDecodeLogConfigRejectsInvalidValues(t *testing.T) {
	tests := []struct {
		name string
		raw  map[string]any
	}{
		{name: "mode", raw: map[string]any{"mode": "fast"}},
		{name: "console level", raw: map[string]any{
			"console": map[string]any{"level": "trace"},
		}},
		{name: "file level", raw: map[string]any{
			"file": map[string]any{"level": "trace"},
		}},
		{name: "console format", raw: map[string]any{
			"console": map[string]any{"format": "xml"},
		}},
		{name: "file format", raw: map[string]any{
			"file": map[string]any{"format": "xml"},
		}},
		{name: "unaligned size", raw: map[string]any{
			"file": map[string]any{
				"rotation": map[string]any{"max_size": "1KB"},
			},
		}},
		{name: "timezone", raw: map[string]any{
			"file": map[string]any{
				"rotation": map[string]any{"timezone": "Asia/Shanghai"},
			},
		}},
		{name: "max age", raw: map[string]any{
			"file": map[string]any{
				"retention": map[string]any{"max_age": "1h"},
			},
		}},
		{name: "unknown field", raw: map[string]any{"queue_size": 10}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := decodeLogConfig(test.raw); !errs.IsCode(err, errs.CodeInvalidConfig) {
				t.Fatalf("decodeLogConfig() error = %v", err)
			}
		})
	}
}

func TestServiceDeclarationFormsAndErrors(t *testing.T) {
	valid := []struct {
		value    string
		name     string
		template string
		private  bool
	}{
		{value: "PlayerService", name: "PlayerService", template: "PlayerService"},
		{value: "_DebugService", name: "DebugService", template: "DebugService", private: true},
		{value: "scene-1:SceneService", name: "scene-1", template: "SceneService"},
		{value: "_scene-2:SceneService", name: "scene-2", template: "SceneService", private: true},
	}
	for _, test := range valid {
		name, template, private, err := parseServiceDeclaration(test.value)
		if err != nil {
			t.Fatalf("parseServiceDeclaration(%q): %v", test.value, err)
		}
		if name != test.name || template != test.template || private != test.private {
			t.Fatalf(
				"parseServiceDeclaration(%q) = %q, %q, %v",
				test.value,
				name,
				template,
				private,
			)
		}
	}
	for _, value := range []string{"", "_", "a:b:c", ":Template", "actual:"} {
		if _, _, _, err := parseServiceDeclaration(value); err == nil {
			t.Fatalf("parseServiceDeclaration(%q) 未返回错误", value)
		}
	}
}

func TestRegisterCommandAndHelp(t *testing.T) {
	app := New()
	called := false
	err := app.RegisterCommand(command.Command{
		Name:    "inspect",
		Summary: "检查测试数据",
		Usage:   "test inspect",
		Run: func(command.Context, []string) error {
			called = true
			return nil
		},
	})
	if err != nil {
		t.Fatalf("RegisterCommand() error = %v", err)
	}
	var stdout bytes.Buffer
	code, err := app.execute(
		context.Background(),
		[]string{"inspect"},
		command.Options{Stdout: &stdout},
	)
	if err != nil || code != command.ExitSuccess || !called {
		t.Fatalf("execute() = %v, %v, called=%v", code, err, called)
	}
	if err := app.RegisterCommand(command.Command{
		Name: "later",
	}); err == nil {
		t.Fatal("执行命令后 RegisterCommand() 未返回错误")
	}
}

func TestApplicationConstructionAndCreatedStopEdges(t *testing.T) {
	if app := New(Options{}, Options{}); app.catalog.freeze() == nil {
		t.Fatal("多个 Options 未记录错误")
	}
	if app := New(Options{StartTimeout: -time.Second}); app.catalog.freeze() == nil {
		t.Fatal("负 StartTimeout 未记录错误")
	}
	app := New()
	if app.Logger().Enabled(originlog.InfoLevel) {
		t.Fatal("初始化前 Logger 不应启用")
	}
	if err := app.Stop(context.Background()); err != nil {
		t.Fatalf("Created Stop() error = %v", err)
	}
	app.Setup(&lifecycleTestService{})
	if err := app.catalog.freeze(); err == nil {
		t.Fatal("Stopped 后 Setup 未记录错误")
	}
}

func TestRollbackBuiltNodesPreservesPrimaryError(t *testing.T) {
	// 直接构造一个尚未启动的 Node，模拟后续 Node 在装配阶段失败的部分初始化场景。
	target := &lifecycleTestService{}
	built, err := node.New(
		node.Config{ID: "built-1", Services: []string{"LifecycleService"}},
		[]node.ServiceBinding{{
			Name:     "LifecycleService",
			Template: "LifecycleService",
			Service:  target,
		}},
		originlog.NewNop(),
	)
	if err != nil {
		t.Fatalf("node.New() error = %v", err)
	}
	primary := errors.New("next node build failed")

	// 回滚必须保留原始失败，同时把已创建 Node 置为不可复用的 Failed。
	result := rollbackBuiltNodes([]*node.Node{built}, primary)
	if !errors.Is(result, primary) {
		t.Fatalf("rollbackBuiltNodes() 丢失原始错误: %v", result)
	}
	if built.State() != node.StateFailed {
		t.Fatalf("回滚后 Node State = %v，期望 Failed", built.State())
	}
}

func TestUnreportedErrorFiltersOnlyLoggedBranches(t *testing.T) {
	logged := reportedError{cause: errors.New("already logged")}
	pending := errors.New("close failed")
	result := unreportedError(errors.Join(logged, pending))
	if !errors.Is(result, pending) {
		t.Fatalf("未保留未报告错误: %v", result)
	}
	if result == nil || result.Error() != pending.Error() {
		t.Fatalf("过滤结果 = %v", result)
	}
	if result := unreportedError(logged); result != nil {
		t.Fatalf("已报告错误仍需输出: %v", result)
	}
}

func newSilentApplication() *Application {
	return New(Options{
		LogHandlerFactory: func(originlog.Config) (originlog.Handler, error) {
			return &silentHandler{}, nil
		},
	})
}

func writeApplicationConfig(t *testing.T, content string) string {
	t.Helper()
	directory := t.TempDir()
	path := filepath.Join(directory, "application.yaml")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("写入配置: %v", err)
	}
	return directory
}

func waitForState(t *testing.T, app *Application, expected State) {
	t.Helper()
	deadline := time.NewTimer(3 * time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-deadline.C:
			t.Fatalf("等待状态 %v 超时，当前状态 %v", expected, app.State())
		case <-ticker.C:
			if app.State() == expected {
				return
			}
			if app.State() == StateFailed {
				t.Fatalf("等待状态 %v 时 Application 已失败", expected)
			}
		}
	}
}
