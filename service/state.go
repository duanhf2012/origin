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
	// StateRetired 表示 Service 仍在运行，但默认服务发现选择不再把它作为候选。
	StateRetired
	// StateStopping 表示框架正在调用 OnStop。
	StateStopping
	// StateStopped 表示 OnStop 已经完成，实例不能再次启动。
	StateStopped
	// StateFailed 表示初始化、启动失败，或运行期 Scheduler 已无法证明状态安全。
	StateFailed
)

// String 返回用于日志、事件和诊断的稳定小写状态名。
func (state State) String() string {
	switch state {
	case StateCreated:
		return "created"
	case StateInitializing:
		return "initializing"
	case StateInitialized:
		return "initialized"
	case StateStarting:
		return "starting"
	case StateRunning:
		return "running"
	case StateRetired:
		return "retired"
	case StateStopping:
		return "stopping"
	case StateStopped:
		return "stopped"
	case StateFailed:
		return "failed"
	default:
		return "unknown"
	}
}
