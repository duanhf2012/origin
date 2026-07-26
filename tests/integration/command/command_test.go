package command_test

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

const processWaitTimeout = 5 * time.Second
const externalHelperEnv = "ORIGIN_COMMAND_TEST_HELPER"

var (
	helperBinary string
	helperDir    string
	repository   string
)

// targetProcess 保存一个正在持有 M4 PID 锁的测试辅助进程。
type targetProcess struct {
	command *exec.Cmd
	done    chan error
	stdout  *bytes.Buffer
	stderr  *bytes.Buffer
	waited  bool
	waitErr error
}

// TestMain 只构建一次当前平台辅助程序，所有测试共享二进制但使用独立控制目录。
func TestMain(m *testing.M) {
	// Linux 远程机没有 Go 工具链时，允许测试驱动使用已经交叉编译并同目录上传的辅助程序。
	// 该入口只改变测试装配方式，不绕过任何 command 公开 API 或平台行为。
	if externalHelper := os.Getenv(externalHelperEnv); externalHelper != "" {
		helperBinary = externalHelper
		if _, err := os.Stat(helperBinary); err != nil {
			fmt.Fprintf(os.Stderr, "inspect external command helper: %v\n", err)
			os.Exit(1)
		}
		os.Exit(m.Run())
	}

	// 从当前测试源码位置确定仓库根，避免依赖 go test 的当前工作目录细节。
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		fmt.Fprintln(os.Stderr, "locate integration test source failed")
		os.Exit(1)
	}
	repository = filepath.Clean(filepath.Join(filepath.Dir(file), "..", "..", ".."))

	var err error
	helperDir, err = os.MkdirTemp("", "origin-command-helper-")
	if err != nil {
		fmt.Fprintf(os.Stderr, "create helper directory: %v\n", err)
		os.Exit(1)
	}

	name := "commandprocess"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	helperBinary = filepath.Join(helperDir, name)
	build := exec.Command("go", "build", "-o", helperBinary, "./tests/helpers/commandprocess")
	build.Dir = repository
	if output, buildErr := build.CombinedOutput(); buildErr != nil {
		fmt.Fprintf(os.Stderr, "build command helper: %v\n%s", buildErr, output)
		_ = os.RemoveAll(helperDir)
		os.Exit(1)
	}

	// os.Exit 不执行 defer，因此先保存测试退出码并显式清理临时辅助二进制。
	code := m.Run()
	_ = os.RemoveAll(helperDir)
	os.Exit(code)
}

func TestGracefulStopAcrossProcesses(t *testing.T) {
	root, configDir, pidDir := makeControlDirs(t)
	target := startTarget(t, root, configDir, pidDir, "graceful", "normal")
	defer target.cleanup()

	// 独立 stop 进程必须送达平台通知并等到目标 Handler 返回、PID 锁释放。
	code, stdout, stderr := runHelper(
		t,
		nil,
		"stop",
		"--app-name", "graceful",
		"--pid-dir", pidDir,
		"--timeout", "3s",
	)
	if code != 0 {
		t.Fatalf("stop exit = %d\nstdout:\n%s\nstderr:\n%s", code, stdout, stderr)
	}
	if err := target.wait(processWaitTimeout); err != nil {
		t.Fatalf("target exit error = %v\nstdout:\n%s\nstderr:\n%s", err, target.stdout, target.stderr)
	}
}

func TestDuplicateStartAcrossProcesses(t *testing.T) {
	root, configDir, pidDir := makeControlDirs(t)
	target := startTarget(t, root, configDir, pidDir, "duplicate", "normal")
	defer target.cleanup()

	// 第二个进程即使 Handler 配置为立即返回，也必须在进入 Handler 前以退出码 3 被拒绝。
	code, stdout, stderr := runHelper(
		t,
		[]string{"ORIGIN_COMMAND_TEST_MODE=immediate"},
		"start",
		"--app-name", "duplicate",
		"--config", configDir,
		"--pid-dir", pidDir,
	)
	if code != 3 || !strings.Contains(stderr, "already running") {
		t.Fatalf("duplicate exit = %d\nstdout:\n%s\nstderr:\n%s", code, stdout, stderr)
	}

	// 用正常 stop 收尾，同时再次覆盖跨进程通知和锁释放。
	code, stdout, stderr = runHelper(
		t,
		nil,
		"stop",
		"--app-name", "duplicate",
		"--pid-dir", pidDir,
		"--timeout", "3s",
	)
	if code != 0 {
		t.Fatalf("cleanup stop exit = %d\nstdout:\n%s\nstderr:\n%s", code, stdout, stderr)
	}
	if err := target.wait(processWaitTimeout); err != nil {
		t.Fatalf("target exit error = %v", err)
	}
}

func TestStopTimeoutLeavesTargetRunning(t *testing.T) {
	root, configDir, pidDir := makeControlDirs(t)
	target := startTarget(t, root, configDir, pidDir, "ignore-stop", "ignore")
	defer target.cleanup()

	// 忽略 Run Context 的目标应使外部 stop 返回稳定退出码 4，而不是被强制终止。
	code, stdout, stderr := runHelper(
		t,
		nil,
		"stop",
		"--app-name", "ignore-stop",
		"--pid-dir", pidDir,
		"--timeout", "100ms",
	)
	if code != 4 || !strings.Contains(stderr, "exceeded") {
		t.Fatalf("timeout exit = %d\nstdout:\n%s\nstderr:\n%s", code, stdout, stderr)
	}
	select {
	case err := <-target.done:
		target.waited = true
		target.waitErr = err
		t.Fatalf("target exited after timeout: %v", err)
	default:
		// 目标仍运行就是本测试的核心断言，最终由 cleanup 强制回收测试进程。
	}
}

