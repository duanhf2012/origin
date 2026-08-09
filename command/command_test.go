package command

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/duanhf2012/origin/v3/errs"
)

// newTestRunner 创建输入输出完全隔离的 Runner，避免测试污染进程标准流。
func newTestRunner(
	t *testing.T,
	start StartHandler,
) (*Runner, *bytes.Buffer, *bytes.Buffer) {
	t.Helper()

	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}
	runner, err := New(Options{
		ProgramName: "game-server",
		Stdin:       strings.NewReader(""),
		Stdout:      stdout,
		Stderr:      stderr,
		Start:       start,
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	return runner, stdout, stderr
}

// noOpStart 是不创建业务资源的测试 Start Handler。
func noOpStart(context.Context, StartRequest) error {
	return nil
}

func TestNewRequiresStartHandler(t *testing.T) {
	t.Parallel()

	// 缺失唯一生命周期入口时必须在构造阶段失败。
	_, err := New(Options{})
	if !errs.IsCode(err, errs.CodeInvalidArgument) {
		t.Fatalf("New() error code = %d, want %d", errs.CodeOf(err), errs.CodeInvalidArgument)
	}
}

func TestHelpAndVersion(t *testing.T) {
	t.Parallel()

	runner, stdout, _ := newTestRunner(t, noOpStart)

	// 注册顺序故意与字典序相反，验证总帮助使用稳定排序。
	for _, command := range []Command{
		{
			Name:    "z-check",
			Summary: "Z 检查",
			Usage:   "game-server z-check",
			Run:     func(Context, []string) error { return nil },
		},
		{
			Name:    "a-check",
			Summary: "A 检查",
			Usage:   "game-server a-check",
			Run:     func(Context, []string) error { return nil },
		},
	} {
		if err := runner.Register(command); err != nil {
			t.Fatalf("Register(%q) error = %v", command.Name, err)
		}
	}

	code, err := runner.Run(context.Background(), []string{"help"})
	if err != nil || code != ExitSuccess {
		t.Fatalf("help = (%d, %v), want (%d, nil)", code, err, ExitSuccess)
	}
	help := stdout.String()
	if strings.Index(help, "a-check") > strings.Index(help, "z-check") {
		t.Fatalf("custom commands are not sorted:\n%s", help)
	}
	if !strings.Contains(help, "game-server <command>") {
		t.Fatalf("help does not contain program usage:\n%s", help)
	}
	for _, commandName := range []string{"retire", "resume"} {
		if !strings.Contains(help, commandName) {
			t.Fatalf("help does not contain %q:\n%s", commandName, help)
		}
		stdout.Reset()
		code, err = runner.Run(context.Background(), []string{"help", commandName})
		if err != nil || code != ExitSuccess {
			t.Fatalf("help %s = (%d, %v), want success", commandName, code, err)
		}
		for _, option := range []string{"--app-name", "--pid-dir", "--timeout 30s"} {
			if !strings.Contains(stdout.String(), option) {
				t.Fatalf("help %s missing %q:\n%s", commandName, option, stdout.String())
			}
		}
	}

	// 清空输出后验证未注入构建字段仍保持固定 version 外观。
	stdout.Reset()
	code, err = runner.Run(context.Background(), []string{"version"})
	if err != nil || code != ExitSuccess {
		t.Fatalf("version = (%d, %v), want (%d, nil)", code, err, ExitSuccess)
	}
	version := stdout.String()
	for _, field := range []string{
		"version: unknown",
		"commit: unknown",
		"build_time: unknown",
		"go_version: go",
	} {
		if !strings.Contains(version, field) {
			t.Fatalf("version output missing %q:\n%s", field, version)
		}
	}
}

func TestRetireResumeArguments(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		args []string
	}{
		{name: "retire missing app", args: []string{"retire"}},
		{name: "resume invalid app", args: []string{"resume", "--app-name", "Game"}},
		{name: "retire zero timeout", args: []string{"retire", "--app-name", "game", "--timeout", "0s"}},
		{name: "resume invalid timeout", args: []string{"resume", "--app-name", "game", "--timeout", "soon"}},
		{name: "retire positional", args: []string{"retire", "--app-name", "game", "extra"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			runner, _, _ := newTestRunner(t, noOpStart)
			code, err := runner.Run(t.Context(), test.args)
			if code != ExitUsage || !errs.IsCode(err, errs.CodeInvalidArgument) {
				t.Fatalf("Run(%v) = (%d, %v), want usage error", test.args, code, err)
			}
		})
	}
}

