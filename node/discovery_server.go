package node

import "context"

// discoveryServer 是保留 DiscoveryService 向 Node 基础设施阶段提供的最小内部能力。
//
// OnInit/OnStart/OnStop 仍按普通 Service 顺序执行；Listener 必须更早 Prepare，并在当前
// Node Provider 已关闭后由 Node 显式回收。
type discoveryServer interface {
	PrepareDiscovery(context.Context) error
	CloseDiscovery(context.Context) error
}
