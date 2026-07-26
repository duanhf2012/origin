//go:build windows

package processlock

import (
	"errors"
	"os"

	"golang.org/x/sys/windows"
)

// windowsLockOffsetHigh 把锁区放到 4 GiB，避开实际 PID JSON 读取区间。
const windowsLockOffsetHigh = 1

// TryLock 尝试立即获得 Windows 文件句柄从 4 GiB 偏移开始的一个字节独占锁。
//
// 锁区必须远离 PID JSON。Windows 会禁止其他句柄读取已锁字节，如果锁住偏移零，
// stop 就无法在目标持锁时读取 PID。Windows 允许锁定 EOF 之外区域，因此文件不会扩容。
func TryLock(file *os.File) (acquired bool, err error) {
	// OffsetHigh=1、Offset=0 表示 4 GiB；长度固定为一个字节且不依赖当前文件偏移。
	overlapped := windows.Overlapped{OffsetHigh: windowsLockOffsetHigh}
	err = windows.LockFileEx(
		windows.Handle(file.Fd()),
		windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY,
		0,
		1,
		0,
		&overlapped,
	)
	if err == nil {
		return true, nil
	}

	// ERROR_LOCK_VIOLATION 只表示另一个进程持有锁，不能包装成系统故障。
	if errors.Is(err, windows.ERROR_LOCK_VIOLATION) {
		return false, nil
	}
	return false, err
}

// Unlock 释放 Windows 文件句柄从 4 GiB 偏移开始的一个字节锁。
func Unlock(file *os.File) error {
	// 解锁必须使用与加锁完全相同的偏移和长度。
	overlapped := windows.Overlapped{OffsetHigh: windowsLockOffsetHigh}
	return windows.UnlockFileEx(
		windows.Handle(file.Fd()),
		0,
		1,
		0,
		&overlapped,
	)
}
