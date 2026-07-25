package log

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

const (
	crashHelperEnvironment = "ORIGIN_CRASH_TEST_HELPER"
	crashPathEnvironment   = "ORIGIN_CRASH_TEST_PATH"
)

func TestCrashPath(t *testing.T) {
	t.Parallel()

	tests := map[string]string{
		"logs/origin.log": "logs/origin.crash.log",
		"logs/server":     "logs/server.crash.log",
	}
	for input, want := range tests {
		if got := filepath.ToSlash(crashPath(input)); got != filepath.ToSlash(want) {
			t.Errorf("crashPath(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestCrashOutputCloseIsIdempotent(t *testing.T) {
	config := DefaultConfig()
	config.File.Enabled = true
	config.File.Path = filepath.Join(t.TempDir(), "origin.log")

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
	if os.Getenv(crashHelperEnvironment) == "1" {
		runCrashHelper()
		return
	}

	activePath := filepath.Join(t.TempDir(), "origin.log")
	command := exec.Command(os.Args[0], "-test.run=^TestCrashOutputCapturesUnrecoveredPanic$")
	command.Env = append(
		os.Environ(),
		crashHelperEnvironment+"=1",
		crashPathEnvironment+"="+activePath,
	)
	output, err := command.CombinedOutput()
	if err == nil {
		t.Fatalf("crash helper unexpectedly succeeded")
	}
	if !strings.Contains(string(output), "origin crash test") {
		t.Fatalf("stderr does not contain panic: %s", output)
	}

	content, err := os.ReadFile(crashPath(activePath))
	if err != nil {
		t.Fatalf("ReadFile(crash) = %v", err)
	}
	if !strings.Contains(string(content), "origin crash test") {
		t.Fatalf("crash file does not contain panic: %s", content)
	}
}

func runCrashHelper() {
	config := DefaultConfig()
	config.File.Enabled = true
	config.File.Path = os.Getenv(crashPathEnvironment)
	output, err := InstallCrashOutput(config.File)
	if err != nil {
		panic(err)
	}
	_ = output
	panic("origin crash test")
}
