package discovery

import "context"

// ListenerID 是所属 Service 内一次监听注册的稳定取消标识。
//
// 零值无效；成功移除后 Service.RemoveDiscoveryListener 会把调用方持有的 ID 置零。
type ListenerID uint64

// IListener 接收所属 Service 可见目录的状态同步事件。
//
// 回调只在注册方 Service 的唯一 FIFO Runner 中执行，可以使用传入的 Origin Task Context
// 调用 Service.Await。事件是状态同步而不是审计日志，回调跨 Await 后应重新查询当前状态。
type IListener interface {
	OnDiscovered(ctx context.Context, event Event)
	OnStateChanged(ctx context.Context, event Event)
	OnLost(ctx context.Context, event Event)
}
