package command

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/duanhf2012/origin/v3/errs"
)

// errorWriter 为帮助、版本和状态输出提供稳定可触发的 I/O 错误。
type errorWriter struct {
	err error
}

// Write 始终返回测试指定错误，验证 Runner 不吞掉输出失败。
func (writer errorWriter) Write([]byte) (int, error) {
	return 0, writer.err
}

// panicWriter 验证 Runner 最外层 panic 边界，不模拟普通 I/O 错误。
type panicWriter struct{}

// Write 故意 panic，让测试确认框架解析和输出路径也能转换为 CodeInternal。
func (panicWriter) Write([]byte) (int, error) {
	panic("writer panic")
}

func TestHelpCoversBuiltInCustomAndErrors(t *testing.T) {
	t.Parallel()

	runner, stdout, _ := newTestRunner(t, noOpStart)
	if err := runner.Register(Command{
		Name:    "doctor",
		Summary: "诊断",
		Usage:   "game-server doctor",
		Run:     func(Context, []string) error { return nil },
	}); err != nil {
		t.Fatalf("Register() error = %v", err)
	}

	// 所有内置分支和自定义命令帮助都必须返回成功且写出非空内容。
	for _, name := range []string{"start", "stop", "help", "version", "doctor"} {
		stdout.Reset()
		code, err := runner.Run(context.Background(), []string{"help", name})
		if err != nil || code != ExitSuccess {
			t.Fatalf("help %s = (%d, %v), want success", name, code, err)
		}
		if stdout.Len() == 0 {
			t.Fatalf("help %s wrote no output", name)
		}
	}

	// 多余参数和不存在的命令必须是用法错误，不能静默显示总帮助。
	for _, args := range [][]string{
		{"help", "start", "extra"},
		{"help", "missing"},
	} {
		code, err := runner.Run(context.Background(), args)
		if code != ExitUsage || !errs.IsCode(err, errs.CodeInvalidArgument) {
			t.Fatalf("Run(%v) = (%d, %v), want usage error", args, code, err)
		}
	}

	// 内置命令自己的 --help 同样不得初始化配置目录或 PID 资源。
	for _, args := range [][]string{
		{"start", "--help"},
		{"stop", "--help"},
	} {
		code, err := runner.Run(context.Background(), args)
		if err != nil || code != ExitSuccess {
			t.Fatalf("Run(%v) = (%d, %v), want help success", args, code, err)
		}
	}
}

