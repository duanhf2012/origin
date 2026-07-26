//go:build linux || darwin

package processlock

import (
	"errors"
	"os"

	"golang.org/x/sys/unix"
)

// TryLock 尝试立即获得 Unix 文件描述符对应文件的整文件独占锁。
//
// acquired 为 false 且 err 为 nil 表示锁正由其他进程持有；该状态不是系统调用失败。
func TryLock(file *os.File) (acquired bool, err error) {
	// LOCK_NB 保证竞争时立即返回，命令协程不会在内核中无限等待。
	err = unix.Flock(int(file.Fd()), unix.LOCK_EX|unix.LOCK_NB)
	if err == nil {
		return true, nil
	}

	// Linux 与 macOS 可能分别返回 EWOULDBLOCK 或 EAGAIN，两者都统一为普通竞争结果。
	if errors.Is(err, unix.EWOULDBLOCK) || errors.Is(err, unix.EAGAIN) {
		return false, nil
	}
	return false, err
}

// Unlock 释放 Unix 文件描述符持有的整文件锁。
func Unlock(file *os.File) error {
	// 文件关闭由上层 Release 负责，这里只执行与 TryLock 对称的系统调用。
	return unix.Flock(int(file.Fd()), unix.LOCK_UN)
}
