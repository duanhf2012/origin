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
	// ErrProcessAlreadyRunning 表示同名 Application 进程已经运行。
	ErrProcessAlreadyRunning error = newCodeError(CodeProcessAlreadyRunning)
	// ErrProcessControlFailed 表示本地进程控制操作失败。
	ErrProcessControlFailed error = newCodeError(CodeProcessControlFailed)
	// ErrLogClosed 表示日志运行时已经关闭。
	ErrLogClosed error = newCodeError(CodeLogClosed)
	// ErrLogOutputFailed 表示日志输出创建、刷新或关闭失败。
	ErrLogOutputFailed error = newCodeError(CodeLogOutputFailed)
)

// codeError 是只有稳定错误码、没有动态消息和底层原因的轻量错误。
type codeError struct {
	code Code
}

// Error 返回错误码对应的稳定文本。
func (e *codeError) Error() string {
	return errorText(e.code)
}

// Code 返回错误携带的 Origin 错误码。
func (e *codeError) Code() Code {
	return e.code
}

// Is 允许 errors.Is 按稳定错误码匹配哨兵错误。
func (e *codeError) Is(target error) bool {
	return isCodeTarget(e.code, target)
}

// messageError 为稳定错误码附加可安全公开的动态说明。
type messageError struct {
	code    Code
	message string
}

// Error 返回调用方提供的公开说明。
func (e *messageError) Error() string {
	return e.message
}

// Code 返回错误携带的 Origin 错误码。
func (e *messageError) Code() Code {
	return e.code
}

// Is 允许动态消息错误按错误码匹配公共哨兵。
func (e *messageError) Is(target error) bool {
	return isCodeTarget(e.code, target)
}

// wrappedError 在稳定错误码之外保留本地错误链。
type wrappedError struct {
	code  Code
	cause error
}

// Error 组合稳定错误文本与底层原因，便于本地排障。
func (e *wrappedError) Error() string {
	return errorText(e.code) + ": " + e.cause.Error()
}

// Code 返回包装层声明的 Origin 错误码。
func (e *wrappedError) Code() Code {
	return e.code
}

// Is 优先按包装层错误码匹配公共哨兵。
func (e *wrappedError) Is(target error) bool {
	return isCodeTarget(e.code, target)
}

// Unwrap 把底层原因交还给标准库错误链遍历。
func (e *wrappedError) Unwrap() error {
	return e.cause
}

// New 返回指定错误码对应的错误。
//
// 已登记的通用错误会复用只读哨兵；CodeOK 返回 nil。
func New(code Code) error {
	// 成功在 Go API 中始终表示为 nil，避免产生无意义错误对象。
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
	case CodeProcessAlreadyRunning:
		return ErrProcessAlreadyRunning
	case CodeProcessControlFailed:
		return ErrProcessControlFailed
	case CodeLogClosed:
		return ErrLogClosed
	case CodeLogOutputFailed:
		return ErrLogOutputFailed
	default:
		// 未登记错误码仍然需要保留原始数值，因此按需创建轻量对象。
		return newCodeError(code)
	}
}

// NewMessage 创建带有公开动态消息的错误。
//
// message 为空时等同于 New；CodeOK 始终返回 nil。
func NewMessage(code Code, message string) error {
	// CodeOK 的语义优先于消息内容，调用方不能制造“成功错误”。
	if code == CodeOK {
		return nil
	}
	// 空消息复用公共错误，减少对象分配并保持哨兵一致性。
	if message == "" {
		return New(code)
	}
	// 非空消息单独保存；调用方负责保证其中没有敏感信息。
	return &messageError{
		code:    code,
		message: message,
	}
}

// Wrap 使用稳定错误码包装本地 cause。
//
// CodeOK 不改变 cause；cause 为 nil 时等同于 New。
func Wrap(code Code, cause error) error {
	// CodeOK 表示不增加 Origin 语义，只透传原始错误。
	if code == CodeOK {
		return cause
	}
	// 没有底层原因时退化为普通稳定错误，避免保存 nil cause。
	if cause == nil {
		return New(code)
	}
	// 同时保存稳定码和 cause，使 errors.Is/As 可以继续遍历错误链。
	return &wrappedError{
		code:  code,
		cause: cause,
	}
}

// CodeOf 返回错误链最外层的 Origin 错误码。
//
// nil 返回 CodeOK；没有 Origin 错误码的普通错误按照 CodeInternal 处理。
func CodeOf(err error) Code {
	// nil 是唯一的成功结果。
	if err == nil {
		return CodeOK
	}

	// 优先读取最外层 Origin 错误，确保包装层可以重分类底层错误。
	if coder, ok := err.(Coder); ok {
		return coder.Code()
	}
	// Context 的两个标准错误使用直接比较快路径，避免不必要的链遍历。
	if err == context.Canceled {
		return CodeCanceled
	}
	if err == context.DeadlineExceeded {
		return CodeDeadlineExceeded
	}

	// 普通包装错误可能把 Coder 或 Context 错误放在内部，继续遍历错误链。
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
	// 没有已知语义的本地错误统一归类为内部错误。
	return CodeInternal
}

// IsCode 报告 err 是否具有指定 Origin 错误码。
func IsCode(err error, code Code) bool {
	// 复用 CodeOf 的完整错误链和 Context 兼容规则。
	return CodeOf(err) == code
}

// newCodeError 创建不带动态信息的错误对象。
func newCodeError(code Code) *codeError {
	return &codeError{code: code}
}

// errorText 保证任意错误码都能得到非空、稳定的文本。
func errorText(code Code) string {
	// 已登记错误码直接使用固定文本。
	if text := codeText(code); text != "" {
		return text
	}
	// 未登记错误码必须包含数值，便于跨版本和跨语言排查。
	return "error code " + strconv.FormatUint(uint64(code), 10)
}

// isCodeTarget 实现按稳定错误码以及标准 Context 哨兵的匹配。
func isCodeTarget(code Code, target error) bool {
	// errors.Is 约定 nil 目标永远不匹配非 nil 错误。
	if target == nil {
		return false
	}
	// Origin 的取消和超时错误与标准库哨兵保持双向兼容。
	if code == CodeCanceled && target == context.Canceled {
		return true
	}
	if code == CodeDeadlineExceeded && target == context.DeadlineExceeded {
		return true
	}
	// 其他情况只比较 Coder 暴露的稳定数值，不比较动态消息。
	coder, ok := target.(Coder)
	return ok && coder.Code() == code
}