func TestRetireResumeCommandsRoundTrip(t *testing.T) {
	root := t.TempDir()
	configDir := filepath.Join(root, "config")
	pidDir := filepath.Join(root, "run")
	if err := os.Mkdir(configDir, 0o755); err != nil {
		t.Fatal(err)
	}

	started := make(chan struct{})
	actions := make(chan ControlAction, 2)
	target, _, _ := newTestRunner(t, func(ctx context.Context, request StartRequest) error {
		close(started)
		for {
			select {
			case control := <-request.Controls:
				actions <- control.Action()
				control.Complete(nil)
			case <-ctx.Done():
				return nil
			}
		}
	})
	targetCtx, cancelTarget := context.WithCancel(t.Context())
	targetResult := make(chan error, 1)
	go func() {
		_, err := target.Run(targetCtx, []string{
			"start", "--app-name", "game", "--config", configDir, "--pid-dir", pidDir,
		})
		targetResult <- err
	}()
	<-started

	client, _, _ := newTestRunner(t, noOpStart)
	for _, test := range []struct {
		name   string
		action ControlAction
	}{
		{name: "retire", action: ControlActionRetire},
		{name: "resume", action: ControlActionResume},
	} {
		code, err := client.Run(t.Context(), []string{
			test.name, "--app-name", "game", "--pid-dir", pidDir,
		})
		if err != nil || code != ExitSuccess {
			t.Fatalf("%s = (%d, %v), want success", test.name, code, err)
		}
		if action := <-actions; action != test.action {
			t.Fatalf("%s action = %v, want %v", test.name, action, test.action)
		}
	}

	cancelTarget()
	if err := receiveControlResult(t, targetResult); err != nil {
		t.Fatalf("target error = %v", err)
	}
}

func TestRetireNotRunningIsProcessControlError(t *testing.T) {
	t.Parallel()

	runner, _, _ := newTestRunner(t, noOpStart)
	code, err := runner.Run(t.Context(), []string{
		"retire", "--app-name", "game", "--pid-dir", filepath.Join(t.TempDir(), "missing"),
	})
	if code != ExitProcessControl || !errs.IsCode(err, errs.CodeProcessControlFailed) {
		t.Fatalf("retire not running = (%d, %v), want process control error", code, err)
	}
}

func TestRunUsageErrorsAndRejectsLegacyCommands(t *testing.T) {
	t.Parallel()

	// 每个样本创建独立 Runner，确保首次 Run 冻结行为不会掩盖参数结果。
	tests := []struct {
		name     string
		args     []string
		wantCode ExitCode
		wantErr  errs.Code
	}{
		{name: "no command", args: nil, wantCode: ExitUsage, wantErr: errs.CodeInvalidArgument},
		{name: "unknown", args: []string{"missing"}, wantCode: ExitUsage, wantErr: errs.CodeInvalidArgument},
		{name: "legacy start", args: []string{"-start"}, wantCode: ExitUsage, wantErr: errs.CodeInvalidArgument},
		{name: "legacy stop", args: []string{"-stop"}, wantCode: ExitUsage, wantErr: errs.CodeInvalidArgument},
		{name: "legacy help", args: []string{"-help"}, wantCode: ExitUsage, wantErr: errs.CodeInvalidArgument},
		{name: "legacy short help", args: []string{"-h"}, wantCode: ExitUsage, wantErr: errs.CodeInvalidArgument},
		{name: "legacy long help", args: []string{"--help"}, wantCode: ExitUsage, wantErr: errs.CodeInvalidArgument},
		{
			name:     "version extra argument",
			args:     []string{"version", "extra"},
			wantCode: ExitUsage,
			wantErr:  errs.CodeInvalidArgument,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			runner, _, _ := newTestRunner(t, noOpStart)
			code, err := runner.Run(context.Background(), test.args)
			if code != test.wantCode {
				t.Fatalf("Run() code = %d, want %d", code, test.wantCode)
			}
			if got := errs.CodeOf(err); got != test.wantErr {
				t.Fatalf("Run() error code = %d, want %d; error = %v", got, test.wantErr, err)
			}
		})
	}
}

