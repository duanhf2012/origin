// Package provider 定义可由项目替换的最小服务发现 Provider 契约。
//
// Provider 只负责后端连接、完整权威快照和本 Node 发布。目录筛选、业务事件、旧快照过期
// 和 Readiness 统一由框架拥有，第三方实现不需要依赖 Application、Node 或 RPC 内部类型。
package provider

import (
	"context"

	originlog "github.com/duanhf2012/origin/v3/log"
)

// Factory 使用框架提供的受限 Context 创建一个 Node 独占的 Provider。
type Factory func(Context) (Provider, error)

// Provider 是 Origin、etcd、Consul 等发现后端共同实现的最小生命周期。
//
// 框架保证四个方法不会并发调用。实现内部可以并发收包，但必须串行调用 Host。
type Provider interface {
	// Start 建立后端连接、设置 TTL、完成首次完整快照并进入可恢复运行状态。
	Start(context.Context) error
	// Publish 幂等发布或更新当前 Node 的完整公开记录。
	Publish(context.Context, Node) error
	// Withdraw 幂等撤销当前 Node 的精确 Session。
	Withdraw(context.Context) error
	// Close 取消全部后台工作并等待 Provider 自有资源退出。
	Close(context.Context) error
}

// Context 是框架交给 Factory 的不可变 Node 身份和受限能力集合。
type Context struct {
	NodeID    string
	SessionID uint64
	Config    Config
	Host      Host
	Logger    originlog.Logger
}
