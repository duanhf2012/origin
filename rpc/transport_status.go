package rpc

import "github.com/duanhf2012/origin/v3/errs"

// TransportKind 标识当前 Runtime 使用的远程 RPC 传输。
//
// 该类型只用于 Node 冷路径状态通知，不参与逐次 RPC 路由分派。
type TransportKind uint8

const (
	// TransportKindNone 表示当前 Node 只支持本地 RPC。
	TransportKindNone TransportKind = iota
	// TransportKindTCP 表示当前 Node 使用 Origin TCP RPC。
	TransportKindTCP
	// TransportKindNATS 表示当前 Node 使用 NATS RPC。
	TransportKindNATS
)

// TransportState 表示当前 Node 整体入站 RPC 能力的内部状态。
type TransportState uint8

const (
	// TransportStateDisabled 表示没有配置远程 Transport。
	TransportStateDisabled TransportState = iota
	// TransportStateStarting 表示 Transport 正在完成首次启动。
	TransportStateStarting
	// TransportStateReady 表示入站 Transport 可以接收新 RPC。
	TransportStateReady
	// TransportStateRecovering 表示整体入站能力暂时不可用且正在恢复。
	TransportStateRecovering
	// TransportStateFailed 表示内部状态无法安全恢复；普通网络错误不得进入该状态。
	TransportStateFailed
	// TransportStateStopping 表示正式 Stop 已关闭新入站准入。
	TransportStateStopping
	// TransportStateStopped 表示 Transport 的全部资源已经回收。
	TransportStateStopped
)

// TransportEvent 是 RPC Runtime 交给所属 Node 的常数大小状态快照。
//
// Cause 只供本进程日志和诊断使用，不能写入服务发现或线协议。Ready 后 Cause 必须为 nil。
type TransportEvent struct {
	Kind                TransportKind
	State               TransportState
	Reconnects          uint64
	ConsecutiveFailures uint64
	ErrorCode           errs.Code
	Cause               error
}
