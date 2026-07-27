package timerwheel

import (
	"errors"

	"github.com/duanhf2012/origin/v3/errs"
)

var (
	// ErrEngineClosed 表示 Engine 已经进入不可逆的关闭状态。
	ErrEngineClosed = errors.New("timerwheel: Engine 已关闭")
	// ErrDeadlineQueueClosed 表示 DeadlineQueue 已关闭并已经清理全部条目。
	ErrDeadlineQueueClosed = errors.New("timerwheel: DeadlineQueue 已关闭")
)

// invalidArgument 创建带 Origin 稳定错误码的参数或状态错误。
func invalidArgument(message string) error {
	return errs.NewMessage(errs.CodeInvalidArgument, message)
}

// internalError 创建表示内部容量或不变量失败的稳定错误。
func internalError(message string) error {
	return errs.NewMessage(errs.CodeInternal, message)
}
