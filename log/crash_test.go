package log

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

const (
	// 两个环境变量区分父/子测试进程并传递临时 Crash 路径。
	crashHelperEnvironment = "ORIGIN_CRASH_TEST_HELPER"
	crashPathEnvironment   = "ORIGIN_CRASH_TEST_PATH"
)

func TestCrashPath(t *testing.T) {
	t.Parallel()

	// 同时覆盖有扩展名和无扩展名活动路径。
	tests := map[string]string{
		"logs/origin.log": "logs/origin.crash.log",
		"logs/server":     "logs/server.crash.log",
	}
	// 统一路径分隔符后比较，保证跨平台测试稳定。
	for input, want := range tests {
		if got := filepath.ToSlash(crashPath(input)); got != filepath.ToSlash(want) {
			t.Errorf("crashPath(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestCrashOutputCloseIsIdempotent(t *testing.T) {
	// 建立启用文件输出的有效临时配置。
	config := DefaultConfig()
	config.File.Enabled = true
	config.File.Path = filepath.Join(t.TempDir(), "origin.log")

	// 安装后连续关闭两次，两个调用都必须成功。
	output, err := InstallCrashOutput(config.File)
	if err != nil {
		t.Fatalf("InstallCrashOutput() = %v", err)
	}
	if err := output.Close(); err != nil {
		t.Fatalf("Close() = %v", err)
	}
	if err := output.Close(); err != nil {
		t.Fatalf("second Close() = %v", err)
	}
}

func TestCrashOutputCapturesUnrecoveredPanic(t *testing.T) {
	// 子进程分支安装 Crash 输出后制造无法恢复的 panic。
	if os.Getenv(crashHelperEnvironment) == "1" {
		runCrashHelper()
		return
	}

	// 父进程使用当前测试二进制启动隔离子进程，避免终止测试主进程。
	activePath := filepath.Join(t.TempDir(), "origin.log")
	command := exec.Command(os.Args[0], "-test.run=^TestCrashOutputCapturesUnrecoveredPanic$")
	command.Env = append(
		os.Environ(),
		crashHelperEnvironment+"=1",
		crashPathEnvironment+"="+activePath,
	)
	// 未恢复 panic 必须使子进程失败，且 stderr 仍包含原始 panic。
	output, err := command.CombinedOutput()
	if err == nil {
		t.Fatalf("crash helper unexpectedly succeeded")
	}
	if !strings.Contains(string(output), "origin crash test") {
		t.Fatalf("stderr does not contain panic: %s", output)
	}

	// debug Crash 文件也必须包含同一 panic 文本。
	content, err := os.ReadFile(crashPath(activePath))
	if err != nil {
		t.Fatalf("ReadFile(crash) = %v", err)
	}
	if !strings.Contains(string(content), "origin crash test") {
		t.Fatalf("crash file does not contain panic: %s", content)
	}
}

func runCrashHelper() {
	// 从父进程环境读取临时路径并安装进程级 Crash 输出。
	config := DefaultConfig()
	config.File.Enabled = true
	config.File.Path = os.Getenv(crashPathEnvironment)
	output, err := InstallCrashOutput(config.File)
	if err != nil {
		panic(err)
	}
	// 保持注册对象存活，然后触发真实未恢复 panic。
	_ = output
	panic("origin crash test")
}
