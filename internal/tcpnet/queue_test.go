package tcpnet

import (
	"errors"
	"testing"
	"time"

	"github.com/duanhf2012/origin/v3/errs"
	"github.com/duanhf2012/origin/v3/internal/bufferpool"
	"github.com/duanhf2012/origin/v3/internal/bytebudget"
)

func TestSendQueueLimitsAndFIFO(t *testing.T) {
	t.Parallel()

	// 三个槽位覆盖消息数满、空帧以及 FIFO 环回，同时保留明确字节和总预算。
	pool := bufferpool.NewPool(bufferpool.Options{TrackUsage: true})
	queue := newTestSendQueue(t, 3, 256*1024)
	first := newQueueItem(pool, []byte{1, 2})
	empty := newQueueItem(pool, nil)
	second := newQueueItem(pool, []byte{3, 4})

	if err := testEnqueue(queue, first); err != nil {
		t.Fatalf("enqueue first: %v", err)
	}
	if err := testEnqueue(queue, empty); err != nil {
		t.Fatalf("enqueue empty: %v", err)
	}
	if err := testEnqueue(queue, second); err != nil {
		t.Fatalf("enqueue second: %v", err)
	}

	// 三个槽位已经全部使用，即使 payload 为零也必须返回过载。
	rejected := newQueueItem(pool, nil)
	if err := testEnqueue(queue, rejected); !errors.Is(err, errs.ErrTransportOverloaded) {
		t.Fatalf("frame limit error = %v", err)
	}
	// 入队失败没有转移所有权，测试调用方负责释放。
	rejected.buffer.Release()

	// 依次出队并释放，验证零长度项没有破坏顺序。
	for index, want := range [][]byte{{1, 2}, {}, {3, 4}} {
		item, ok := testNext(queue)
		if !ok {
			t.Fatalf("next %d 提前结束", index)
		}
		if got := item.buffer.Bytes(); !equalBytes(got, want) {
			t.Fatalf("next %d = %v，期望 %v", index, got, want)
		}
		queue.releaseItem(&item)
	}
	if snapshot := queue.snapshot(); snapshot.messages != 0 || snapshot.bytes != 0 || snapshot.closed {
		t.Fatalf("空队列快照 = %+v", snapshot)
	}

	// 环形下标已经跨过尾部，再次入队必须仍可正常工作。
	item := newQueueItem(pool, make([]byte, 64*1024))
	if err := testEnqueue(queue, item); err != nil {
		t.Fatalf("enqueue wrapped item: %v", err)
	}
	// 第二条大消息只受剩余槽位约束，证明队列不再按总字节数拒绝合法消息。
	secondLarge := newQueueItem(pool, make([]byte, 64*1024))
	if err := testEnqueue(queue, secondLarge); err != nil {
		t.Fatalf("enqueue second large item: %v", err)
	}

	got, ok := testNext(queue)
	if !ok || len(got.buffer.Bytes()) != 64*1024 {
		t.Fatalf("wrapped next = (%v, %v)", got.buffer, ok)
	}
	queue.releaseItem(&got)
	got, ok = testNext(queue)
	if !ok || len(got.buffer.Bytes()) != 64*1024 {
		t.Fatalf("second large next = (%v, %v)", got.buffer, ok)
	}
	queue.releaseItem(&got)
	queue.close()
	assertPoolReleased(t, pool)
}

func TestSendQueueCloseReleasesAndWakes(t *testing.T) {
	t.Parallel()

	// 先放入两个待发送 Buffer，Close 必须接管并释放它们。
	pool := bufferpool.NewPool(bufferpool.Options{TrackUsage: true})
	queue := newTestSendQueue(t, 2, 64)
	if err := testEnqueue(queue, newQueueItem(pool, []byte{1})); err != nil {
		t.Fatalf("enqueue first: %v", err)
	}
	if err := testEnqueue(queue, newQueueItem(pool, []byte{2})); err != nil {
		t.Fatalf("enqueue second: %v", err)
	}
	queue.close()
	queue.close()

	// 已关闭队列不能再接管新 Buffer。
	rejected := newQueueItem(pool, []byte{3})
	if err := testEnqueue(queue, rejected); !errors.Is(err, errs.ErrTransportClosed) {
		t.Fatalf("enqueue after close error = %v", err)
	}
	rejected.buffer.Release()

	// next 必须立即观察关闭，不在 wake channel 上永久等待。
	done := make(chan bool, 1)
	go func() {
		_, ok := testNext(queue)
		done <- ok
	}()
	select {
	case ok := <-done:
		if ok {
			t.Fatal("关闭且排空的队列仍返回了元素")
		}
	case <-time.After(time.Second):
		t.Fatal("next 没有被 Close 唤醒")
	}
	assertPoolReleased(t, pool)
}

func TestSendQueueCloseWakesEmptyWaiter(t *testing.T) {
	t.Parallel()

	// 让消费者先阻塞在空队列，再验证 Close 信号没有丢失。
	queue := newTestSendQueue(t, 1, 16)
	done := make(chan struct{})
	go func() {
		_, _ = testNext(queue)
		close(done)
	}()

	// 给消费者一个调度机会；测试正确性不依赖它一定已经开始等待。
	time.Sleep(time.Millisecond)
	queue.close()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("空队列消费者没有退出")
	}
}

