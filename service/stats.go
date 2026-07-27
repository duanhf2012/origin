package service

// ExecutionStats 是一个 ServiceScheduler 在同一时刻的一致统计快照。
type ExecutionStats struct {
	// Accepted 是已经接收但尚未完整返回的根任务数。
	Accepted int
	// Ready 是当前位于 FIFO 中等待执行或恢复的任务数。
	Ready int
	// Running 是当前持有 Service 执行权的任务数，只能为零或一。
	Running int
	// Awaiting 是已经释放执行权但尚未从 Await 返回的任务数。
	Awaiting int
	// AcceptedHighWatermark 是当前 Scheduler 生命周期内 Accepted 的最高值。
	AcceptedHighWatermark int
	// DispatchedTotal 是成功接收的新根任务累计数量。
	DispatchedTotal uint64
	// CompletedTotal 是普通返回或被 panic 边界清理的根任务累计数量。
	CompletedTotal uint64
	// RejectedTotal 是因状态、参数之外的容量过载而拒绝的任务累计数量。
	RejectedTotal uint64
	// AwaitTotal 是成功释放执行权的 Await 累计数量。
	AwaitTotal uint64
	// AwaitCanceledTotal 是最终因非 Deadline Context 取消返回的 Await 累计数量。
	AwaitCanceledTotal uint64
	// AwaitTimeoutTotal 是最终因 Deadline 返回的 Await 累计数量。
	AwaitTimeoutTotal uint64
	// PanicTotal 是业务根任务边界捕获的 panic 累计数量。
	PanicTotal uint64
}
