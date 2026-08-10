//go:build windows

package command

import (
	"errors"

	"golang.org/x/sys/windows"
)

// isTransientControlResponseReadError 只识别 Windows 文件发布交接期间可能出现的共享冲突。
//
// 响应文件已经通过原子 Rename 发布，但杀毒软件、索引器或目标进程的短暂句柄仍可能禁止
// 共享读取。现有控制 Deadline 和 25ms 轮询为这两个系统错误提供有界重试；访问拒绝、路径
// 类型、损坏文件和其他 I/O 错误继续立即返回，不能被当作暂态掩盖。
func isTransientControlResponseReadError(err error) bool {
	return errors.Is(err, windows.ERROR_SHARING_VIOLATION) ||
		errors.Is(err, windows.ERROR_LOCK_VIOLATION)
}
