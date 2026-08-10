package tcpnet

import (
	"context"
	"errors"
	"fmt"
	"net"
	"runtime/debug"

	"github.com/duanhf2012/origin/v3/errs"
)

// invalidArgument 创建 TCP 公共调用参数错误。
func invalidArgument(message string) error {
	// 参数错误属于调用方立即可修复的问题，保留简短动态说明。
	return errs.NewMessage(errs.CodeInvalidArgument, message)
}

// invalidConfig 创建 TCP 启动配置错误。
func invalidConfig(message string) error {
	// Options 在创建网络资源前统一映射为稳定配置错误码。
	return errs.NewMessage(errs.CodeInvalidConfig, message)
}

// transportUnavailable 使用稳定 Transport 码保留底层网络原因。
func transportUnavailable(cause error) error {
	// 没有 cause 时复用公共哨兵，正常错误路径不制造无意义包装。
	return errs.Wrap(errs.CodeTransportUnavailable, cause)
}

// deadlineError 把网络超时映射为统一 Deadline 错误，同时保留 net.Error。
func deadlineError(cause error) error {
	// errs 的 CodeDeadlineExceeded 与 context.DeadlineExceeded 保持 errors.Is 兼容。
	return errs.Wrap(errs.CodeDeadlineExceeded, cause)
}

// contextError 把 Context 结束原因转换为 Origin 稳定错误。
func contextError(err error) error {
	// 只接受 Context 的两个终态；其他错误按内部错误保留，避免错误分类丢失。
	switch {
	case errors.Is(err, context.Canceled):
		return errs.Wrap(errs.CodeCanceled, err)
	case errors.Is(err, context.DeadlineExceeded):
		return errs.Wrap(errs.CodeDeadlineExceeded, err)
	default:
		return errs.Wrap(errs.CodeInternal, err)
	}
}

// normalizeIOError 把底层读写错误转换为 Transport 或 Deadline 语义。
func normalizeIOError(err error) error {
	// Deadline 必须优先于普通 I/O 分类，使上层能够区分失活和其他断线。
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return deadlineError(err)
	}
	// 其他 EOF、reset、broken pipe 和关闭竞态都表示当前物理传输不可用。
	return transportUnavailable(err)
}

// normalizeHandlerError 保留 Handler 已经给出的 Origin 码，否则归类为内部错误。
func normalizeHandlerError(err error) error {
	// nil 表示 Handler 正常完成，不创建错误对象。
	if err == nil {
		return nil
	}
	// Adapter 可以显式返回协议或 Transport 错误；已有稳定码不再被二次覆盖。
	var coder errs.Coder
	if errors.As(err, &coder) {
		return err
	}
	// 普通错误来自框架内部 Handler，实现问题统一映射为 CodeInternal。
	return errs.Wrap(errs.CodeInternal, err)
}

// panicError 把连接 goroutine 或 Handler panic 转换为带原始堆栈的内部错误。
func panicError(scope string, value any) error {
	// debug.Stack 必须在 recover 所在 defer 中调用，才能保留真正 panic 现场。
	cause := fmt.Errorf("%s panic: %v\n%s", scope, value, debug.Stack())
	return errs.Wrap(errs.CodeInternal, cause)
}