func TestRegisterAndRunCustomCommand(t *testing.T) {
	t.Parallel()

	runner, stdout, _ := newTestRunner(t, noOpStart)
	var received []string
	err := runner.Register(Command{
		Name:    "check-config",
		Summary: "检查配置",
		Usage:   "game-server check-config --config ./config",
		Run: func(ctx Context, args []string) error {
			// 验证实例流和原始参数均被交给回调。
			if ctx.Stdout != stdout {
				t.Fatalf("custom stdout was not injected")
			}
			received = append([]string(nil), args...)
			return nil
		},
	})
	if err != nil {
		t.Fatalf("Register() error = %v", err)
	}

	code, err := runner.Run(context.Background(), []string{"check-config", "--config", "./test"})
	if err != nil || code != ExitSuccess {
		t.Fatalf("custom command = (%d, %v), want (%d, nil)", code, err, ExitSuccess)
	}
	if want := []string{"--config", "./test"}; !reflect.DeepEqual(received, want) {
		t.Fatalf("custom args = %#v, want %#v", received, want)
	}

	// 首次 Run 后注册表冻结。
	err = runner.Register(Command{
		Name:    "later",
		Summary: "late",
		Usage:   "game-server later",
		Run:     func(Context, []string) error { return nil },
	})
	if !errs.IsCode(err, errs.CodeInvalidArgument) {
		t.Fatalf("late Register() error = %v, want invalid argument", err)
	}
}

func TestRegisterRejectsInvalidCommands(t *testing.T) {
	t.Parallel()

	validRun := func(Context, []string) error { return nil }
	tests := []Command{
		{Name: "", Summary: "x", Usage: "x", Run: validRun},
		{Name: "Start", Summary: "x", Usage: "x", Run: validRun},
		{Name: "start", Summary: "x", Usage: "x", Run: validRun},
		{Name: "valid", Summary: "", Usage: "x", Run: validRun},
		{Name: "valid", Summary: "x", Usage: "", Run: validRun},
		{Name: "valid", Summary: "x", Usage: "x"},
	}
	for index, command := range tests {
		runner, _, _ := newTestRunner(t, noOpStart)
		if err := runner.Register(command); !errs.IsCode(err, errs.CodeInvalidArgument) {
			t.Fatalf("case %d Register() error = %v, want invalid argument", index, err)
		}
	}

	runner, _, _ := newTestRunner(t, noOpStart)
	command := Command{Name: "valid", Summary: "x", Usage: "x", Run: validRun}
	if err := runner.Register(command); err != nil {
		t.Fatalf("first Register() error = %v", err)
	}
	if err := runner.Register(command); !errs.IsCode(err, errs.CodeInvalidArgument) {
		t.Fatalf("duplicate Register() error = %v, want invalid argument", err)
	}
}

