package rpc

import (
	"context"
	"sync"

	"github.com/duanhf2012/origin/v3/errs"
)

const (
	// SystemServiceDiscovery 是框架保留的服务发现控制平面名称。它不属于业务 RPC
	// 路由、Service 目录或生成代码，且不能由项目配置扩展。
	SystemServiceDiscovery = "discovery"

	// systemTCPFramePrefix 与业务 TCP Hello 的版本字节区分。系统帧的实际内容在
	// TCP 复用监听器中剥离此前缀后才会交给 SystemHandler。
	systemTCPFramePrefix byte = 0

	// MaxSystemMessageSize 限制保留控制平面的一条完整消息；当前只承载 Discovery
	// 快照，必须覆盖公开 Provider 的 16 MiB 快照上限和一个 TCP 前缀字节。
	MaxSystemMessageSize = 16 * 1024 * 1024
)

// SystemTarget 是一条框架保留控制连接的静态目标。NodeID 始终必填；TCP 还要求
// Address 为目标 Node 在 nodes[].rpc.tcp.advertise 中声明的可连接地址。
type SystemTarget struct {
	NodeID  string
	Address string
}

// SystemPeer 是系统控制平面的一条已建立双向通道。
//
// Send 在返回 nil 后复制 payload 或接管其等价不可变快照；调用者可立即复用原 Slice。
// 所有回调均串行于同一 Peer，但不同 Peer 可以并发。该接口仅供框架内部包使用，业务
// Service 不会通过普通 RPC Client 获得它。
type SystemPeer interface {
	Send(payload []byte) error
	Close()
}

// SystemHandler 接收一个保留系统服务的连接生命周期与消息。handler 必须快速返回；
// 需要串行状态机时应自行转移到有界 Actor 队列。
type SystemHandler interface {
	OnSystemOpen(peer SystemPeer)
	OnSystemMessage(peer SystemPeer, payload []byte)
	OnSystemClose(peer SystemPeer, cause error)
}

// systemRuntime 保存当前 Node 的保留控制平面。它与业务 RPC 共用 Transport，但从不
// 穿过业务 Route/Session/Service Dispatcher；系统 Handler 只能在 Freeze 前绑定一次。
type systemRuntime struct {
	owner *Runtime

	mu      sync.Mutex
	handler SystemHandler
	closed  bool

	// natsPeer 是当前 Node 主动拨往 Discovery Server 的唯一控制通道。每个 Node 只需要
	// 一个该通道；连接丢失时会通知 Provider 重新建立，而不会缓冲或重放控制消息。
	natsPeer   *natsSystemPeer
	natsDialID uint64
	// natsInbound 根据 NATS reply subject 保存服务端已见客户端。NATS Core 没有连接级
	// Close 事件，TTL/Withdraw 负责成员失效；Runtime Close 时会统一通知所有 Peer。
	natsInbound map[string]*natsSystemPeer
}

func newSystemRuntime(owner *Runtime) *systemRuntime {
	return &systemRuntime{
		owner:       owner,
		natsInbound: make(map[string]*natsSystemPeer),
	}
}

func (runtime *Runtime) BindSystemHandler(handler SystemHandler) error {
	if runtime == nil || handler == nil {
		return errs.ErrInvalidArgument
	}
	runtime.mu.Lock()
	defer runtime.mu.Unlock()
	if runtime.frozen.Load() || runtime.closed.Load() || runtime.system == nil {
		return errs.ErrServiceNotReady
	}
	runtime.system.mu.Lock()
	defer runtime.system.mu.Unlock()
	if runtime.system.handler != nil || runtime.system.closed {
		return errs.ErrServiceNotReady
	}
	runtime.system.handler = handler
	return nil
}

// DialSystem 创建一条到固定目标的控制通道。它只支持 Discovery 保留服务，且必须在
// Runtime 网络已启动后调用；业务 RPC 的发现目录与目标 SessionID 不参与本次引导。
func (runtime *Runtime) DialSystem(
	ctx context.Context,
	target SystemTarget,
	handler SystemHandler,
) (SystemPeer, error) {
	if runtime == nil || ctx == nil || handler == nil || runtime.system == nil ||
		!validWireName(target.NodeID) || runtime.closed.Load() {
		return nil, errs.ErrInvalidArgument
	}
	if runtime.remote != nil {
		if err := validateAdvertiseAddress(target.Address); err != nil {
			return nil, err
		}
		return runtime.system.dialTCP(ctx, target, handler)
	}
	if runtime.nats != nil {
		if target.Address != "" {
			return nil, errs.ErrInvalidArgument
		}
		return runtime.system.dialNATS(target, handler)
	}
	return nil, errs.ErrTransportUnavailable
}

func (system *systemRuntime) inboundHandler() SystemHandler {
	if system == nil {
		return nil
	}
	system.mu.Lock()
	defer system.mu.Unlock()
	if system.closed {
		return nil
	}
	return system.handler
}

func (system *systemRuntime) close(cause error) {
	if system == nil {
		return
	}
	system.mu.Lock()
	if system.closed {
		system.mu.Unlock()
		return
	}
	system.closed = true
	peers := make([]*natsSystemPeer, 0, len(system.natsInbound)+1)
	if system.natsPeer != nil {
		peers = append(peers, system.natsPeer)
		system.natsPeer = nil
	}
	for _, peer := range system.natsInbound {
		peers = append(peers, peer)
	}
	system.natsInbound = make(map[string]*natsSystemPeer)
	system.mu.Unlock()
	for _, peer := range peers {
		peer.closeWith(cause)
	}
}
