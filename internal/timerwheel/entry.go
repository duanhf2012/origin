package timerwheel

// DeadlineID 是 Engine 生命周期内单调递增且永不复用的一次性 Deadline 身份。
type DeadlineID uint64

const (
	// InvalidDeadlineID 是不指向任何 Deadline 的零值。
	InvalidDeadlineID DeadlineID = 0
)

// entryState 描述 timerEntry 的唯一所有权阶段。
type entryState uint8

const (
	entryFree entryState = iota
	entryScheduled
)

// timerEntry 同时作为时间轮桶节点和所属 DeadlineQueue 的 Scheduled 节点。
//
// 两组链表指针互相独立，使已知 ID 取消和 Queue 批量关闭都无需扫描其他条目。
type timerEntry struct {
	id           DeadlineID
	deadlineTick uint64
	queue        *DeadlineQueue

	wheelPrev *timerEntry
	wheelNext *timerEntry
	queuePrev *timerEntry
	queueNext *timerEntry

	level uint8
	slot  uint8
	state entryState
}
