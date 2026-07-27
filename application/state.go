package application

// State 表示 Application 的一次性生命周期状态。
type State uint8

const (
	// StateCreated 表示 Application 尚未执行任何命令。
	StateCreated State = iota
	// StateStarting 表示配置、Node 或 Service 正在启动。
	StateStarting
	// StateRunning 表示所有选中 Node 都已经启动完成。
	StateRunning
	// StateStopping 表示 Application 正在按反序停止 Node。
	StateStopping
	// StateStopped 表示 Application 已经完成正常停止。
	StateStopped
	// StateFailed 表示启动或停止阶段发生错误，Application 不能再次启动。
	StateFailed
)
