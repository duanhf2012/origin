package kcpnet

import (
	"context"
	"errors"
	"fmt"
	"net"
	"runtime/debug"

	"github.com/duanhf2012/origin/v3/errs"
)

func invalidArgument(message string) error {
	return errs.NewMessage(errs.CodeInvalidArgument, message)
}

func invalidConfig(message string) error {
	return errs.NewMessage(errs.CodeInvalidConfig, message)
}

func transportUnavailable(cause error) error {
	return errs.Wrap(errs.CodeTransportUnavailable, cause)
}

func contextError(err error) error {
	switch {
	case errors.Is(err, context.Canceled):
		return errs.Wrap(errs.CodeCanceled, err)
	case errors.Is(err, context.DeadlineExceeded):
		return errs.Wrap(errs.CodeDeadlineExceeded, err)
	default:
		return errs.Wrap(errs.CodeInternal, err)
	}
}

func normalizeIOError(err error) error {
	if err == nil {
		return nil
	}
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return errs.Wrap(errs.CodeDeadlineExceeded, err)
	}
	// kcp-go/v5 v5.6.18 的 deadline 终态是包内普通 error，经 pkg/errors 包装后仍只暴露该文本。
	// 在依赖升级门禁中必须复核这一映射，不能让第三方错误文本泄漏到公共分类。
	if err.Error() == "timeout" {
		return errs.Wrap(errs.CodeDeadlineExceeded, err)
	}
	return transportUnavailable(err)
}

func normalizeHandlerError(err error) error {
	if err == nil {
		return nil
	}
	var coder errs.Coder
	if errors.As(err, &coder) {
		return err
	}
	return errs.Wrap(errs.CodeInternal, err)
}

func panicError(scope string, value any) error {
	cause := fmt.Errorf("%s panic: %v\n%s", scope, value, debug.Stack())
	return errs.Wrap(errs.CodeInternal, cause)
}

type slowClientError struct{}

func (slowClientError) Error() string { return "kcpnet: 发送队列持续高水位，关闭慢连接" }
func (slowClientError) Code() errs.Code {
	return errs.CodeTransportOverloaded
}
func (slowClientError) Is(target error) bool { return target == errs.ErrTransportOverloaded }
func (slowClientError) SlowClient() bool     { return true }
