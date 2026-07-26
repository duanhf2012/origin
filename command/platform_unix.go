//go:build linux || darwin

package command

import (
	"context"
	"errors"
	"os"
	"os/signal"
	"syscall"

	"golang.org/x/sys/unix"
)

// startPlatformControl 把父 Context、SIGINT 和 SIGTERM 合并为同一个运行期 Context。
func startPlatformControl(
	parent context.Context,
	_ string,
) (context.Context, func() error, error) {
	// signal.NotifyContext 负责注册和注销信号；stop 必须在 Handler 返回后调用。
	runCtx, stop := signal.NotifyContext(parent, os.Interrupt, syscall.SIGTERM)
	closeControl := func() error {
		stop()
		return nil
	}
	return runCtx, closeControl, nil
}

// requestPlatformStop 向当前持锁 PID 发送标准 SIGTERM。
func requestPlatformStop(pid int, _ string) error {
	// PID 已由严格记录校验保证为正数，unix.Kill 不使用 shell 或进程名匹配。
	return unix.Kill(pid, unix.SIGTERM)
}

// platformProcessGone 报告发送信号失败是否仅因为目标进程已经退出。
func platformProcessGone(err error) bool {
	return errors.Is(err, unix.ESRCH)
}
