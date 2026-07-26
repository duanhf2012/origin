package tcpnet

import (
	"sync"

	"github.com/duanhf2012/origin/v3/errs"
	"github.com/duanhf2012/origin/v3/internal/bufferpool"
)

// sendItem 是环形队列中的值类型槽位。
//
// 帧头与 Buffer 指针保存在同一槽位，发送时无需把完整 payload 再复制到连续大切片。
type sendItem struct {
	header      [4]byte
	headerSize  uint8
	buffer      *bufferpool.Buffer
	payloadSize int
}

// sendQueue 是按帧数和 payload 字节数双重限制的单消费者环形队列。
//
// 多个业务 goroutine 可以并发 enqueue，唯一 WriteLoop 调用 next。队列不关闭 wake channel，
// 从而避免并发 Send 与 Close 产生 send-on-closed-channel。
type sendQueue struct {
	mu sync.Mutex

	items []sendItem
	head  int
	tail  int
	count int
	bytes int

	maxBytes int
	closed   bool
	// wake 只表示“状态可能变化”，不承载队列项；容量一允许重复信号合并。
	wake chan struct{}
}

// newSendQueue 按已校验配置创建一条连接独占的固定槽位队列。
func newSendQueue(maxFrames, maxBytes int) *sendQueue {
	// 槽位只保存小型元数据，不预分配 maxBytes 对应的 payload 内存。
	return &sendQueue{
		items:    make([]sendItem, maxFrames),
		maxBytes: maxBytes,
		wake:     make(chan struct{}, 1),
	}
}

// enqueue 非阻塞地提交一帧，并在成功时接管 Buffer 所有权。
func (queue *sendQueue) enqueue(item sendItem) error {
	// 状态检查、双重额度校验和所有权提交必须处于同一个锁边界。
	queue.mu.Lock()
	if queue.closed {
		queue.mu.Unlock()
		return errs.ErrTransportClosed
	}
	if queue.count == len(queue.items) ||
		item.payloadSize > queue.maxBytes-queue.bytes {
		queue.mu.Unlock()
		return errs.ErrTransportOverloaded
	}

	// 写入尾槽后再更新计数，确保消费者看到的每一项都已经完整初始化。
	queue.items[queue.tail] = item
	queue.tail++
	if queue.tail == len(queue.items) {
		queue.tail = 0
	}
	queue.count++
	queue.bytes += item.payloadSize
	queue.mu.Unlock()

	// 唤醒信号可以合并；队列本身才是唯一事实来源。
	queue.signal()
	return nil
}

// next 等待并取得下一帧；队列关闭且没有剩余项时返回 false。
func (queue *sendQueue) next() (sendItem, bool) {
	for {
		// 每次被唤醒都重新在锁内检查队列，允许重复和提前信号。
		queue.mu.Lock()
		if queue.count > 0 {
			item := queue.items[queue.head]
			// 立即清空槽位引用，避免已经出队的 Buffer 被队列底层数组延长生命周期。
			queue.items[queue.head] = sendItem{}
			queue.head++
			if queue.head == len(queue.items) {
				queue.head = 0
			}
			queue.count--
			queue.bytes -= item.payloadSize
			queue.mu.Unlock()
			return item, true
		}
		if queue.closed {
			queue.mu.Unlock()
			return sendItem{}, false
		}
		queue.mu.Unlock()

		// enqueue 和 close 都会发送信号；收到后回到锁内判断真实状态。
		<-queue.wake
	}
}

// close 停止新准入并释放所有尚未被 WriteLoop 取得的 Buffer。
func (queue *sendQueue) close() {
	// 关闭状态和剩余槽位释放在同一个锁内提交，防止 Send 成功后无人释放。
	queue.mu.Lock()
	if queue.closed {
		queue.mu.Unlock()
		return
	}
	queue.closed = true

	// WriteLoop 已经取得的活动项不在队列中，由它自己的恢复边界释放。
	for queue.count > 0 {
		item := queue.items[queue.head]
		queue.items[queue.head] = sendItem{}
		queue.head++
		if queue.head == len(queue.items) {
			queue.head = 0
		}
		queue.count--
		queue.bytes -= item.payloadSize
		item.buffer.Release()
	}
	queue.mu.Unlock()

	// 唤醒可能正在空队列上等待的 WriteLoop，使其观察 closed 并退出。
	queue.signal()
}

// isClosed 报告队列是否已经停止发送准入。
func (queue *sendQueue) isClosed() bool {
	// 该查询只用于回调返回后的快速终止，不承担 Send 准入原子性。
	queue.mu.Lock()
	closed := queue.closed
	queue.mu.Unlock()
	return closed
}

// snapshot 返回测试和内部诊断使用的当前帧数、字节数和关闭状态。
func (queue *sendQueue) snapshot() (frames, bytes int, closed bool) {
	// 快照字段在同一锁内读取，保证测试不会看到互相矛盾的水位。
	queue.mu.Lock()
	frames, bytes, closed = queue.count, queue.bytes, queue.closed
	queue.mu.Unlock()
	return frames, bytes, closed
}

// signal 合并唤醒通知，调用方永远不会因 Writer 尚未等待而阻塞。
func (queue *sendQueue) signal() {
	// wake 永不关闭，因此该非阻塞发送与 Close 并发时也是安全的。
	select {
	case queue.wake <- struct{}{}:
	default:
	}
}
