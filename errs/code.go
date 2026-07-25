// Package errs 提供 Origin 各组件共享的稳定错误码。
package errs

// Code 是跨模块、跨进程和跨语言使用的稳定错误码。
type Code uint32

const (
	// CodeOK 表示操作成功。Go API 使用 nil 表示成功。
	CodeOK Code = iota
	// CodeCanceled 表示操作已取消。
	CodeCanceled
	// CodeDeadlineExceeded 表示操作超过截止时间。
	CodeDeadlineExceeded
	// CodeInternal 表示未归类的内部错误。
	CodeInternal
	// CodeInvalidArgument 表示调用参数无效。
	CodeInvalidArgument
	// CodeInvalidConfig 表示配置无效。
	CodeInvalidConfig

	// CodeLogClosed 表示日志运行时已经关闭。
	CodeLogClosed Code = 7001
	// CodeLogOutputFailed 表示日志输出创建、刷新或关闭失败。
	CodeLogOutputFailed Code = 7002
)

func codeText(code Code) string {
	switch code {
	case CodeOK:
		return "ok"
	case CodeCanceled:
		return "operation canceled"
	case CodeDeadlineExceeded:
		return "deadline exceeded"
	case CodeInternal:
		return "internal error"
	case CodeInvalidArgument:
		return "invalid argument"
	case CodeInvalidConfig:
		return "invalid config"
	case CodeLogClosed:
		return "log runtime closed"
	case CodeLogOutputFailed:
		return "log output failed"
	default:
		return ""
	}
}
