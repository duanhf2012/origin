package tcpnet

import (
	"sync"
	"time"

	"github.com/duanhf2012/origin/v3/errs"
	"github.com/duanhf2012/origin/v3/internal/bufferpool"
	"github.com/duanhf2012/origin/v3/internal/bytebudget"
	"github.com/duanhf2012/origin/v3/internal/container/ringqueue"
)

const defaultSendQueueInitialCapacity = 16

// sendItem 是发送队列中的值类型槽位。
//
// 帧头与 Buffer 指针保存在同一项，发送时无需把完整 payload 再复制到连续大切片。
type sendItem struct {
	header      [4]byte
	headerSize  uint8
	buffer      *bufferpool.Buffer
	payloadSize int
	chargeBytes int64
}

// sendQueueSnapshot 是同一锁时刻的发送队列诊断快照。
type sendQueueSnapshot struct {
	messages     int
	bytes        int64
	highMessages int
	highBytes    int64
	writable     bool
	closed       bool
}

// sendQueue 是 MPSC、单消费者的有界 FIFO。
//
// 多个业务 goroutine 可以并发 enqueue，唯一 WriteLoop 调用 next。Ring 只保存数据，wake 只合并
// 状态变化；队列不关闭 wake Channel，从而避免并发 Send 与 Close 产生 send-on-closed-channel。
type sendQueue struct {
	mu sync.Mutex

	items *ringqueue.Queue[sendItem]
	wake  chan struct{}

	maxBytes int64
	budget   *bytebudget.Budget
	bytes    int64

	highMessages int
	lowMessages  int
	highBytes    int64
	lowBytes     int64
	writable     bool
	highSince    time.Time
	closed       bool
}

// newSendQueue 创建一条惰性分配槽位、同时限制消息数和保留容量的连接队列。
func newSendQueue(maxMessages int, maxBytes int64, budget *bytebudget.Budget) *sendQueue {
	// 所有参数已经由 ConnectionOptions 校验；这里的失败只可能来自内部调用破坏不变量。
	initial := min(defaultSendQueueInitialCapacity, maxMessages)
	items, err := ringqueue.New[sendItem](initial, maxMessages)
	if err != nil || maxBytes <= 0 || budget == nil {
		panic("tcpnet: 未校验的发送队列配置")
	}

	// 高水位向上取整保证小容量队列至少在接近满时变为不可写；低水位向下取整形成迟滞。
	return &sendQueue{
		items:        items,
		wake:         make(chan struct{}, 1),
		maxBytes:     maxBytes,
		budget:       budget,
		highMessages: max(1, (maxMessages*80+99)/100),
		lowMessages:  maxMessages / 2,
		highBytes:    max(1, (maxBytes*80+99)/100),
		lowBytes:     maxBytes / 2,
		writable:     true,
	}
}

// enqueue 非阻塞地提交一帧，并在成功时接管 Buffer 与总预算所有权。
//
// changed/writable 只在本次调用跨越高水位时有效；调用方在锁外通知可写状态。
func (queue *sendQueue) enqueue(item sendItem) (
	changed bool,
	writable bool,
	err error,
) {
	// 状态、Session 双维额度、Module 总额度和所有权提交属于同一个可回滚锁事务。
	queue.mu.Lock()
	if queue.closed {
		queue.mu.Unlock()
		return false, false, errs.ErrTransportClosed
	}
	if item.buffer == nil || item.chargeBytes < 0 {
		queue.mu.Unlock()
		panic("tcpnet: 非法发送队列项")
	}
	if queue.items.Len() >= queue.items.MaxCap() ||
		item.chargeBytes > queue.maxBytes-queue.bytes {
		queue.mu.Unlock()
		return false, queue.writable, errs.ErrTransportOverloaded
	}
	if !queue.budget.TryAcquire(item.chargeBytes) {
		queue.mu.Unlock()
		return false, queue.writable, errs.ErrTransportOverloaded
	}
	if !queue.items.Enqueue(item) {
		// 已在同一锁内检查 MaxCap，到达这里表示 Ring 内部状态被破坏；先回滚额度再暴露错误。
		queue.budget.Release(item.chargeBytes)
		queue.mu.Unlock()
		panic("tcpnet: 发送 Ring 在容量检查后拒绝入队")
	}
	queue.bytes += item.chargeBytes
	changed, writable = queue.updateWritabilityLocked(time.Time{})
	queue.mu.Unlock()

	// 唤醒信号可以合并；队列本身才是唯一事实来源。
	queue.signal()
	return changed, writable, nil
}

