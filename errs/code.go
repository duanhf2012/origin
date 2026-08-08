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
	// CodeConfigNotFound 表示显式请求的配置路径不存在。
	CodeConfigNotFound

	// CodeServiceRetired 表示业务主动拒绝退休期间不允许执行的操作。
	//
	// 框架不会仅因 Service 处于 Retired 自动返回该错误。
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
	// CodeServiceFailed 表示 Service 因框架内部状态无法证明安全而被运行期隔离。
	CodeServiceFailed Code = 1007

	// CodeRPCNoRoute 表示当前 RPC Target 没有可用的 Service。
	CodeRPCNoRoute Code = 2001
	// CodeRPCInvalidRouteKey 表示 RPC 路由键类型无效。
	CodeRPCInvalidRouteKey Code = 2002
	// CodeRPCRouteSelectorFailed 表示自定义 RPC 路由选择失败。
	CodeRPCRouteSelectorFailed Code = 2003
	// CodeRPCContractMismatch 表示调用方和目标 Service 的完整 RPC 契约不一致。
	CodeRPCContractMismatch Code = 2004
	// CodeRPCMethodNotFound 表示目标契约不包含给定 MethodID。
	CodeRPCMethodNotFound Code = 2005
	// CodeRPCEncodeFailed 表示 RPC 请求或响应静态编码失败。
	CodeRPCEncodeFailed Code = 2006
	// CodeRPCRequestDecodeFailed 表示目标端无法按契约解码请求。
	CodeRPCRequestDecodeFailed Code = 2007
	// CodeRPCResponseDecodeFailed 表示调用端无法按契约解码响应。
	CodeRPCResponseDecodeFailed Code = 2008
	// CodeRPCExecutionPanic 表示目标 RPC 业务方法发生 panic。
	CodeRPCExecutionPanic Code = 2009
	// CodeRPCBroadcastPartialFailed 表示多目标广播只有部分目标完成本地提交。
	CodeRPCBroadcastPartialFailed Code = 2010
	// CodeRPCBroadcastFailed 表示多目标广播没有任何目标完成本地提交。
	CodeRPCBroadcastFailed Code = 2011

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

	// CodeDiscoveryUnavailable 表示必需服务发现当前不可用或尚未完成重新发布。
	CodeDiscoveryUnavailable Code = 5001
	// CodeDiscoveryDuplicateNode 表示活跃 NodeID 已由其他 Session 占用。
	CodeDiscoveryDuplicateNode Code = 5002
	// CodeDiscoveryCapacity 表示服务发现记录、快照、连接或队列达到固定上限。
	CodeDiscoveryCapacity Code = 5003
	// CodeDiscoverySnapshotInvalid 表示 Provider 提交的完整权威事实不合法。
	CodeDiscoverySnapshotInvalid Code = 5004

	// CodeLogClosed 表示日志运行时已经关闭。
	CodeLogClosed Code = 7001
	// CodeLogOutputFailed 表示日志输出创建、刷新或关闭失败。
	CodeLogOutputFailed Code = 7002
	// CodeLogControlUnsupported 表示自定义日志 Handler 没有实现可选运行时控制接口。
	CodeLogControlUnsupported Code = 7003
	// CodeLogOutputUnavailable 表示启动配置没有为目标日志输出端建立资源。
	CodeLogOutputUnavailable Code = 7004

	// CodeDiagnosticsUnavailable 表示诊断 Listener、HTTP Serve 或受控关闭无法完成。
	CodeDiagnosticsUnavailable Code = 8001
	// CodeDiagnosticsStateConflict 表示当前 Application 或 Listener 状态不允许该诊断操作。
	CodeDiagnosticsStateConflict Code = 8002
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
	case CodeConfigNotFound:
		return "config path not found"
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
	case CodeServiceFailed:
		return "service failed"
	case CodeRPCNoRoute:
		return "rpc route not found"
	case CodeRPCInvalidRouteKey:
		return "invalid rpc route key"
	case CodeRPCRouteSelectorFailed:
		return "rpc route selector failed"
	case CodeRPCContractMismatch:
		return "rpc contract mismatch"
	case CodeRPCMethodNotFound:
		return "rpc method not found"
	case CodeRPCEncodeFailed:
		return "rpc encode failed"
	case CodeRPCRequestDecodeFailed:
		return "rpc request decode failed"
	case CodeRPCResponseDecodeFailed:
		return "rpc response decode failed"
	case CodeRPCExecutionPanic:
		return "rpc execution panic"
	case CodeRPCBroadcastPartialFailed:
		return "rpc broadcast partially failed"
	case CodeRPCBroadcastFailed:
		return "rpc broadcast failed"
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
	case CodeDiscoveryUnavailable:
		return "discovery unavailable"
	case CodeDiscoveryDuplicateNode:
		return "discovery duplicate node"
	case CodeDiscoveryCapacity:
		return "discovery capacity exceeded"
	case CodeDiscoverySnapshotInvalid:
		return "discovery snapshot invalid"
	case CodeLogClosed:
		return "log runtime closed"
	case CodeLogOutputFailed:
		return "log output failed"
	case CodeLogControlUnsupported:
		return "log runtime control unsupported"
	case CodeLogOutputUnavailable:
		return "log output unavailable"
	case CodeDiagnosticsUnavailable:
		return "diagnostics unavailable"
	case CodeDiagnosticsStateConflict:
		return "diagnostics state conflict"
	default:
		// 空字符串是“未登记”的内部标记，不作为最终错误文本对外返回。
		return ""
	}
}