func TestOutputFailuresAndRunnerOuterPanic(t *testing.T) {
	t.Parallel()

	outputErr := errors.New("output failed")
	tests := []struct {
		name string
		args []string
	}{
		{name: "general help", args: []string{"help"}},
		{name: "command help", args: []string{"help", "start"}},
		{name: "version", args: []string{"version"}},
		{
			name: "not running status",
			args: []string{"stop", "--app-name", "missing", "--pid-dir", filepath.Join(t.TempDir(), "none")},
		},
	}
	for _, test := range tests {
		runner, newErr := New(Options{
			ProgramName: "game-server",
			Stdout:      errorWriter{err: outputErr},
			Start:       noOpStart,
		})
		if newErr != nil {
			t.Fatalf("New() error = %v", newErr)
		}
		code, err := runner.Run(context.Background(), test.args)
		if code != ExitFailure || !errors.Is(err, outputErr) {
			t.Errorf("%s = (%d, %v), want output failure", test.name, code, err)
		}
	}

	// panic writer 发生在普通框架输出路径，应由 Run 最外层转换而不是逃出测试进程。
	runner, err := New(Options{
		ProgramName: "game-server",
		Stdout:      panicWriter{},
		Start:       noOpStart,
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	code, runErr := runner.Run(context.Background(), []string{"help"})
	if code != ExitFailure || !errs.IsCode(runErr, errs.CodeInternal) {
		t.Fatalf("outer panic = (%d, %v), want ExitFailure/CodeInternal", code, runErr)
	}
}

func TestNewDefaultsAndSmallHelpers(t *testing.T) {
	t.Parallel()

	runner, err := New(Options{Start: noOpStart})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if runner.programName == "" || runner.stdin == nil || runner.stdout == nil || runner.stderr == nil {
		t.Fatalf("New() did not populate defaults: %#v", runner)
	}
	if got := unknownIfEmpty("v3"); got != "v3" {
		t.Fatalf("unknownIfEmpty(v3) = %q", got)
	}
	if _, exists := runner.builtInHelp("missing"); exists {
		t.Fatalf("builtInHelp(missing) unexpectedly exists")
	}
	if platformProcessGone(errors.New("ordinary")) {
		t.Fatalf("platformProcessGone(ordinary) = true")
	}

	// 直接锁定两个错误包装辅助函数的 nil 与非 nil 分支。
	if err := wrapControlCleanup("game", nil); err != nil {
		t.Fatalf("wrapControlCleanup(nil) = %v", err)
	}
	if err := wrapLeaseCleanup("game.pid", nil); err != nil {
		t.Fatalf("wrapLeaseCleanup(nil) = %v", err)
	}
	if err := wrapControlCleanup("game", errors.New("close failed")); !errs.IsCode(
		err,
		errs.CodeProcessControlFailed,
	) {
		t.Fatalf("wrapControlCleanup(error) = %v", err)
	}
	if err := wrapLeaseCleanup("game.pid", errors.New("unlock failed")); !errs.IsCode(
		err,
		errs.CodeProcessControlFailed,
	) {
		t.Fatalf("wrapLeaseCleanup(error) = %v", err)
	}
	if err := processControlf("control %s", "failed"); !errs.IsCode(err, errs.CodeProcessControlFailed) {
		t.Fatalf("processControlf() = %v", err)
	}
	joined := joinExecutionErrors(nil, nil, errors.New("cleanup"))
	if joined == nil || !strings.Contains(joined.Error(), "cleanup") {
		t.Fatalf("joinExecutionErrors() = %v", joined)
	}
}

func TestCustomCommandErrorIsPreserved(t *testing.T) {
	t.Parallel()

	sentinel := errors.New("custom failed")
	runner, _, _ := newTestRunner(t, noOpStart)
	if err := runner.Register(Command{
		Name:    "fail-command",
		Summary: "失败",
		Usage:   "game-server fail-command",
		Run:     func(Context, []string) error { return sentinel },
	}); err != nil {
		t.Fatalf("Register() error = %v", err)
	}

	code, err := runner.Run(context.Background(), []string{"fail-command"})
	if code != ExitFailure || !errors.Is(err, sentinel) {
		t.Fatalf("custom failure = (%d, %v), want preserved sentinel", code, err)
	}
}

func TestStartFlagHandlerAndPIDDirectoryErrors(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	configDir := filepath.Join(root, "config")
	if err := os.Mkdir(configDir, 0o755); err != nil {
		t.Fatalf("Mkdir(config) error = %v", err)
	}

	// flag 解析、多余位置参数和 PID 目录创建错误分别走不同退出阶段。
	tests := []struct {
		name     string
		args     []string
		wantCode ExitCode
		wantErr  errs.Code
	}{
		{
			name:     "unknown flag",
			args:     []string{"start", "--unknown"},
			wantCode: ExitUsage,
			wantErr:  errs.CodeInvalidArgument,
		},
		{
			name: "positional",
			args: []string{
				"start", "--app-name", "game", "--config", configDir, "extra",
			},
			wantCode: ExitUsage,
			wantErr:  errs.CodeInvalidArgument,
		},
	}
	for _, test := range tests {
		runner, _, _ := newTestRunner(t, noOpStart)
		code, err := runner.Run(context.Background(), test.args)
		if code != test.wantCode || errs.CodeOf(err) != test.wantErr {
			t.Errorf("%s = (%d, %v), want (%d, %d)", test.name, code, err, test.wantCode, test.wantErr)
		}
	}

	// 让 PID 目录的父路径成为普通文件，稳定触发 MkdirAll 失败。
	blocker := filepath.Join(root, "pid-parent")
	if err := os.WriteFile(blocker, []byte("file"), 0o600); err != nil {
		t.Fatalf("WriteFile(blocker) error = %v", err)
	}
	runner, _, _ := newTestRunner(t, noOpStart)
	code, err := runner.Run(context.Background(), []string{
		"start",
		"--app-name", "game",
		"--config", configDir,
		"--pid-dir", filepath.Join(blocker, "run"),
	})
	if code != ExitProcessControl || !errs.IsCode(err, errs.CodeProcessControlFailed) {
		t.Fatalf("pid directory failure = (%d, %v)", code, err)
	}

	// Handler error必须保持原错误链，并在返回前释放 PID lease。
	handlerErr := errors.New("application failed")
	handlerRunner, _, _ := newTestRunner(t, func(context.Context, StartRequest) error {
		return handlerErr
	})
	pidDir := filepath.Join(root, "handler-run")
	code, err = handlerRunner.Run(context.Background(), []string{
		"start",
		"--app-name", "handler-fail",
		"--config", configDir,
		"--pid-dir", pidDir,
	})
	if code != ExitFailure || !errors.Is(err, handlerErr) {
		t.Fatalf("handler failure = (%d, %v), want preserved error", code, err)
	}
	lease, acquireErr := acquirePIDLease(pidDir, "handler-fail")
	if acquireErr != nil {
		t.Fatalf("handler failure leaked pid lease: %v", acquireErr)
	}
	if closeErr := lease.close(); closeErr != nil {
		t.Fatalf("close reacquired lease error = %v", closeErr)
	}
}

func TestStopArgumentAndPIDPathErrors(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	pidFileInsteadOfDir := filepath.Join(root, "pid-file")
	if err := os.WriteFile(pidFileInsteadOfDir, []byte("file"), 0o600); err != nil {
		t.Fatalf("WriteFile(pid path) error = %v", err)
	}

	tests := []struct {
		name     string
		args     []string
		wantCode ExitCode
		wantErr  errs.Code
	}{
		{
			name:     "unknown flag",
			args:     []string{"stop", "--unknown"},
			wantCode: ExitUsage,
			wantErr:  errs.CodeInvalidArgument,
		},
		{
			name:     "positional",
			args:     []string{"stop", "--app-name", "game", "extra"},
			wantCode: ExitUsage,
			wantErr:  errs.CodeInvalidArgument,
		},
		{
			name:     "missing app",
			args:     []string{"stop"},
			wantCode: ExitUsage,
			wantErr:  errs.CodeInvalidArgument,
		},
		{
			name:     "invalid duration",
			args:     []string{"stop", "--app-name", "game", "--timeout", "later"},
			wantCode: ExitUsage,
			wantErr:  errs.CodeInvalidArgument,
		},
		{
			name:     "zero duration",
			args:     []string{"stop", "--app-name", "game", "--timeout", "0s"},
			wantCode: ExitUsage,
			wantErr:  errs.CodeInvalidArgument,
		},
		{
			name: "pid path is file",
			args: []string{
				"stop", "--app-name", "game", "--pid-dir", pidFileInsteadOfDir,
			},
			wantCode: ExitProcessControl,
			wantErr:  errs.CodeProcessControlFailed,
		},
	}

	for _, test := range tests {
		runner, _, _ := newTestRunner(t, noOpStart)
		code, err := runner.Run(context.Background(), test.args)
		if code != test.wantCode || errs.CodeOf(err) != test.wantErr {
			t.Errorf("%s = (%d, %v), want (%d, %d)", test.name, code, err, test.wantCode, test.wantErr)
		}
	}
}

func TestActivePIDRecordFailures(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		data []byte
	}{
		{name: "malformed", data: []byte(`{"pid":`)},
		{name: "too large", data: bytes.Repeat([]byte("x"), maxPIDRecordSize+1)},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			pidDir := t.TempDir()
			lease, err := acquirePIDLease(pidDir, "broken")
			if err != nil {
				t.Fatalf("acquirePIDLease() error = %v", err)
			}
			defer func() {
				_ = lease.close()
			}()

			// 在仍持锁的同一文件句柄上写入损坏内容，模拟崩溃或外部破坏。
			if err := lease.file.Truncate(0); err != nil {
				t.Fatalf("Truncate() error = %v", err)
			}
			if _, err := lease.file.Seek(0, 0); err != nil {
				t.Fatalf("Seek() error = %v", err)
			}
			if _, err := lease.file.Write(test.data); err != nil {
				t.Fatalf("Write() error = %v", err)
			}
			if err := lease.file.Sync(); err != nil {
				t.Fatalf("Sync() error = %v", err)
			}

			runner, _, _ := newTestRunner(t, noOpStart)
			code, stopErr := runner.Run(context.Background(), []string{
				"stop",
				"--app-name", "broken",
				"--pid-dir", pidDir,
				"--timeout", "50ms",
			})
			if code != ExitProcessControl || !errs.IsCode(stopErr, errs.CodeProcessControlFailed) {
				t.Fatalf("stop broken record = (%d, %v), want process control failure", code, stopErr)
			}
		})
	}
}