func TestStartBuildsRequestAndPIDRecord(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	configDir := filepath.Join(root, "config")
	pidDir := filepath.Join(root, "run")
	if err := os.Mkdir(configDir, 0o755); err != nil {
		t.Fatalf("Mkdir(config) error = %v", err)
	}

	var received StartRequest
	runner, _, _ := newTestRunner(t, func(_ context.Context, request StartRequest) error {
		received = request
		return nil
	})
	code, err := runner.Run(context.Background(), []string{
		"start",
		"--app-name", "game-dev",
		"--config", configDir,
		"--pid-dir", pidDir,
		"--node", " gateway-1,game-1 ",
		"--diagnostics", "127.0.0.1:6061",
		"--pprof", "127.0.0.1:6060",
	})
	if err != nil || code != ExitSuccess {
		t.Fatalf("start = (%d, %v), want (%d, nil)", code, err, ExitSuccess)
	}
	if received.AppName != "game-dev" {
		t.Fatalf("AppName = %q, want game-dev", received.AppName)
	}
	if !filepath.IsAbs(received.ConfigDir) || !filepath.IsAbs(received.PIDDir) {
		t.Fatalf("request paths are not absolute: %#v", received)
	}
	if want := []string{"gateway-1", "game-1"}; !reflect.DeepEqual(received.NodeIDs, want) {
		t.Fatalf("NodeIDs = %#v, want %#v", received.NodeIDs, want)
	}
	if received.DiagnosticsAddress != "127.0.0.1:6061" ||
		received.PprofAddress != "127.0.0.1:6060" {
		t.Fatalf("HTTP addresses = %#v", received)
	}

	// PID 文件在正常退出后保留，内容必须是严格可解析的当前进程记录。
	data, readErr := os.ReadFile(filepath.Join(pidDir, "game-dev.pid"))
	if readErr != nil {
		t.Fatalf("ReadFile(pid) error = %v", readErr)
	}
	record, decodeErr := decodePIDRecord(data)
	if decodeErr != nil {
		t.Fatalf("decodePIDRecord() error = %v", decodeErr)
	}
	if record.PID != os.Getpid() {
		t.Fatalf("pid record = %d, want %d", record.PID, os.Getpid())
	}
}

func TestStartRejectsInvalidArgumentsBeforeHandler(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	configDir := filepath.Join(root, "config")
	if err := os.Mkdir(configDir, 0o755); err != nil {
		t.Fatalf("Mkdir(config) error = %v", err)
	}

	tests := []struct {
		name string
		args []string
		code errs.Code
	}{
		{name: "missing app", args: []string{"start", "--config", configDir}, code: errs.CodeInvalidArgument},
		{
			name: "uppercase app",
			args: []string{"start", "--app-name", "Game", "--config", configDir},
			code: errs.CodeInvalidArgument,
		},
		{
			name: "empty node",
			args: []string{"start", "--app-name", "game", "--config", configDir, "--node", "a,,b"},
			code: errs.CodeInvalidArgument,
		},
		{
			name: "duplicate node",
			args: []string{"start", "--app-name", "game", "--config", configDir, "--node", "a,a"},
			code: errs.CodeInvalidArgument,
		},
		{
			name: "removed retired option",
			args: []string{"start", "--app-name", "game", "--config", configDir, "--retired"},
			code: errs.CodeInvalidArgument,
		},
		{
			name: "missing config",
			args: []string{"start", "--app-name", "game", "--config", filepath.Join(root, "missing")},
			code: errs.CodeInvalidConfig,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			called := false
			runner, _, _ := newTestRunner(t, func(context.Context, StartRequest) error {
				called = true
				return nil
			})
			code, err := runner.Run(context.Background(), test.args)
			if code != ExitUsage {
				t.Fatalf("Run() code = %d, want %d", code, ExitUsage)
			}
			if got := errs.CodeOf(err); got != test.code {
				t.Fatalf("Run() error code = %d, want %d; error = %v", got, test.code, err)
			}
			if called {
				t.Fatalf("Start Handler was called for invalid arguments")
			}
		})
	}
}