func TestSendQueueByteLimitAndSharedBudget(t *testing.T) {
	t.Parallel()

	pool := bufferpool.NewPool(bufferpool.Options{TrackUsage: true})
	budget, err := bytebudget.New(32)
	if err != nil {
		t.Fatal(err)
	}
	firstQueue := newSendQueue(10, 32, budget)
	secondQueue := newSendQueue(10, 32, budget)
	first := newQueueItem(pool, []byte{1})
	second := newQueueItem(pool, []byte{2})
	third := newQueueItem(pool, []byte{3})
	if first.chargeBytes != 16 || second.chargeBytes != 16 || third.chargeBytes != 16 {
		t.Fatalf("测试依赖的池化容量发生变化：%d,%d,%d", first.chargeBytes, second.chargeBytes, third.chargeBytes)
	}
	if err := testEnqueue(firstQueue, first); err != nil {
		t.Fatal(err)
	}
	if err := testEnqueue(secondQueue, second); err != nil {
		t.Fatal(err)
	}
	if err := testEnqueue(firstQueue, third); !errors.Is(err, errs.ErrTransportOverloaded) {
		t.Fatalf("共享预算耗尽 error=%v", err)
	}
	third.buffer.Release()
	firstQueue.close()
	secondQueue.close()
	if snapshot := budget.Snapshot(); snapshot.Used != 0 || snapshot.HighWatermark != 32 {
		t.Fatalf("budget=%+v", snapshot)
	}
	assertPoolReleased(t, pool)
}

func TestSendQueueHighLowWatermarkHysteresis(t *testing.T) {
	t.Parallel()

	pool := bufferpool.NewPool(bufferpool.Options{TrackUsage: true})
	queue := newTestSendQueue(t, 10, 1024)
	for index := 0; index < 7; index++ {
		changed, writable, err := queue.enqueue(newQueueItem(pool, []byte{byte(index)}))
		if err != nil || changed || !writable {
			t.Fatalf("enqueue %d=(changed=%v writable=%v err=%v)", index, changed, writable, err)
		}
	}
	changed, writable, err := queue.enqueue(newQueueItem(pool, []byte{7}))
	if err != nil || !changed || writable {
		t.Fatalf("cross high=(changed=%v writable=%v err=%v)", changed, writable, err)
	}
	for remaining := 7; remaining >= 6; remaining-- {
		item, ok, changed, writable := queue.next()
		if !ok || changed || writable {
			t.Fatalf("remaining %d=(ok=%v changed=%v writable=%v)", remaining, ok, changed, writable)
		}
		queue.releaseItem(&item)
	}
	item, ok, changed, writable := queue.next()
	if !ok || !changed || !writable {
		t.Fatalf("cross low=(ok=%v changed=%v writable=%v)", ok, changed, writable)
	}
	queue.releaseItem(&item)
	queue.close()
	assertPoolReleased(t, pool)
}

// newQueueItem 使用最终 payload Buffer 构造测试队列项。
func newQueueItem(pool *bufferpool.Pool, payload []byte) sendItem {
	// nil payload 表示有效零长度 Buffer，而不是 nil Buffer 指针。
	buffer := pool.Acquire(len(payload))
	copy(buffer.Bytes(), payload)
	return sendItem{
		buffer:      buffer,
		payloadSize: len(payload),
		chargeBytes: int64(buffer.Capacity()),
	}
}

// newTestSendQueue 为每个测试建立独立总预算，避免并行用例互相影响。
func newTestSendQueue(t testing.TB, maxMessages int, maxBytes int64) *sendQueue {
	t.Helper()
	budget, err := bytebudget.New(maxBytes)
	if err != nil {
		t.Fatalf("bytebudget.New error=%v", err)
	}
	return newSendQueue(maxMessages, maxBytes, budget)
}

// testEnqueue 忽略不属于当前断言目标的水位返回值。
func testEnqueue(queue *sendQueue, item sendItem) error {
	_, _, err := queue.enqueue(item)
	return err
}

// testNext 忽略不属于当前断言目标的水位返回值。
func testNext(queue *sendQueue) (sendItem, bool) {
	item, ok, _, _ := queue.next()
	return item, ok
}

// equalBytes 在不引入额外测试依赖的情况下比较短 payload。
func equalBytes(left, right []byte) bool {
	// 长度不同立即失败；相同长度逐字节比较。
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

// assertPoolReleased 验证测试结束时没有遗留 Buffer 所有权。
func assertPoolReleased(t *testing.T, pool *bufferpool.Pool) {
	t.Helper()

	// TrackUsage 已开启，总数为零即可覆盖零长度、池化和超大对象分支。
	if stats := pool.Stats(); stats.InUseBuffers != 0 {
		t.Fatalf("未释放 Buffer = %d", stats.InUseBuffers)
	}
}
