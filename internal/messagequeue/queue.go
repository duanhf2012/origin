// Package messagequeue 提供网络传输内部共享的有界单消费者发送 Ring。
//
// 本包只管理 FIFO、消息/字节容量、端点总预算、水位和唯一值释放；具体传输负责定义队列项并
// 完成实际写出。它是 internal 实现细节，不向业务暴露队列或 Buffer 所有权。
package messagequeue

import (
	"errors"
	"sync"
	"time"

	"github.com/duanhf2012/origin/v3/errs"
	"github.com/duanhf2012/origin/v3/internal/bytebudget"
	"github.com/duanhf2012/origin/v3/internal/container/ringqueue"
)

const defaultInitialCapacity = 16

// Entry 是消费者从队列取得、尚未归还端点总预算的唯一值。
type Entry[T any] struct {
	Value       T
	ChargeBytes int64
}

// Snapshot 是同一锁时刻的发送队列诊断快照。
type Snapshot struct {
	Messages     int
	Bytes        int64
	HighMessages int
	HighBytes    int64
	Writable     bool
	Closed       bool
}

type queuedEntry[T any] struct {
	value       T
	chargeBytes int64
}

// Queue 是多生产者、单消费者的有界 FIFO。
//
// wake 只合并状态通知且永不关闭；队列状态始终以锁内 Ring 为准，因此并发 Enqueue 与 Close 不会
// 产生 send-on-closed-channel。release 在值离开队列且不再被 Writer 使用后恰好调用一次。
type Queue[T any] struct {
	mu sync.Mutex

	items *ringqueue.Queue[queuedEntry[T]]
	wake  chan struct{}

	maxBytes int64
	budget   *bytebudget.Budget
	release  func(T)
	bytes    int64

	highMessages int
	lowMessages  int
	highBytes    int64
	lowBytes     int64
	writable     bool
	highSince    time.Time
	closed       bool
}

// New 创建惰性分配槽位、同时限制消息数和保留容量的队列。
func New[T any](
	maxMessages int,
	maxBytes int64,
	budget *bytebudget.Budget,
	release func(T),
) (*Queue[T], error) {
	if maxMessages <= 0 || maxBytes <= 0 || budget == nil || release == nil {
		return nil, errors.New("messagequeue: 配置无效")
	}
	initial := min(defaultInitialCapacity, maxMessages)
	items, err := ringqueue.New[queuedEntry[T]](initial, maxMessages)
	if err != nil {
		return nil, err
	}
	return &Queue[T]{
		items:        items,
		wake:         make(chan struct{}, 1),
		maxBytes:     maxBytes,
		budget:       budget,
		release:      release,
		highMessages: max(1, (maxMessages*80+99)/100),
		lowMessages:  maxMessages / 2,
		highBytes:    max(1, (maxBytes*80+99)/100),
		lowBytes:     maxBytes / 2,
		writable:     true,
	}, nil
}

// Enqueue 非阻塞提交一个值；成功时队列接管值和端点总预算，失败时所有权仍属于调用方。
func (queue *Queue[T]) Enqueue(value T, chargeBytes int64) (
	changed bool,
	writable bool,
	err error,
) {
	return queue.enqueue(value, chargeBytes, false)
}

// EnqueueFinal 原子提交最后一个值并停止后续准入；消费者仍会按 FIFO 取完已有值和该值。
func (queue *Queue[T]) EnqueueFinal(value T, chargeBytes int64) (
	changed bool,
	writable bool,
	err error,
) {
	return queue.enqueue(value, chargeBytes, true)
}

func (queue *Queue[T]) enqueue(value T, chargeBytes int64, final bool) (
	changed bool,
	writable bool,
	err error,
) {
	if queue == nil || chargeBytes < 0 {
		return false, false, errs.ErrInvalidArgument
	}
	queue.mu.Lock()
	if queue.closed {
		queue.mu.Unlock()
		return false, false, errs.ErrTransportClosed
	}
	if queue.items.Len() >= queue.items.MaxCap() ||
		chargeBytes > queue.maxBytes-queue.bytes {
		queue.mu.Unlock()
		return false, queue.writable, errs.ErrTransportOverloaded
	}
	if !queue.budget.TryAcquire(chargeBytes) {
		queue.mu.Unlock()
		return false, queue.writable, errs.ErrTransportOverloaded
	}
	if !queue.items.Enqueue(queuedEntry[T]{value: value, chargeBytes: chargeBytes}) {
		queue.budget.Release(chargeBytes)
		queue.mu.Unlock()
		panic("messagequeue: Ring 在容量检查后拒绝入队")
	}
	queue.bytes += chargeBytes
	changed, writable = queue.updateWritabilityLocked(time.Time{})
	if final {
		queue.closed = true
		if writable {
			changed = true
		}
		writable = false
	}
	queue.mu.Unlock()
	queue.signal()
	return changed, writable, nil
}

