// Package discovery 定义 Origin 业务代码可见的服务发现只读类型。
//
// 本包不提供目录修改、Provider、地址或 Transport API；所有查询、等待和监听入口直接组合
// 在 service.Service 上。
package discovery

// State 是远端 Service 对业务公开的最小运行状态。
type State uint8

const (
	// StateUnknown 只作为零值和非法输入防护，不会出现在有效查询或事件中。
	StateUnknown State = iota
	// StateRunning 表示远端 Service 正常运行。
	StateRunning
	// StateRetired 表示远端 Service 处于可观察的退休状态；普通 RPC 和其他业务仍按 Running
	// 规则运行，是否拒绝具体请求由业务自行决定。
	StateRetired
)

// Instance 是业务独立持有的一份远端 Service 发现记录。
//
// Labels 由框架深复制；业务可以在返回后保存或修改，不会污染 Node 内部不可变目录。
type Instance struct {
	NodeID      string
	SessionID   string
	ServiceName string
	State       State
	Labels      map[string]string
}

// Service 是一次发现事件中一个远端 Service 的名称和状态。
type Service struct {
	ServiceName string
	State       State
}

// Event 按远端 Node 批量携带稳定排序的 Service 变化。
//
// 每个监听器取得独立的 Services Slice，可以在回调返回后继续持有。
type Event struct {
	NodeID   string
	Services []Service
}
