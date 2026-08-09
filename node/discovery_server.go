package node

import (
	"context"

	"github.com/duanhf2012/origin/v3/rpc"
)

// discoveryServer 是保留 DiscoveryService 向 Node 基础设施阶段提供的最小内部能力。
//
// OnInit/OnStart/OnStop 仍按普通 Service 顺序执行；Listener 必须更早 Prepare，并在当前
// Node Provider 已关闭后由 Node 显式回收。
type discoveryServer interface {
	PrepareDiscovery(context.Context) error
	CloseDiscovery(context.Context) error
}

// discoverySystemBinder 让保留 DiscoveryService 在 RPC Freeze 前登记系统控制处理器。
// 该接口不会暴露给业务 Service，也不属于公开服务发现 Provider SPI。
type discoverySystemBinder interface {
	BindSystemRPC(*rpc.Runtime) error
}
