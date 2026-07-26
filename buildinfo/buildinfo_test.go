package buildinfo_test

import (
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/duanhf2012/origin/v3/buildinfo"
)

func TestDefaultsAreEmpty(t *testing.T) {
	t.Parallel()

	// 未通过链接器注入时，三个访问器都应保持明确空值。
	if got := buildinfo.BuildTime(); got != "" {
		t.Fatalf("BuildTime() = %q, want empty", got)
	}
	if got := buildinfo.Version(); got != "" {
		t.Fatalf("Version() = %q, want empty", got)
	}
	if got := buildinfo.Commit(); got != "" {
		t.Fatalf("Commit() = %q, want empty", got)
	}
}

func TestLinkerInjection(t *testing.T) {
	// 使用固定样本，分别覆盖时间、版本和提交三个 -X 目标。
	const (
		wantBuildTime = "2026-07-25T12:00:00+08:00"
		wantVersion   = "v3.0.0-test"
		wantCommit    = "abcdef123456"
	)

	// 子进程负责断言本次 test 二进制中已经注入的变量。
	if os.Getenv("ORIGIN_BUILDINFO_TEST_SUBPROCESS") == "1" {
		if got := buildinfo.BuildTime(); got != wantBuildTime {
			t.Fatalf("BuildTime() = %q, want %q", got, wantBuildTime)
		}
		if got := buildinfo.Version(); got != wantVersion {
			t.Fatalf("Version() = %q, want %q", got, wantVersion)
		}
		if got := buildinfo.Commit(); got != wantCommit {
			t.Fatalf("Commit() = %q, want %q", got, wantCommit)
		}
		return
	}

	// 父进程组装与生产构建脚本相同形式的 ldflags。
	ldflags := strings.Join([]string{
		"-X=github.com/duanhf2012/origin/v3/buildinfo.buildTime=" + wantBuildTime,
		"-X=github.com/duanhf2012/origin/v3/buildinfo.version=" + wantVersion,
		"-X=github.com/duanhf2012/origin/v3/buildinfo.commit=" + wantCommit,
	}, " ")

	// 重新构建并只运行当前测试，避免父进程自身未注入状态干扰。
	cmd := exec.Command(
		"go",
		"test",
		"-count=1",
		"-run=^TestLinkerInjection$",
		"-ldflags",
		ldflags,
		".",
	)
	// 环境标记阻止子进程再次递归创建测试进程。
	cmd.Env = append(os.Environ(), "ORIGIN_BUILDINFO_TEST_SUBPROCESS=1")

	// 同时捕获标准输出和错误，失败时保留完整构建诊断。
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("run buildinfo subprocess test: %v\n%s", err, output)
	}
}
