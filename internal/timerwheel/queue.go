package timerwheel

import (
	"fmt"
	"time"
)

// DeadlineQueue 隔离一个上层组件登记和接收的全部 Deadline。
//
// Queue 只能由 Engine 创建。关闭 Queue 会批量取消其未到期条目并清空已到期 ID。
type DeadlineQueue struct {
	engine *Engine
	signal chan struct{}
	closed bool

	scheduledHead *timerEntry
	scheduledTail *timerEntry
	expired       idRing
}

// ScheduleAfter 登记一个相对于当前单调时间的一次性 Deadline。
func (queue *DeadlineQueue) ScheduleAfter(delay time.Duration) (DeadlineID, error) {
	// 实际实现统一放在 Engine，Queue 只负责所有权校验。
	if queue == nil || queue.engine == nil {
		return InvalidDeadlineID, invalidArgument("DeadlineQueue 不能为空")
	}
	return queue.engine.scheduleAfter(queue, delay)
}

// RescheduleAfter 保留已登记 Deadline 的 ID 和 Queue 所有权，只把它的到期点改为从当前起的 delay。
//
// 返回 false、nil 表示 ID 不存在、已取消、已到期或不属于当前 Queue。该区分让上层
// 在到期竞争中可以改用新 Deadline；非 nil error 只表示参数、Engine 或 Queue 状态无效。
func (queue *DeadlineQueue) RescheduleAfter(
	id DeadlineID,
	delay time.Duration,
) (bool, error) {
	if queue == nil || queue.engine == nil {
		return false, invalidArgument("DeadlineQueue 不能为空")
	}
	return queue.engine.rescheduleAfter(queue, id, delay)
}

// Cancel 取消仍位于当前 Queue 时间轮中的 Deadline。
//
// ID 不存在、已经到期、已取消或属于其他 Queue 时返回 false。
func (queue *DeadlineQueue) Cancel(id DeadlineID) bool {
	if queue == nil || queue.engine == nil || id == InvalidDeadlineID {
		return false
	}
	return queue.engine.cancel(queue, id)
}

// ExpiredSignal 返回容量为 1 的合并到期通知 Channel。
//
// 收到通知后必须调用 DrainExpired 获取真实 ID；Queue 关闭时 Channel 会关闭。
func (queue *DeadlineQueue) ExpiredSignal() <-chan struct{} {
	if queue == nil {
		return nil
	}
	return queue.signal
}

// DrainExpired 按交付顺序最多取出 limit 个到期 ID。
//
// dst 的现有元素会保留，新结果追加到其末尾；调用方通常传入 dst[:0] 复用容量。
func (queue *DeadlineQueue) DrainExpired(
	dst []DeadlineID,
	limit int,
) ([]DeadlineID, error) {
	// limit 必须显式为正数，避免零值被误解成“无限”而破坏批量公平性。
	if queue == nil || queue.engine == nil {
		return dst, invalidArgument("DeadlineQueue 不能为空")
	}
	if limit <= 0 {
		return dst, invalidArgument("DrainExpired limit 必须大于 0")
	}

	// Engine 单锁同时裁决到期追加、Queue 关闭和本次 Drain。
	engine := queue.engine
	engine.mu.Lock()
	defer engine.mu.Unlock()
	if queue.closed {
		return dst, ErrDeadlineQueueClosed
	}

	// 批量弹出并同步维护 Engine 当前 Expired 统计。
	count := min(limit, queue.expired.Len())
	for index := 0; index < count; index++ {
		id, ok := queue.expired.Pop()
		if !ok {
			return dst, internalError("DeadlineQueue 到期环形队列状态损坏")
		}
		dst = append(dst, id)
	}
	engine.stats.expired -= uint64(count)

	// 仍有剩余时补回合并信号，保证一次只处理部分 ID 不会丢失后续唤醒。
	if queue.expired.Len() > 0 {
		queue.notifyLocked()
	}
	return dst, nil
}

