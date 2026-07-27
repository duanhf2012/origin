package ringqueue

import (
	"errors"
	"runtime"
	"testing"
)

func TestNewRejectsInvalidCapacity(t *testing.T) {
	t.Parallel()

	// 覆盖硬上限非正、初始容量为负和初始容量超过硬上限三个独立校验分支。
	for _, testCase := range []struct {
		name    string
		initial int
		maximum int
	}{
		{name: "zero maximum", initial: 0, maximum: 0},
		{name: "negative initial", initial: -1, maximum: 1},
		{name: "initial exceeds maximum", initial: 2, maximum: 1},
	} {
		testCase := testCase
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			queue, err := New[int](testCase.initial, testCase.maximum)
			if queue != nil {
				t.Fatalf("invalid queue = %#v, want nil", queue)
			}
			if !errors.Is(err, ErrInvalidCapacity) {
				t.Fatalf("error = %v, want ErrInvalidCapacity", err)
			}
		})
	}
}

func TestQueueGrowsWrapsAndPreservesFIFO(t *testing.T) {
	t.Parallel()

	// 从很小的容量开始，先制造一次回绕，再触发扩容，验证两段复制没有改变 FIFO。
	queue, err := New[int](2, 5)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if !queue.Enqueue(1) || !queue.Enqueue(2) {
		t.Fatal("initial enqueue failed")
	}
	if value, ok := queue.Dequeue(); !ok || value != 1 {
		t.Fatalf("first dequeue = (%d, %v), want (1, true)", value, ok)
	}
	for _, value := range []int{3, 4, 5} {
		if !queue.Enqueue(value) {
			t.Fatalf("Enqueue(%d) failed", value)
		}
	}
	if queue.Cap() != 4 {
		t.Fatalf("capacity = %d, want 4", queue.Cap())
	}

	// 最后一次增长被硬上限截断到五，随后必须稳定拒绝第六个元素。
	if !queue.Enqueue(6) {
		t.Fatal("Enqueue(6) failed")
	}
	if queue.Cap() != 5 || queue.MaxCap() != 5 {
		t.Fatalf("capacity = %d/%d, want 5/5", queue.Cap(), queue.MaxCap())
	}
	if queue.Enqueue(7) {
		t.Fatal("Enqueue above hard limit succeeded")
	}

	for _, want := range []int{2, 3, 4, 5, 6} {
		value, ok := queue.Dequeue()
		if !ok || value != want {
			t.Fatalf("Dequeue() = (%d, %v), want (%d, true)", value, ok, want)
		}
	}
	if value, ok := queue.Dequeue(); ok || value != 0 {
		t.Fatalf("empty Dequeue() = (%d, %v), want (0, false)", value, ok)
	}
}

func TestQueueZeroInitialCapacityAndClear(t *testing.T) {
	t.Parallel()

	// 零初始容量应在首次入队时延迟分配，并且 Clear 保留已增长的存储供后续复用。
	queue, err := New[string](0, 3)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if queue.Cap() != 0 || !queue.Enqueue("a") {
		t.Fatalf("initial capacity/enqueue = %d, want 0 and success", queue.Cap())
	}
	if !queue.Enqueue("b") {
		t.Fatal("second enqueue failed")
	}
	capacity := queue.Cap()
	if cleared := queue.Clear(); cleared != 2 {
		t.Fatalf("Clear() = %d, want 2", cleared)
	}
	if queue.Len() != 0 || queue.Cap() != capacity {
		t.Fatalf("after Clear len/cap = %d/%d, want 0/%d", queue.Len(), queue.Cap(), capacity)
	}
	if cleared := queue.Clear(); cleared != 0 {
		t.Fatalf("second Clear() = %d, want 0", cleared)
	}
}

func TestNilQueueIsSafeForQueriesAndRejection(t *testing.T) {
	t.Parallel()

	// nil Queue 只提供安全的只读零值和入队拒绝，避免诊断路径因 nil 再次 panic。
	var queue *Queue[int]
	if queue.Len() != 0 || queue.Cap() != 0 || queue.MaxCap() != 0 {
		t.Fatal("nil queue queries must return zero")
	}
	if queue.Enqueue(1) {
		t.Fatal("nil queue enqueue succeeded")
	}
	if value, ok := queue.Dequeue(); ok || value != 0 {
		t.Fatalf("nil dequeue = (%d, %v), want zero/false", value, ok)
	}
	if queue.Clear() != 0 {
		t.Fatal("nil queue Clear must return zero")
	}
}

func TestQueueReleasesDequeuedAndClearedReferences(t *testing.T) {
	// 两个只由队列持有的大对象分别通过 Dequeue 和 Clear 释放。多次 GC 后若仍未执行
	// Finalizer，说明底层数组残留了已经离队的引用。
	type object struct {
		payload [1024]byte
	}
	released := make(chan int, 2)
	newObject := func(id int) *object {
		value := &object{}
		runtime.SetFinalizer(value, func(*object) {
			released <- id
		})
		return value
	}

	queue, err := New[*object](2, 2)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	first := newObject(1)
	second := newObject(2)
	queue.Enqueue(first)
	queue.Enqueue(second)
	first = nil
	second = nil

	dequeued, ok := queue.Dequeue()
	if !ok {
		t.Fatal("Dequeue() failed")
	}
	// 显式读取一次确认返回值有效，再移除测试栈上的最后一个引用。
	if dequeued == nil {
		t.Fatal("Dequeue() returned nil object")
	}
	dequeued = nil
	queue.Clear()

	// KeepAlive 保证 Queue 本身直到清理完成仍可达；随后重复触发 GC 等待 Finalizer。
	runtime.KeepAlive(queue)
	seen := map[int]bool{}
	for attempt := 0; attempt < 20 && len(seen) < 2; attempt++ {
		runtime.GC()
		runtime.Gosched()
		for {
			select {
			case id := <-released:
				seen[id] = true
			default:
				goto drained
			}
		}
	drained:
	}
	if len(seen) != 2 {
		t.Fatalf("released objects = %v, want both references collected", seen)
	}
}
