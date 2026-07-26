package command

import (
	"fmt"
	"runtime/debug"

	"github.com/duanhf2012/origin/v3/errs"
)

// invalidArgumentf 创建带安全公开说明的参数错误。
func invalidArgumentf(format string, args ...any) error {
	// 参数说明不包含配置内容或凭据，可以直接作为公开消息返回。
	return errs.NewMessage(errs.CodeInvalidArgument, fmt.Sprintf(format, args...))
}

// invalidConfigf 创建只包含已清理路径和文件系统原因的配置目录错误。
func invalidConfigf(format string, args ...any) error {
	return errs.NewMessage(errs.CodeInvalidConfig, fmt.Sprintf(format, args...))
}

// processControlf 创建带本地控制位置说明的进程控制错误。
func processControlf(format string, args ...any) error {
	return errs.NewMessage(errs.CodeProcessControlFailed, fmt.Sprintf(format, args...))
}

// panicError 把可恢复 panic 转换为带堆栈的 CodeInternal 本地错误。
func panicError(scope string, value any) error {
	// 堆栈只通过调用方显式处理的本地 error 返回，不进入跨进程错误结构。
	cause := fmt.Errorf("%s panic: %v\n%s", scope, value, debug.Stack())
	return errs.Wrap(errs.CodeInternal, cause)
}

// callSafely 在用户回调边界恢复 panic，并保留原始回调 error。
func callSafely(scope string, call func() error) (err error) {
	// defer 必须包围整个回调；发生 panic 时，调用栈先执行资源 defer，再转换成普通错误。
	defer func() {
		if value := recover(); value != nil {
			err = panicError(scope, value)
		}
	}()
	return call()
}
