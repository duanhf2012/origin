package errs

import (
	"context"
	"errors"
	"strconv"
)

// Coder 是可以提供稳定 Origin 错误码的错误。
type Coder interface {
	Code() Code
}

var (
	// ErrCanceled 表示操作已取消。
	ErrCanceled error = newCodeError(CodeCanceled)
	// ErrDeadlineExceeded 表示操作超过截止时间。
	ErrDeadlineExceeded error = newCodeError(CodeDeadlineExceeded)
	// ErrInternal 表示未归类的内部错误。
	ErrInternal error = newCodeError(CodeInternal)
	// ErrInvalidArgument 表示调用参数无效。
	ErrInvalidArgument error = newCodeError(CodeInvalidArgument)
	// ErrInvalidConfig 表示配置无效。
	ErrInvalidConfig error = newCodeError(CodeInvalidConfig)
	// ErrLogClosed 表示日志运行时已经关闭。
	ErrLogClosed error = newCodeError(CodeLogClosed)
	// ErrLogOutputFailed 表示日志输出创建、刷新或关闭失败。
	ErrLogOutputFailed error = newCodeError(CodeLogOutputFailed)
)

type codeError struct {
	code Code
}

func (e *codeError) Error() string {
	return errorText(e.code)
}

func (e *codeError) Code() Code {
	return e.code
}

func (e *codeError) Is(target error) bool {
	return isCodeTarget(e.code, target)
}

type messageError struct {
	code    Code
	message string
}

func (e *messageError) Error() string {
	return e.message
}

func (e *messageError) Code() Code {
	return e.code
}

func (e *messageError) Is(target error) bool {
	return isCodeTarget(e.code, target)
}

type wrappedError struct {
	code  Code
	cause error
}

func (e *wrappedError) Error() string {
	return errorText(e.code) + ": " + e.cause.Error()
}

func (e *wrappedError) Code() Code {
	return e.code
}

func (e *wrappedError) Is(target error) bool {
	return isCodeTarget(e.code, target)
}

func (e *wrappedError) Unwrap() error {
	return e.cause
}

// New 返回指定错误码对应的错误。
//
// 已登记的通用错误会复用只读哨兵；CodeOK 返回 nil。
func New(code Code) error {
	switch code {
	case CodeOK:
		return nil
	case CodeCanceled:
		return ErrCanceled
	case CodeDeadlineExceeded:
		return ErrDeadlineExceeded
	case CodeInternal:
		return ErrInternal
	case CodeInvalidArgument:
		return ErrInvalidArgument
	case CodeInvalidConfig:
		return ErrInvalidConfig
	case CodeLogClosed:
		return ErrLogClosed
	case CodeLogOutputFailed:
		return ErrLogOutputFailed
	default:
		return newCodeError(code)
	}
}

// NewMessage 创建带有公开动态消息的错误。
//
// message 为空时等同于 New；CodeOK 始终返回 nil。
func NewMessage(code Code, message string) error {
	if code == CodeOK {
		return nil
	}
	if message == "" {
		return New(code)
	}
	return &messageError{
		code:    code,
		message: message,
	}
}

// Wrap 使用稳定错误码包装本地 cause。
//
// CodeOK 不改变 cause；cause 为 nil 时等同于 New。
func Wrap(code Code, cause error) error {
	if code == CodeOK {
		return cause
	}
	if cause == nil {
		return New(code)
	}
	return &wrappedError{
		code:  code,
		cause: cause,
	}
}

// CodeOf 返回错误链最外层的 Origin 错误码。
//
// nil 返回 CodeOK；没有 Origin 错误码的普通错误按照 CodeInternal 处理。
func CodeOf(err error) Code {
	if err == nil {
		return CodeOK
	}

	if coder, ok := err.(Coder); ok {
		return coder.Code()
	}
	if err == context.Canceled {
		return CodeCanceled
	}
	if err == context.DeadlineExceeded {
		return CodeDeadlineExceeded
	}

	var coder Coder
	if errors.As(err, &coder) {
		return coder.Code()
	}
	if errors.Is(err, context.Canceled) {
		return CodeCanceled
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return CodeDeadlineExceeded
	}
	return CodeInternal
}

// IsCode 报告 err 是否具有指定 Origin 错误码。
func IsCode(err error, code Code) bool {
	return CodeOf(err) == code
}

func newCodeError(code Code) *codeError {
	return &codeError{code: code}
}

func errorText(code Code) string {
	if text := codeText(code); text != "" {
		return text
	}
	return "error code " + strconv.FormatUint(uint64(code), 10)
}

func isCodeTarget(code Code, target error) bool {
	if target == nil {
		return false
	}
	if code == CodeCanceled && target == context.Canceled {
		return true
	}
	if code == CodeDeadlineExceeded && target == context.DeadlineExceeded {
		return true
	}
	coder, ok := target.(Coder)
	return ok && coder.Code() == code
}
