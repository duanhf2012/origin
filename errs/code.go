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

	// CodeServiceRetired 表示 Service 处于退休状态并拒绝新的 RPC 请求。
	CodeServiceRetired Code = 1001
	// CodeServiceStopping 表示 Service 已经开始停止并拒绝新的工作。
	CodeServiceStopping Code = 1002
	// CodeServiceStopped 表示 Service 已经完全停止或启动失败。
	CodeServiceStopped Code = 1003
	// CodeServiceQueueFull 表示 Service 已接受任务或 Await 任务达到有界上限。
	CodeServiceQueueFull Code = 1004
	// CodeGracefulShutdownTimeout 表示优雅停止没有在调用方给定的期限内完成。
	CodeGracefulShutdownTimeout Code = 1005
	// CodeServiceNotReady 表示 Service 尚未完成启动或调度器尚未开放。
	CodeServiceNotReady Code = 1006

	// CodeTransportUnavailable 表示 Dial、Accept 或底层 I/O 导致传输不可用。
	CodeTransportUnavailable Code = 3001
	// CodeTransportClosed 表示本地 Transport 或连接已经关闭。
	CodeTransportClosed Code = 3002
	// CodeTransportOverloaded 表示连接数或发送队列达到有界上限。
	CodeTransportOverloaded Code = 3003
	// CodeTransportProtocol 表示远端数据违反 Transport 帧协议。
	CodeTransportProtocol Code = 3004
	// CodeTransportMessageTooLarge 表示发送或接收的消息超过配置上限。
	CodeTransportMessageTooLarge Code = 3005

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
	case CodeServiceRetired:
		return "service retired"
	case CodeServiceStopping:
		return "service stopping"
	case CodeServiceStopped:
		return "service stopped"
	case CodeServiceQueueFull:
		return "service queue full"
	case CodeGracefulShutdownTimeout:
		return "graceful shutdown timeout"
	case CodeServiceNotReady:
		return "service not ready"
	case CodeTransportUnavailable:
		return "transport unavailable"
	case CodeTransportClosed:
		return "transport closed"
	case CodeTransportOverloaded:
		return "transport overloaded"
	case CodeTransportProtocol:
		return "transport protocol error"
	case CodeTransportMessageTooLarge:
		return "transport message too large"
	case CodeLogClosed:
		return "log runtime closed"
	case CodeLogOutputFailed:
		return "log output failed"
	default:
		// 空字符串是“未登记”的内部标记，不作为最终错误文本对外返回。
		return ""
	}
}
