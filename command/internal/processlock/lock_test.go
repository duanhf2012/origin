package processlock

import (
	"os"
	"path/filepath"
	"testing"
)

func TestTryLockContentionAndRelease(t *testing.T) {
	// 使用同一路径的两个独立文件句柄验证真实操作系统锁，而不是进程内 mutex。
	path := filepath.Join(t.TempDir(), "process.pid")
	first, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		t.Fatalf("open first file error = %v", err)
	}
	acquired, err := TryLock(first)
	if err != nil || !acquired {
		_ = first.Close()
		t.Fatalf("TryLock(first) = (%v, %v), want acquired", acquired, err)
	}

	second, err := os.OpenFile(path, os.O_RDWR, 0o600)
	if err != nil {
		_ = Release(first)
		t.Fatalf("open second file error = %v", err)
	}
	acquired, err = TryLock(second)
	if err != nil || acquired {
		_ = second.Close()
		_ = Release(first)
		t.Fatalf("TryLock(second) = (%v, %v), want contended", acquired, err)
	}

	// 第一把锁释放后，第二个已经打开的句柄必须可以取得同一锁。
	if err := Release(first); err != nil {
		_ = second.Close()
		t.Fatalf("Release(first) error = %v", err)
	}
	acquired, err = TryLock(second)
	if err != nil || !acquired {
		_ = second.Close()
		t.Fatalf("TryLock(second after release) = (%v, %v), want acquired", acquired, err)
	}
	if err := Release(second); err != nil {
		t.Fatalf("Release(second) error = %v", err)
	}
}
