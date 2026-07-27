// Package node 提供 Origin Node 的配置、Service 所有权和最小生命周期。
package node

// State 表示 Node 在当前 Application 中的生命周期状态。
type State uint8

const (
	// StateCreated 表示 Node 已经装配完成但尚未启动。
	StateCreated State = iota
	// StateStarting 表示 Node 正在初始化或启动 Service。
	StateStarting
	// StateReady 表示全部 Service 已经成功进入 Running。
	StateReady
	// StateStopping 表示 Node 正在严格反序停止 Service。
	StateStopping
	// StateStopped 表示 Node 已完成正常停止。
	StateStopped
	// StateFailed 表示 Node 初始化或启动失败并且不能原地重试。
	StateFailed
)