// next 等待并取得下一帧；队列关闭且没有剩余项时返回 false。
//
// changed/writable 只在出队使容量降到低水位时有效。Module 总预算仍随返回项保留，直到 Writer
// 完成写入并调用 releaseItem。
func (queue *sendQueue) next() (
	item sendItem,
	ok bool,
	changed bool,
	writable bool,
) {
	for {
		// 每次被唤醒都重新在锁内检查队列，允许重复和提前信号。
		queue.mu.Lock()
		if queue.items.Len() > 0 {
			item, ok = queue.items.Dequeue()
			if !ok {
				queue.mu.Unlock()
				panic("tcpnet: 非空发送 Ring 无法出队")
			}
			queue.bytes -= item.chargeBytes
			if queue.bytes < 0 {
				queue.mu.Unlock()
				panic("tcpnet: 发送队列字节记账为负数")
			}
			changed, writable = queue.updateWritabilityLocked(time.Time{})
			queue.mu.Unlock()
			return item, true, changed, writable
		}
		if queue.closed {
			queue.mu.Unlock()
			return sendItem{}, false, false, false
		}
		queue.mu.Unlock()

		// enqueue 和 close 都会发送信号；收到后回到锁内判断真实状态。
		<-queue.wake
	}
}

// releaseItem 在 Writer 不再引用 Payload 后释放 Buffer 和 Module 总字节额度。
func (queue *sendQueue) releaseItem(item *sendItem) {
	// Writer 对活动项拥有唯一所有权；先断开 Buffer，再归还与其容量对应的总额度。
	if item == nil || item.buffer == nil {
		return
	}
	item.buffer.Release()
	queue.budget.Release(item.chargeBytes)
	*item = sendItem{}
}

// close 停止新准入并释放所有尚未被 WriteLoop 取得的 Buffer 与总预算。
func (queue *sendQueue) close() {
	// 关闭状态和剩余槽位提取在同一个锁内提交，防止 Send 成功后无人释放。
	queue.mu.Lock()
	if queue.closed {
		queue.mu.Unlock()
		return
	}
	queue.closed = true
	items := make([]sendItem, 0, queue.items.Len())
	for queue.items.Len() > 0 {
		item, ok := queue.items.Dequeue()
		if !ok {
			queue.mu.Unlock()
			panic("tcpnet: Close 无法排空发送 Ring")
		}
		queue.bytes -= item.chargeBytes
		items = append(items, item)
	}
	if queue.bytes != 0 {
		queue.mu.Unlock()
		panic("tcpnet: Close 后发送队列字节未归零")
	}
	queue.mu.Unlock()

	// Buffer Release 和原子 Budget Release 不需要持有队列锁，减少 Close 与并发诊断的临界区。
	for index := range items {
		queue.releaseItem(&items[index])
	}
	queue.signal()
}

// isClosed 报告队列是否已经停止发送准入。
func (queue *sendQueue) isClosed() bool {
	queue.mu.Lock()
	closed := queue.closed
	queue.mu.Unlock()
	return closed
}

// isSlow 报告队列是否连续处于高水位超过 timeout。
func (queue *sendQueue) isSlow(timeout time.Duration) bool {
	queue.mu.Lock()
	slow := !queue.writable && !queue.highSince.IsZero() &&
		time.Since(queue.highSince) >= timeout
	queue.mu.Unlock()
	return slow
}

// snapshot 返回当前消息、字节、水位、可写和关闭状态。
func (queue *sendQueue) snapshot() sendQueueSnapshot {
	queue.mu.Lock()
	snapshot := sendQueueSnapshot{
		messages:     queue.items.Len(),
		bytes:        queue.bytes,
		highMessages: queue.highMessages,
		highBytes:    queue.highBytes,
		writable:     queue.writable && !queue.closed,
		closed:       queue.closed,
	}
	queue.mu.Unlock()
	return snapshot
}

// updateWritabilityLocked 按固定 80%/50% 双维水位更新迟滞状态。
func (queue *sendQueue) updateWritabilityLocked(now time.Time) (bool, bool) {
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

// signal 合并唤醒通知，调用方永远不会因 Writer 尚未等待而阻塞。
func (queue *sendQueue) signal() {
	select {
	case queue.wake <- struct{}{}:
	default:
	}
}
