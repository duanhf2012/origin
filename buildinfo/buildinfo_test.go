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
	const (
		wantBuildTime = "2026-07-25T12:00:00+08:00"
		wantVersion   = "v3.0.0-test"
		wantCommit    = "abcdef123456"
	)

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

	ldflags := strings.Join([]string{
		"-X=github.com/duanhf2012/origin/v3/buildinfo.buildTime=" + wantBuildTime,
		"-X=github.com/duanhf2012/origin/v3/buildinfo.version=" + wantVersion,
		"-X=github.com/duanhf2012/origin/v3/buildinfo.commit=" + wantCommit,
	}, " ")

	cmd := exec.Command(
		"go",
		"test",
		"-count=1",
		"-run=^TestLinkerInjection$",
		"-ldflags",
		ldflags,
		".",
	)
	cmd.Env = append(os.Environ(), "ORIGIN_BUILDINFO_TEST_SUBPROCESS=1")

	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("run buildinfo subprocess test: %v\n%s", err, output)
	}
}