func TestStartHandlerAndCustomPanicBecomeErrors(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	configDir := filepath.Join(root, "config")
	if err := os.Mkdir(configDir, 0o755); err != nil {
		t.Fatalf("Mkdir(config) error = %v", err)
	}

	startRunner, _, _ := newTestRunner(t, func(context.Context, StartRequest) error {
		panic("start failed")
	})
	code, err := startRunner.Run(context.Background(), []string{
		"start",
		"--app-name", "panic-start",
		"--config", configDir,
		"--pid-dir", filepath.Join(root, "run"),
	})
	if code != ExitFailure || !errs.IsCode(err, errs.CodeInternal) {
		t.Fatalf("start panic = (%d, %v), want ExitFailure/CodeInternal", code, err)
	}
	if !strings.Contains(err.Error(), "goroutine") {
		t.Fatalf("start panic error does not contain stack: %v", err)
	}

	customRunner, _, _ := newTestRunner(t, noOpStart)
	if registerErr := customRunner.Register(Command{
		Name:    "panic-command",
		Summary: "panic",
		Usage:   "game-server panic-command",
		Run:     func(Context, []string) error { panic("custom failed") },
	}); registerErr != nil {
		t.Fatalf("Register() error = %v", registerErr)
	}
	code, err = customRunner.Run(context.Background(), []string{"panic-command"})
	if code != ExitFailure || !errs.IsCode(err, errs.CodeInternal) {
		t.Fatalf("custom panic = (%d, %v), want ExitFailure/CodeInternal", code, err)
	}
}

