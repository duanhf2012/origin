// Package rpc 提供 Origin Native RPC 的稳定契约类型、生成代码运行底座和本地 Runtime。
package rpc

import (
	"context"

	"github.com/duanhf2012/origin/v3/internal/bufferpool"
)

const (
	// GeneratedABIVersion 由生成文件在编译期校验，防止旧生成代码静默连接新 Runtime。
	GeneratedABIVersion = 3
	// DefaultMaxPayloadSize 是 Origin RPC 固定的默认业务载荷上限。
	DefaultMaxPayloadSize = 4 * 1024 * 1024
	// MaxContainerElements 限制单个 Slice 或 Map 声明的元素数量。
	MaxContainerElements = 1 << 20
)

// ContractID 是一个 RPC 接口在 Origin 进程和线协议中的稳定标识。
type ContractID uint64

// MethodID 是一个 RPC 方法在 Origin 进程和线协议中的全局稳定标识。
type MethodID uint64

// ContractFingerprint 精确描述完整接口签名、Schema、Codec 和格式版本。
type ContractFingerprint [32]byte

// CallKind 区分需要响应的调用和主动放弃响应的通知。
type CallKind uint8

const (
	// CallRequest 表示 Await 或 Async 请求，Dispatcher 必须编码成功响应。
	CallRequest CallKind = iota + 1
	// CallNotify 表示 Notify 或 Broadcast，Dispatcher 不得申请和编码响应。
	CallNotify
)

// Buffer 是 RPC Runtime 唯一持有和转移的内部可复用字节缓冲区。
//
// 生成代码只在一次调用的同步编码、解码边界使用它；业务方法不会收到 Buffer，也不负责
// 归还。该别名使生成代码不需要越过 Go internal 包边界。
type Buffer = bufferpool.Buffer

// targetMode 表示 M11 当前支持的本地 Service 选择方式。
type targetMode uint8

const (
	targetInvalid targetMode = iota
	targetService
	targetServiceOnNode
)

// Target 是生成 RPC 客户端保存的不可变逻辑目标。
//
// Target 只保存 NodeID 和 ServiceName，不持有 Service 指针、连接、路由快照、Future 或
// Buffer。零值可安全构造客户端，但真正调用时返回 CodeInvalidArgument。
type Target struct {
	mode        targetMode
	nodeID      string
	serviceName string
}

// ToService 选择调用方所属 Node 内指定实际名称的 Service。
func ToService(serviceName string) Target {
	return Target{
		mode:        targetService,
		serviceName: serviceName,
	}
}

// ToServiceOnNode 选择指定 Node 中指定实际名称的 Service。
//
// M11 尚未接入远端 Transport，因此只有 nodeID 等于调用方所属 Node 时能够成功。
func ToServiceOnNode(nodeID, serviceName string) Target {
	return Target{
		mode:        targetServiceOnNode,
		nodeID:      nodeID,
		serviceName: serviceName,
	}
}

// valid 报告 Target 是否具有完整且无歧义的本地路由数据。
func (target Target) valid() bool {
	switch target.mode {
	case targetService:
		return target.nodeID == "" && target.serviceName != ""
	case targetServiceOnNode:
		return target.nodeID != "" && target.serviceName != ""
	default:
		return false
	}
}

// Dispatcher 是 origingen 为一个 RPC 契约生成的静态服务端分派器。
//
// request 只在本次调用期间有效。CallRequest 必须使用 response 写入完整响应；
// CallNotify 收到零值 response，不能编码响应。ResponseWriter 使用值传入和值返回，避免
// 接口调用迫使栈上写入器逃逸；实现不得在返回后保存任何参数引用。
type Dispatcher interface {
	ContractID() ContractID
	Fingerprint() ContractFingerprint
	Dispatch(
		ctx context.Context,
		methodID MethodID,
		kind CallKind,
		request []byte,
		response ResponseWriter,
	) (ResponseWriter, error)
}