// Next 等待并取得下一项；队列关闭且排空后返回 ok=false。
//
// 返回 Entry 仍持有端点总预算，Writer 必须在不再引用 Value 后调用 Release。
func (queue *Queue[T]) Next() (
	entry Entry[T],
	ok bool,
	changed bool,
	writable bool,
) {
	if queue == nil {
		return Entry[T]{}, false, false, false
	}
	for {
		queue.mu.Lock()
		if queue.items.Len() > 0 {
			item, dequeued := queue.items.Dequeue()
			if !dequeued {
				queue.mu.Unlock()
				panic("messagequeue: 非空 Ring 无法出队")
			}
			queue.bytes -= item.chargeBytes
			if queue.bytes < 0 {
				queue.mu.Unlock()
				panic("messagequeue: 队列字节记账为负数")
			}
			changed, writable = queue.updateWritabilityLocked(time.Time{})
			queue.mu.Unlock()
			return Entry[T]{Value: item.value, ChargeBytes: item.chargeBytes}, true,
				changed, writable
		}
		if queue.closed {
			queue.mu.Unlock()
			return Entry[T]{}, false, false, false
		}
		queue.mu.Unlock()
		<-queue.wake
	}
}

// Release 在 Writer 不再引用 Entry 后释放具体值和端点总预算。
func (queue *Queue[T]) Release(entry *Entry[T]) {
	if queue == nil || entry == nil || entry.ChargeBytes < 0 {
		return
	}
	queue.release(entry.Value)
	queue.budget.Release(entry.ChargeBytes)
	*entry = Entry[T]{}
}

// Close 停止新准入并释放全部尚未被消费者取得的值。
func (queue *Queue[T]) Close() {
	if queue == nil {
		return
	}
	queue.mu.Lock()
	if queue.closed && queue.items.Len() == 0 {
		queue.mu.Unlock()
		return
	}
	queue.closed = true
	entries := make([]Entry[T], 0, queue.items.Len())
	for queue.items.Len() > 0 {
		item, ok := queue.items.Dequeue()
		if !ok {
			queue.mu.Unlock()
			panic("messagequeue: Close 无法排空 Ring")
		}
		queue.bytes -= item.chargeBytes
		entries = append(entries, Entry[T]{Value: item.value, ChargeBytes: item.chargeBytes})
	}
	if queue.bytes != 0 {
		queue.mu.Unlock()
		panic("messagequeue: Close 后队列字节未归零")
	}
	queue.mu.Unlock()

	for index := range entries {
		queue.Release(&entries[index])
	}
	queue.signal()
}

// IsClosed 报告队列是否停止发送准入。
func (queue *Queue[T]) IsClosed() bool {
	if queue == nil {
		return true
	}
	queue.mu.Lock()
	closed := queue.closed
	queue.mu.Unlock()
	return closed
}

// IsSlow 报告队列是否连续处于高水位超过 timeout。
func (queue *Queue[T]) IsSlow(timeout time.Duration) bool {
	if queue == nil {
		return false
	}
	queue.mu.Lock()
	slow := !queue.writable && !queue.highSince.IsZero() &&
		time.Since(queue.highSince) >= timeout
	queue.mu.Unlock()
	return slow
}

// Snapshot 返回当前消息、字节、水位、可写和关闭状态。
func (queue *Queue[T]) Snapshot() Snapshot {
	if queue == nil {
		return Snapshot{Closed: true}
	}
	queue.mu.Lock()
	snapshot := Snapshot{
		Messages:     queue.items.Len(),
		Bytes:        queue.bytes,
		HighMessages: queue.highMessages,
		HighBytes:    queue.highBytes,
		Writable:     queue.writable && !queue.closed,
		Closed:       queue.closed,
	}
	queue.mu.Unlock()
	return snapshot
}

func (queue *Queue[T]) updateWritabilityLocked(now time.Time) (bool, bool) {
	messages := queue.items.Len()
	if queue.writable {
		if messages < queue.highMessages && queue.bytes < queue.highBytes {
			return false, true
		}
		if now.IsZero() {
			now = time.Now()
		}
		queue.writable = false
		queue.highSince = now
		return true, false
	}
	if messages > queue.lowMessages || queue.bytes > queue.lowBytes {
		return false, false
	}
	queue.writable = true
	queue.highSince = time.Time{}
	return true, true
}

func (queue *Queue[T]) signal() {
	select {
	case queue.wake <- struct{}{}:
	default:
	}
}