func TestParentCancellationStopsStartAndReleasesLease(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	configDir := filepath.Join(root, "config")
	pidDir := filepath.Join(root, "run")
	if err := os.Mkdir(configDir, 0o755); err != nil {
		t.Fatalf("Mkdir(config) error = %v", err)
	}

	parent, cancel := context.WithCancel(context.Background())
	started := make(chan struct{})
	result := make(chan error, 1)
	runner, _, _ := newTestRunner(t, func(ctx context.Context, _ StartRequest) error {
		close(started)
		<-ctx.Done()
		return nil
	})
	go func() {
		_, err := runner.Run(parent, []string{
			"start",
			"--app-name", "parent-cancel",
			"--config", configDir,
			"--pid-dir", pidDir,
		})
		result <- err
	}()

	// Handler 就绪后取消父 Context，平台监听必须退出且 PID lease 必须完整释放。
	<-started
	cancel()
	select {
	case err := <-result:
		if err != nil {
			t.Fatalf("start after parent cancellation error = %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatalf("start did not return after parent cancellation")
	}

	second, err := acquirePIDLease(pidDir, "parent-cancel")
	if err != nil {
		t.Fatalf("reacquire after parent cancellation error = %v", err)
	}
	if err := second.close(); err != nil {
		t.Fatalf("close reacquired lease error = %v", err)
	}
}

func TestDuplicateStartAndStopRoundTrip(t *testing.T) {
	// 该测试在 Unix 会向当前测试进程发送 SIGTERM，不能与其他信号测试并行。
	root := t.TempDir()
	configDir := filepath.Join(root, "config")
	pidDir := filepath.Join(root, "run")
	if err := os.Mkdir(configDir, 0o755); err != nil {
		t.Fatalf("Mkdir(config) error = %v", err)
	}

	started := make(chan struct{})
	targetResult := make(chan struct {
		code ExitCode
		err  error
	}, 1)
	target, _, _ := newTestRunner(t, func(ctx context.Context, _ StartRequest) error {
		close(started)
		<-ctx.Done()
		return nil
	})
	go func() {
		code, err := target.Run(context.Background(), []string{
			"start",
			"--app-name", "round-trip",
			"--config", configDir,
			"--pid-dir", pidDir,
		})
		targetResult <- struct {
			code ExitCode
			err  error
		}{code: code, err: err}
	}()
	<-started

	// 第二个 start 必须在调用 Handler 前被同一 PID 文件锁拒绝。
	duplicateCalled := false
	duplicate, _, _ := newTestRunner(t, func(context.Context, StartRequest) error {
		duplicateCalled = true
		return nil
	})
	code, err := duplicate.Run(context.Background(), []string{
		"start",
		"--app-name", "round-trip",
		"--config", configDir,
		"--pid-dir", pidDir,
	})
	if code != ExitProcessControl || !errors.Is(err, errs.ErrProcessAlreadyRunning) {
		t.Fatalf("duplicate start = (%d, %v), want process already running", code, err)
	}
	if duplicateCalled {
		t.Fatalf("duplicate start called its Handler")
	}

	// stop 使用平台入口取消目标 Context，并等待目标返回后释放锁。
	stopper, _, _ := newTestRunner(t, noOpStart)
	code, err = stopper.Run(context.Background(), []string{
		"stop",
		"--app-name", "round-trip",
		"--pid-dir", pidDir,
		"--timeout", "3s",
	})
	if err != nil || code != ExitSuccess {
		t.Fatalf("stop = (%d, %v), want success", code, err)
	}
	select {
	case result := <-targetResult:
		if result.err != nil || result.code != ExitSuccess {
			t.Fatalf("target result = (%d, %v), want success", result.code, result.err)
		}
	case <-time.After(3 * time.Second):
		t.Fatalf("target did not stop")
	}
}

func TestStopNotRunningIsIdempotent(t *testing.T) {
	t.Parallel()

	pidDir := filepath.Join(t.TempDir(), "missing")
	runner, stdout, _ := newTestRunner(t, noOpStart)
	code, err := runner.Run(context.Background(), []string{
		"stop",
		"--app-name", "missing-app",
		"--pid-dir", pidDir,
	})
	if err != nil || code != ExitSuccess {
		t.Fatalf("stop missing = (%d, %v), want success", code, err)
	}
	if stdout.String() != "not running\n" {
		t.Fatalf("stdout = %q, want %q", stdout.String(), "not running\n")
	}
	if _, statErr := os.Stat(pidDir); !os.IsNotExist(statErr) {
		t.Fatalf("stop created missing pid directory: %v", statErr)
	}
}

func TestStopTimeoutDoesNotTakeOwnership(t *testing.T) {
	// 与停止往返测试相同，本测试可能向当前 Unix 测试进程发送 SIGTERM。
	root := t.TempDir()
	configDir := filepath.Join(root, "config")
	pidDir := filepath.Join(root, "run")
	if err := os.Mkdir(configDir, 0o755); err != nil {
		t.Fatalf("Mkdir(config) error = %v", err)
	}

	started := make(chan struct{})
	release := make(chan struct{})
	targetResult := make(chan error, 1)
	target, _, _ := newTestRunner(t, func(context.Context, StartRequest) error {
		close(started)
		// 故意忽略取消，验证 stop timeout 不会强杀或偷取 PID 锁。
		<-release
		return nil
	})
	go func() {
		_, err := target.Run(context.Background(), []string{
			"start",
			"--app-name", "slow-stop",
			"--config", configDir,
			"--pid-dir", pidDir,
		})
		targetResult <- err
	}()
	<-started

	stopper, _, _ := newTestRunner(t, noOpStart)
	code, err := stopper.Run(context.Background(), []string{
		"stop",
		"--app-name", "slow-stop",
		"--pid-dir", pidDir,
		"--timeout", "75ms",
	})
	if code != ExitControlTimeout || !errs.IsCode(err, errs.CodeDeadlineExceeded) {
		t.Fatalf("stop timeout = (%d, %v), want timeout", code, err)
	}
	locked, inspectErr := isPIDLocked(filepath.Join(pidDir, "slow-stop.pid"))
	if inspectErr != nil || !locked {
		t.Fatalf("target lock after timeout = (%v, %v), want locked", locked, inspectErr)
	}

	close(release)
	select {
	case targetErr := <-targetResult:
		if targetErr != nil {
			t.Fatalf("target cleanup error = %v", targetErr)
		}
	case <-time.After(3 * time.Second):
		t.Fatalf("target did not finish after test release")
	}
}

func TestNilContextIsUsageError(t *testing.T) {
	t.Parallel()

	runner, _, _ := newTestRunner(t, noOpStart)
	code, err := runner.Run(nil, []string{"help"})
	if code != ExitUsage || !errs.IsCode(err, errs.CodeInvalidArgument) {
		t.Fatalf("Run(nil) = (%d, %v), want usage error", code, err)
	}
}