// Close 幂等关闭 Queue，并清理未到期和已到期的全部 ID。
func (queue *DeadlineQueue) Close() {
	if queue == nil || queue.engine == nil {
		return
	}

	// Queue 清理与到期、取消共享 Engine 锁，最终所有权只能由一条路径取得。
	engine := queue.engine
	engine.mu.Lock()
	changed := engine.closeQueueLocked(queue)
	engine.mu.Unlock()
	if changed {
		// 删除最早 Deadline 后唤醒工作 goroutine，使其重新计算休眠目标。
		engine.notifyChange()
	}
}

// addScheduledLocked 把条目尾插到 Queue 私有 Scheduled 链表。
func (queue *DeadlineQueue) addScheduledLocked(entry *timerEntry) {
	entry.queuePrev = queue.scheduledTail
	entry.queueNext = nil
	if queue.scheduledTail == nil {
		queue.scheduledHead = entry
	} else {
		queue.scheduledTail.queueNext = entry
	}
	queue.scheduledTail = entry
}

// removeScheduledLocked 从 Queue 私有 Scheduled 链表移除已知条目。
func (queue *DeadlineQueue) removeScheduledLocked(entry *timerEntry) {
	if entry.queuePrev == nil {
		queue.scheduledHead = entry.queueNext
	} else {
		entry.queuePrev.queueNext = entry.queueNext
	}
	if entry.queueNext == nil {
		queue.scheduledTail = entry.queuePrev
	} else {
		entry.queueNext.queuePrev = entry.queuePrev
	}
	entry.queuePrev = nil
	entry.queueNext = nil
}

// notifyLocked 非阻塞合并到期通知；调用方必须持有 Engine 锁。
func (queue *DeadlineQueue) notifyLocked() {
	if queue.closed {
		return
	}
	select {
	case queue.signal <- struct{}{}:
	default:
	}
}

// idRing 是保留容量并主动清空已消费槽位的 DeadlineID 环形队列。
type idRing struct {
	items []DeadlineID
	head  int
	size  int
}

// Len 返回当前尚未消费的 ID 数量。
func (ring *idRing) Len() int {
	return ring.size
}

// Push 尾插一个 ID，并按需以两倍容量增长。
func (ring *idRing) Push(id DeadlineID) {
	// 首次使用分配一个小批次，后续消费保留容量避免稳定负载反复分配。
	if len(ring.items) == 0 {
		ring.items = make([]DeadlineID, 16)
	}
	if ring.size == len(ring.items) {
		ring.grow()
	}
	tail := (ring.head + ring.size) % len(ring.items)
	ring.items[tail] = id
	ring.size++
}

// Pop 从头部取出一个 ID，并清空槽位避免陈旧值影响诊断。
func (ring *idRing) Pop() (DeadlineID, bool) {
	if ring.size == 0 {
		return InvalidDeadlineID, false
	}
	id := ring.items[ring.head]
	ring.items[ring.head] = InvalidDeadlineID
	ring.head = (ring.head + 1) % len(ring.items)
	ring.size--
	if ring.size == 0 {
		ring.head = 0
	}
	return id, true
}

// Clear 清空全部有效 ID 并返回清理数量，同时保留底层容量供关闭诊断前复用。
func (ring *idRing) Clear() int {
	count := ring.size
	for ring.size > 0 {
		_, _ = ring.Pop()
	}
	return count
}

// grow 保持当前逻辑顺序复制到两倍容量的新数组。
func (ring *idRing) grow() {
	if len(ring.items) == 0 {
		panic(fmt.Sprintf("timerwheel: 非法环形队列容量 %d", len(ring.items)))
	}
	next := make([]DeadlineID, len(ring.items)*2)
	for index := 0; index < ring.size; index++ {
		next[index] = ring.items[(ring.head+index)%len(ring.items)]
	}
	ring.items = next
	ring.head = 0
}
