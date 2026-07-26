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
	// CodeProcessAlreadyRunning 表示同名 Application 进程已经持有运行权。
	CodeProcessAlreadyRunning
	// CodeProcessControlFailed 表示本地进程锁、控制文件或停止通知操作失败。
	CodeProcessControlFailed

	// CodeLogClosed 表示日志运行时已经关闭。
	CodeLogClosed Code = 7001
	// CodeLogOutputFailed 表示日志输出创建、刷新或关闭失败。
	CodeLogOutputFailed Code = 7002
)

// codeText 返回已经登记的稳定英文错误文本。
//
// 未知错误码返回空字符串，由上层统一生成包含数值的兜底文本。
func codeText(code Code) string {
	// 使用显式 switch 固定错误码与文本的对应关系，避免可变全局 Map。
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
	case CodeProcessAlreadyRunning:
		return "process already running"
	case CodeProcessControlFailed:
		return "process control failed"
	case CodeLogClosed:
		return "log runtime closed"
	case CodeLogOutputFailed:
		return "log output failed"
	default:
		// 空字符串是“未登记”的内部标记，不作为最终错误文本对外返回。
		return ""
	}
}