func TestAbnormalExitReleasesPIDLock(t *testing.T) {
	root, configDir, pidDir := makeControlDirs(t)
	target := startTarget(t, root, configDir, pidDir, "crash-release", "ignore")

	// 测试进程模拟崩溃被终止，操作系统必须自动回收文件句柄和独占锁。
	if err := target.command.Process.Kill(); err != nil {
		t.Fatalf("kill target error = %v", err)
	}
	if err := target.wait(processWaitTimeout); err == nil {
		t.Fatalf("killed target unexpectedly exited without error")
	}

	// 遗留 PID 文件存在，但新的 start 仍应取得锁、覆盖记录并正常结束。
	code, stdout, stderr := runHelper(
		t,
		[]string{"ORIGIN_COMMAND_TEST_MODE=immediate"},
		"start",
		"--app-name", "crash-release",
		"--config", configDir,
		"--pid-dir", pidDir,
	)
	if code != 0 {
		t.Fatalf("restart exit = %d\nstdout:\n%s\nstderr:\n%s", code, stdout, stderr)
	}
}

func TestStopMissingTargetIsIdempotent(t *testing.T) {
	root := t.TempDir()
	missingPIDDir := filepath.Join(root, "missing")

	// 不存在的 PID 目录不能被 stop 顺手创建，并返回稳定的幂等提示。
	code, stdout, stderr := runHelper(
		t,
		nil,
		"stop",
		"--app-name", "missing-target",
		"--pid-dir", missingPIDDir,
	)
	if code != 0 || stdout != "not running\n" || stderr != "" {
		t.Fatalf("missing stop exit = %d\nstdout:%q\nstderr:%q", code, stdout, stderr)
	}
	if _, err := os.Stat(missingPIDDir); !os.IsNotExist(err) {
		t.Fatalf("stop created missing pid directory: %v", err)
	}
}

// makeControlDirs 为每个测试建立独立配置目录和 PID 目录。
func makeControlDirs(t *testing.T) (root string, configDir string, pidDir string) {
	t.Helper()

	root = t.TempDir()
	configDir = filepath.Join(root, "config")
	pidDir = filepath.Join(root, "run")
	if err := os.Mkdir(configDir, 0o755); err != nil {
		t.Fatalf("create config directory: %v", err)
	}
	return root, configDir, pidDir
}

// startTarget 启动并等待辅助进程进入 Start Handler。
func startTarget(
	t *testing.T,
	root string,
	configDir string,
	pidDir string,
	appName string,
	mode string,
) *targetProcess {
	t.Helper()

	readyFile := filepath.Join(root, appName+".ready")
	command := exec.Command(
		helperBinary,
		"start",
		"--app-name", appName,
		"--config", configDir,
		"--pid-dir", pidDir,
	)
	command.Env = append(
		os.Environ(),
		"ORIGIN_COMMAND_TEST_READY_FILE="+readyFile,
		"ORIGIN_COMMAND_TEST_MODE="+mode,
	)
	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}
	command.Stdout = stdout
	command.Stderr = stderr
	if err := command.Start(); err != nil {
		t.Fatalf("start target process: %v", err)
	}

	target := &targetProcess{
		command: command,
		done:    make(chan error, 1),
		stdout:  stdout,
		stderr:  stderr,
	}
	go func() {
		target.done <- command.Wait()
	}()

	deadline := time.Now().Add(processWaitTimeout)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(readyFile); err == nil {
			return target
		}
		select {
		case err := <-target.done:
			target.waited = true
			target.waitErr = err
			t.Fatalf(
				"target exited before ready: %v\nstdout:\n%s\nstderr:\n%s",
				err,
				stdout,
				stderr,
			)
		default:
		}
		time.Sleep(10 * time.Millisecond)
	}

	target.cleanup()
	t.Fatalf("target did not become ready\nstdout:\n%s\nstderr:\n%s", stdout, stderr)
	return nil
}

// wait 等待目标退出一次，并缓存结果供 cleanup 幂等复用。
func (target *targetProcess) wait(timeout time.Duration) error {
	if target.waited {
		return target.waitErr
	}
	select {
	case target.waitErr = <-target.done:
		target.waited = true
		return target.waitErr
	case <-time.After(timeout):
		return fmt.Errorf("wait target timeout after %s", timeout)
	}
}

// cleanup 强制回收仍在运行的测试辅助进程，避免失败测试遗留后台进程和文件锁。
func (target *targetProcess) cleanup() {
	if target == nil || target.waited {
		return
	}
	select {
	case target.waitErr = <-target.done:
		target.waited = true
		return
	default:
	}

	_ = target.command.Process.Kill()
	_ = target.wait(processWaitTimeout)
}

// runHelper 同步执行一次辅助命令并把进程退出状态转换为可直接断言的整数。
func runHelper(
	t *testing.T,
	extraEnv []string,
	args ...string,
) (code int, stdout string, stderr string) {
	t.Helper()

	command := exec.Command(helperBinary, args...)
	command.Env = append(os.Environ(), extraEnv...)
	stdoutBuffer := &bytes.Buffer{}
	stderrBuffer := &bytes.Buffer{}
	command.Stdout = stdoutBuffer
	command.Stderr = stderrBuffer
	err := command.Run()
	if err == nil {
		return 0, stdoutBuffer.String(), stderrBuffer.String()
	}

	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return exitErr.ExitCode(), stdoutBuffer.String(), stderrBuffer.String()
	}
	t.Fatalf("run helper %v: %v", args, err)
	return -1, stdoutBuffer.String(), stderrBuffer.String()
}