func TestWaitForPIDUnlockHonorsParentCancellation(t *testing.T) {
	t.Parallel()

	pidDir := t.TempDir()
	lease, err := acquirePIDLease(pidDir, "cancel-wait")
	if err != nil {
		t.Fatalf("acquirePIDLease() error = %v", err)
	}
	defer func() {
		_ = lease.close()
	}()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	timedOut, waitErr := waitForPIDUnlock(
		ctx,
		pidFilePath(pidDir, "cancel-wait"),
		time.Second,
	)
	if timedOut || !errs.IsCode(waitErr, errs.CodeCanceled) {
		t.Fatalf("waitForPIDUnlock() = (%v, %v), want canceled", timedOut, waitErr)
	}
}

func TestDirectoryValidationBranches(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	filePath := filepath.Join(root, "file")
	if err := os.WriteFile(filePath, []byte("file"), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	// 配置目录覆盖空值、不存在和普通文件三种稳定错误。
	for _, path := range []string{" ", filepath.Join(root, "missing"), filePath} {
		if _, err := resolveExistingDir(path, "config directory"); err == nil {
			t.Errorf("resolveExistingDir(%q) error = nil", path)
		}
	}

	// start PID 目录覆盖空值和普通文件，stop 另外覆盖空值与不存在。
	if _, err := resolvePIDDir(" "); err == nil {
		t.Fatalf("resolvePIDDir(empty) error = nil")
	}
	if _, err := resolvePIDDir(filePath); err == nil {
		t.Fatalf("resolvePIDDir(file) error = nil")
	}
	if _, _, err := resolvePIDDirForStop(" "); err == nil {
		t.Fatalf("resolvePIDDirForStop(empty) error = nil")
	}
	absolute, exists, err := resolvePIDDirForStop(filepath.Join(root, "missing"))
	if err != nil || exists || !filepath.IsAbs(absolute) {
		t.Fatalf("resolvePIDDirForStop(missing) = (%q, %v, %v)", absolute, exists, err)
	}
}

func TestClosedPIDFilesReturnControlErrors(t *testing.T) {
	t.Parallel()

	// 已关闭句柄稳定触发 Truncate 和平台加锁错误，不需要伪造操作系统状态。
	file, err := os.CreateTemp(t.TempDir(), "closed-*.pid")
	if err != nil {
		t.Fatalf("CreateTemp() error = %v", err)
	}
	path := file.Name()
	if err := file.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	lease := &pidLease{file: file, path: path}
	if err := lease.writeRecord(); err == nil {
		t.Fatalf("writeRecord(closed file) error = nil")
	}

	// 把目录路径当作 PID 文件传入，必须返回进程控制错误而不是 panic。
	if _, _, err := readRunningPID(t.TempDir()); err == nil {
		t.Fatalf("readRunningPID(directory) error = nil")
	}
	if _, err := isPIDLocked(t.TempDir()); err == nil {
		t.Fatalf("isPIDLocked(directory) error = nil")
	}

	// 普通文件不能作为 PID 目录，稳定覆盖 acquirePIDLease 的 OpenFile 失败路径。
	parentFile := filepath.Join(t.TempDir(), "pid-parent")
	if err := os.WriteFile(parentFile, nil, 0o600); err != nil {
		t.Fatalf("WriteFile(parent) error = %v", err)
	}
	if _, err := acquirePIDLease(parentFile, "game"); err == nil {
		t.Fatalf("acquirePIDLease(file parent) error = nil")
	}
}
