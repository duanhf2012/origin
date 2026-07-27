// Package service 提供 Origin 业务 Service 的基础类型与最小生命周期契约。
package service

// State 表示 Service 在当前进程中的生命周期状态。
type State uint8

const (
	// StateCreated 表示实例已经创建并绑定，但尚未开始初始化。
	StateCreated State = iota
	// StateInitializing 表示框架正在调用 OnInit。
	StateInitializing
	// StateInitialized 表示 OnInit 已经成功完成。
	StateInitialized
	// StateStarting 表示框架正在调用 OnStart。
	StateStarting
	// StateRunning 表示 OnStart 已经成功完成。
	StateRunning
	// StateStopping 表示框架正在调用 OnStop。
	StateStopping
	// StateStopped 表示 OnStop 已经完成，实例不能再次启动。
	StateStopped
	// StateFailed 表示初始化或启动回调失败。
	StateFailed
)
