package command

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestValidateKebabName(t *testing.T) {
	t.Parallel()

	// 表格锁定合法边界以及所有容易误写成路径或多套命名风格的情况。
	valid := []string{"a", "game", "game-1", strings.Repeat("a", maxKebabNameLength)}
	for _, name := range valid {
		if err := validateKebabName(name, "name"); err != nil {
			t.Errorf("validateKebabName(%q) error = %v", name, err)
		}
	}

	invalid := []string{
		"",
		"1game",
		"Game",
		"game_1",
		"game--1",
		"game-",
		"game.1",
		strings.Repeat("a", maxKebabNameLength+1),
	}
	for _, name := range invalid {
		if err := validateKebabName(name, "name"); err == nil {
			t.Errorf("validateKebabName(%q) error = nil", name)
		}
	}
}

func TestDecodePIDRecordStrictly(t *testing.T) {
	t.Parallel()

	now := time.Now().Format(time.RFC3339)
	valid := `{"pid":123,"started_at":"` + now + `"}`
	record, err := decodePIDRecord([]byte(valid))
	if err != nil || record.PID != 123 {
		t.Fatalf("decode valid record = (%#v, %v)", record, err)
	}

	// 未知字段、非法 PID、非法时间和拼接 JSON 都必须拒绝。
	invalid := []string{
		`{"pid":123,"started_at":"` + now + `","state":"running"}`,
		`{"pid":0,"started_at":"` + now + `"}`,
		`{"pid":123,"started_at":"local time"}`,
		valid + `{}`,
	}
	for _, data := range invalid {
		if _, err := decodePIDRecord([]byte(data)); err == nil {
			t.Errorf("decodePIDRecord(%q) error = nil", data)
		}
	}
}

func TestIdlePIDInspectionDoesNotOverwriteRecord(t *testing.T) {
	t.Parallel()

	// 写入遗留诊断内容后检查空闲锁，内容必须保持逐字节不变。
	pidPath := filepath.Join(t.TempDir(), "game.pid")
	content := []byte(`{"pid":321,"started_at":"2026-07-26T12:00:00+08:00"}`)
	if err := os.WriteFile(pidPath, content, 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	running, _, err := readRunningPID(pidPath)
	if err != nil || running {
		t.Fatalf("readRunningPID() = (%v, %v), want not running", running, err)
	}
	got, err := os.ReadFile(pidPath)
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	if string(got) != string(content) {
		t.Fatalf("PID content changed: got %q, want %q", got, content)
	}
}

func TestPIDLeaseCloseIsIdempotent(t *testing.T) {
	t.Parallel()

	pidDir := t.TempDir()
	lease, err := acquirePIDLease(pidDir, "idempotent")
	if err != nil {
		t.Fatalf("acquirePIDLease() error = %v", err)
	}
	if err := lease.close(); err != nil {
		t.Fatalf("first close() error = %v", err)
	}
	if err := lease.close(); err != nil {
		t.Fatalf("second close() error = %v", err)
	}

	// 关闭后应能重新取得同一路径的运行权。
	second, err := acquirePIDLease(pidDir, "idempotent")
	if err != nil {
		t.Fatalf("second acquirePIDLease() error = %v", err)
	}
	if err := second.close(); err != nil {
		t.Fatalf("second lease close() error = %v", err)
	}
}
