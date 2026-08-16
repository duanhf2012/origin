package tcpnet

import (
	"time"

	"github.com/duanhf2012/origin/v3/internal/bufferpool"
	"github.com/duanhf2012/origin/v3/internal/bytebudget"
	"github.com/duanhf2012/origin/v3/internal/messagequeue"
)

// sendItem 保存 TCP Writer 写一帧所需的长度头和唯一 Payload。
type sendItem struct {
	header      [4]byte
	headerSize  uint8
	buffer      *bufferpool.Buffer
	payloadSize int
	chargeBytes int64
	closeAfter  bool
}

// sendQueueSnapshot 保留 tcpnet 内部原有诊断形状，避免共享实现泄漏到具体传输 API。
type sendQueueSnapshot struct {
	messages     int
	bytes        int64
	highMessages int
	highBytes    int64
	writable     bool
	closed       bool
}

// sendQueue 是共享 messagequeue 的 TCP 薄适配，不建立第二层队列。
type sendQueue struct {
	shared *messagequeue.Queue[sendItem]
}

func newSendQueue(maxMessages int, maxBytes int64, budget *bytebudget.Budget) *sendQueue {
	shared, err := messagequeue.New(maxMessages, maxBytes, budget, func(item sendItem) {
		if item.buffer != nil {
			item.buffer.Release()
		}
	})
	if err != nil {
		panic("tcpnet: 未校验的发送队列配置")
	}
	return &sendQueue{shared: shared}
}

func (queue *sendQueue) enqueue(item sendItem) (bool, bool, error) {
	if item.buffer == nil || item.chargeBytes < 0 {
		panic("tcpnet: 非法发送队列项")
	}
	return queue.shared.Enqueue(item, item.chargeBytes)
}

func (queue *sendQueue) enqueueFinal(item sendItem) (bool, bool, error) {
	if item.buffer == nil || item.chargeBytes < 0 {
		panic("tcpnet: 非法最终发送队列项")
	}
	item.closeAfter = true
	return queue.shared.EnqueueFinal(item, item.chargeBytes)
}

func (queue *sendQueue) next() (sendItem, bool, bool, bool) {
	entry, ok, changed, writable := queue.shared.Next()
	if !ok {
		return sendItem{}, false, changed, writable
	}
	return entry.Value, true, changed, writable
}

func (queue *sendQueue) releaseItem(item *sendItem) {
	if item == nil || item.buffer == nil {
		return
	}
	entry := messagequeue.Entry[sendItem]{Value: *item, ChargeBytes: item.chargeBytes}
	queue.shared.Release(&entry)
	*item = sendItem{}
}

func (queue *sendQueue) close() { queue.shared.Close() }

func (queue *sendQueue) isClosed() bool { return queue.shared.IsClosed() }

func (queue *sendQueue) isSlow(timeout time.Duration) bool {
	return queue.shared.IsSlow(timeout)
}

func (queue *sendQueue) snapshot() sendQueueSnapshot {
	snapshot := queue.shared.Snapshot()
	return sendQueueSnapshot{
		messages:     snapshot.Messages,
		bytes:        snapshot.Bytes,
		highMessages: snapshot.HighMessages,
		highBytes:    snapshot.HighBytes,
		writable:     snapshot.Writable,
		closed:       snapshot.Closed,
	}
}
