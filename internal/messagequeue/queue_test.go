package messagequeue

import (
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/duanhf2012/origin/v3/errs"
	"github.com/duanhf2012/origin/v3/internal/bytebudget"
)

func TestQueueLimitsFIFOReleaseAndBudget(t *testing.T) {
	budget, err := bytebudget.New(32)
	if err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	released := make([]int, 0, 3)
	queue, err := New(3, 32, budget, func(value int) {
		mu.Lock()
		released = append(released, value)
		mu.Unlock()
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, value := range []int{1, 2, 3} {
		if _, _, err := queue.Enqueue(value, 8); err != nil {
			t.Fatalf("enqueue %d: %v", value, err)
		}
	}
	if _, _, err := queue.Enqueue(4, 0); !errors.Is(err, errs.ErrTransportOverloaded) {
		t.Fatalf("message limit=%v", err)
	}
	for want := 1; want <= 3; want++ {
		entry, ok, _, _ := queue.Next()
		if !ok || entry.Value != want || entry.ChargeBytes != 8 {
			t.Fatalf("next=(%+v,%v), want=%d", entry, ok, want)
		}
		queue.Release(&entry)
		if entry != (Entry[int]{}) {
			t.Fatalf("Release 未清空 Entry：%+v", entry)
		}
	}
	if snapshot := budget.Snapshot(); snapshot.Used != 0 || snapshot.HighWatermark != 24 {
		t.Fatalf("budget=%+v", snapshot)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(released) != 3 || released[0] != 1 || released[2] != 3 {
		t.Fatalf("released=%v", released)
	}
}

func TestQueueSharedBudgetByteLimitAndCloseRelease(t *testing.T) {
	budget, err := bytebudget.New(16)
	if err != nil {
		t.Fatal(err)
	}
	released := make(chan string, 3)
	first, err := New(4, 16, budget, func(value string) { released <- value })
	if err != nil {
		t.Fatal(err)
	}
	second, err := New(4, 16, budget, func(value string) { released <- value })
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := first.Enqueue("one", 8); err != nil {
		t.Fatal(err)
	}
	if _, _, err := second.Enqueue("two", 8); err != nil {
		t.Fatal(err)
	}
	if _, _, err := first.Enqueue("over", 1); !errors.Is(err, errs.ErrTransportOverloaded) {
		t.Fatalf("shared budget error=%v", err)
	}
	first.Close()
	first.Close()
	second.Close()
	if !first.IsClosed() || first.Snapshot().Bytes != 0 || budget.Snapshot().Used != 0 {
		t.Fatalf("close state first=%+v budget=%+v", first.Snapshot(), budget.Snapshot())
	}
	if _, _, err := first.Enqueue("closed", 0); !errors.Is(err, errs.ErrTransportClosed) {
		t.Fatalf("enqueue closed=%v", err)
	}
	if len(released) != 2 {
		t.Fatalf("released count=%d", len(released))
	}
}

func TestQueueWatermarkHysteresisAndSlow(t *testing.T) {
	budget, err := bytebudget.New(1024)
	if err != nil {
		t.Fatal(err)
	}
	queue, err := New(10, 1024, budget, func(int) {})
	if err != nil {
		t.Fatal(err)
	}
	for index := 0; index < 7; index++ {
		changed, writable, err := queue.Enqueue(index, 1)
		if err != nil || changed || !writable {
			t.Fatalf("enqueue %d=(%v,%v,%v)", index, changed, writable, err)
		}
	}
	changed, writable, err := queue.Enqueue(7, 1)
	if err != nil || !changed || writable || queue.Snapshot().Writable {
		t.Fatalf("high=(%v,%v,%v) snapshot=%+v", changed, writable, err, queue.Snapshot())
	}
	time.Sleep(2 * time.Millisecond)
	if !queue.IsSlow(time.Millisecond) {
		t.Fatal("高水位持续后未识别慢连接")
	}
	for remaining := 7; remaining >= 6; remaining-- {
		entry, ok, changed, _ := queue.Next()
		if !ok || changed {
			t.Fatalf("remaining %d=(%v,%v)", remaining, ok, changed)
		}
		queue.Release(&entry)
	}
	entry, ok, changed, writable := queue.Next()
	if !ok || !changed || !writable || queue.IsSlow(0) {
		t.Fatalf("low=(%v,%v,%v)", ok, changed, writable)
	}
	queue.Release(&entry)
	queue.Close()
}

func TestQueueCloseWakesEmptyConsumerAndInvalidCalls(t *testing.T) {
	if queue, err := New[int](0, 1, nil, nil); err == nil || queue != nil {
		t.Fatalf("invalid New=(%v,%v)", queue, err)
	}
	var nilQueue *Queue[int]
	if _, _, err := nilQueue.Enqueue(1, 1); !errors.Is(err, errs.ErrInvalidArgument) {
		t.Fatalf("nil Enqueue=%v", err)
	}
	if _, ok, _, _ := nilQueue.Next(); ok || !nilQueue.IsClosed() || nilQueue.IsSlow(0) {
		t.Fatal("nil Queue 查询结果异常")
	}
	if snapshot := nilQueue.Snapshot(); !snapshot.Closed {
		t.Fatalf("nil snapshot=%+v", snapshot)
	}
	nilQueue.Close()
	nilQueue.Release(nil)

	budget, err := bytebudget.New(16)
	if err != nil {
		t.Fatal(err)
	}
	queue, err := New(1, 16, budget, func(int) {})
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan bool, 1)
	go func() {
		_, ok, _, _ := queue.Next()
		done <- ok
	}()
	queue.Close()
	select {
	case ok := <-done:
		if ok {
			t.Fatal("关闭空队列仍返回值")
		}
	case <-time.After(time.Second):
		t.Fatal("Close 未唤醒消费者")
	}
}

func TestQueueEnqueueFinalDrainsInFIFOAndRejectsLaterValues(t *testing.T) {
	budget, err := bytebudget.New(16)
	if err != nil {
		t.Fatal(err)
	}
	queue, err := New(3, 16, budget, func(int) {})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err = queue.Enqueue(1, 1); err != nil {
		t.Fatal(err)
	}
	if _, writable, err := queue.EnqueueFinal(2, 1); err != nil || writable {
		t.Fatalf("EnqueueFinal() writable=%v err=%v", writable, err)
	}
	if _, _, err = queue.Enqueue(3, 1); !errors.Is(err, errs.ErrTransportClosed) {
		t.Fatalf("final 后 Enqueue() error=%v", err)
	}
	for _, want := range []int{1, 2} {
		entry, ok, _, _ := queue.Next()
		if !ok || entry.Value != want {
			t.Fatalf("Next() value=%d ok=%v want=%d", entry.Value, ok, want)
		}
		queue.Release(&entry)
	}
	if _, ok, _, _ := queue.Next(); ok {
		t.Fatal("最终值之后队列必须结束")
	}
}

func TestQueueCloseReleasesSealedFinalQueueAfterWriterFailure(t *testing.T) {
	budget, err := bytebudget.New(16)
	if err != nil {
		t.Fatal(err)
	}
	released := 0
	queue, err := New(3, 16, budget, func(int) { released++ })
	if err != nil {
		t.Fatal(err)
	}
	_, _, _ = queue.Enqueue(1, 1)
	_, _, _ = queue.EnqueueFinal(2, 1)
	queue.Close()
	if released != 2 || budget.Snapshot().Used != 0 {
		t.Fatalf("released=%d budget=%+v", released, budget.Snapshot())
	}
}
