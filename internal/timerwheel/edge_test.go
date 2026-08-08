package timerwheel

import (
	"errors"
	"math"
	"testing"
	"time"

	"github.com/duanhf2012/origin/v3/errs"
)

func TestDefaultEngineUsesReusableRuntimeTimer(t *testing.T) {
	// 使用生产默认依赖走通真实 time.Timer 路径，防止可控测试替身掩盖 Stop/Reset/Drain 问题。
	engine, err := New(DefaultOptions())
	if err != nil {
		t.Fatalf("New(DefaultOptions()) error = %v", err)
	}
	if err := engine.Start(); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	queue, err := engine.NewDeadlineQueue()
	if err != nil {
		t.Fatalf("NewDeadlineQueue() error = %v", err)
	}
	id, err := queue.ScheduleAfter(time.Millisecond)
	if err != nil {
		t.Fatalf("ScheduleAfter() error = %v", err)
	}

	// 1ms 会向上量化到 10ms；给操作系统调度留出宽裕上限，但仍校验唯一 ID。
	select {
	case <-queue.ExpiredSignal():
	case <-time.After(2 * time.Second):
		t.Fatal("真实 Timer 等待到期超时")
	}
	got, err := queue.DrainExpired(nil, 1)
	if err != nil {
		t.Fatalf("DrainExpired() error = %v", err)
	}
	if len(got) != 1 || got[0] != id {
		t.Fatalf("真实 Timer 到期 ID = %v，期望 [%d]", got, id)
	}
	if err := engine.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
}

func TestNilReceiversAndClosedState(t *testing.T) {
	// nil 接收者是框架错误路径，必须返回稳定结果而不是产生二次 panic。
	var engine *Engine
	if err := engine.Start(); !errs.IsCode(err, errs.CodeInvalidArgument) {
		t.Fatalf("nil Engine Start() error = %v", err)
	}
	if _, err := engine.NewDeadlineQueue(); !errs.IsCode(err, errs.CodeInvalidArgument) {
		t.Fatalf("nil Engine NewDeadlineQueue() error = %v", err)
	}
	if stats := engine.Stats(); !stats.Closed {
		t.Fatalf("nil Engine Stats() = %+v", stats)
	}
	if err := engine.Close(); err != nil {
		t.Fatalf("nil Engine Close() error = %v", err)
	}

	var queue *DeadlineQueue
	if _, err := queue.ScheduleAfter(time.Second); !errs.IsCode(err, errs.CodeInvalidArgument) {
		t.Fatalf("nil Queue ScheduleAfter() error = %v", err)
	}
	if _, err := queue.RescheduleAfter(1, time.Second); !errs.IsCode(err, errs.CodeInvalidArgument) {
		t.Fatalf("nil Queue RescheduleAfter() error = %v", err)
	}
	if queue.Cancel(1) {
		t.Fatal("nil Queue Cancel() 意外成功")
	}
	if queue.ExpiredSignal() != nil {
		t.Fatal("nil Queue ExpiredSignal() 应返回 nil")
	}
	if _, err := queue.DrainExpired(nil, 1); !errs.IsCode(err, errs.CodeInvalidArgument) {
		t.Fatalf("nil Queue DrainExpired() error = %v", err)
	}
	queue.Close()

	// Engine 关闭优先于 Queue 关闭返回统一 Engine 哨兵，便于上层识别 Node 已停止。
	running, active, _, _ := newFakeTimerEngine(t, false)
	if err := running.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	if _, err := active.ScheduleAfter(time.Second); !errors.Is(err, ErrEngineClosed) {
		t.Fatalf("Engine 关闭后 ScheduleAfter() error = %v", err)
	}
	if _, err := active.RescheduleAfter(1, time.Second); !errors.Is(err, ErrEngineClosed) {
		t.Fatalf("Engine 关闭后 RescheduleAfter() error = %v", err)
	}
}

func TestScheduleRejectsForeignQueueOverflowAndExhaustedID(t *testing.T) {
	first, firstQueue, firstClock, _ := newFakeTimerEngine(t, false)
	_, foreign, _, _ := newFakeTimerEngine(t, false)

	// Queue 的 Engine 归属不能伪造，跨 Node 调用必须在修改任何索引前失败。
	if _, err := first.scheduleAfter(foreign, time.Second); !errs.IsCode(err, errs.CodeInvalidArgument) {
		t.Fatalf("跨 Engine Queue error = %v", err)
	}

	// 单调基准已经接近 Duration 上限时，再叠加延迟必须显式报告内部溢出。
	firstClock.Advance(time.Duration(math.MaxInt64))
	if _, err := firstQueue.ScheduleAfter(time.Nanosecond); !errs.IsCode(err, errs.CodeInternal) {
		t.Fatalf("Deadline Duration 溢出 error = %v", err)
	}
	if _, err := firstQueue.RescheduleAfter(InvalidDeadlineID, time.Second); !errs.IsCode(err, errs.CodeInvalidArgument) {
		t.Fatalf("零 DeadlineID RescheduleAfter() error = %v", err)
	}
	if _, err := firstQueue.RescheduleAfter(1, -time.Nanosecond); !errs.IsCode(err, errs.CodeInvalidArgument) {
		t.Fatalf("负延迟 RescheduleAfter() error = %v", err)
	}
	if _, err := first.rescheduleAfter(foreign, 1, time.Second); !errs.IsCode(err, errs.CodeInvalidArgument) {
		t.Fatalf("跨 Engine Queue RescheduleAfter() error = %v", err)
	}

	// ID 零值代表 uint64 空间耗尽，禁止绕回并复用旧 ID。
	engine, queue, _, _ := newFakeTimerEngine(t, false)
	engine.mu.Lock()
	engine.nextID = InvalidDeadlineID
	engine.mu.Unlock()
	if _, err := queue.ScheduleAfter(time.Second); !errs.IsCode(err, errs.CodeInternal) {
		t.Fatalf("DeadlineID 耗尽 error = %v", err)
	}
}

func TestInternalEntryPoolGuards(t *testing.T) {
	engine, _, _, _ := newFakeTimerEngine(t, false)

	// 池中出现仍带运行状态的条目表示所有权实现被破坏，必须立即 panic 暴露问题。
	engine.entryPool.New = func() any {
		return &timerEntry{state: entryScheduled}
	}
	assertPanic(t, "未清零池条目", func() {
		engine.mu.Lock()
		defer engine.mu.Unlock()
		_ = engine.acquireEntryLocked()
	})

	// nil 或已经释放的条目再次回池同样属于内部双重释放。
	assertPanic(t, "非法回收", func() {
		engine.mu.Lock()
		defer engine.mu.Unlock()
		engine.releaseEntryLocked(nil)
	})
}

func TestTimeConversionDefensiveBranches(t *testing.T) {
	t.Parallel()

	// 负相对时间按 Engine 尚未经过任何 Tick 处理，不能转换成巨大无符号数。
	if floorTick(-time.Second) != 0 || ceilTick(-time.Second) != 0 {
		t.Fatal("负 Duration 没有归零")
	}
	start := time.Unix(100, 0)
	if delay := durationUntilTick(start, start.Add(-time.Second), 1); delay != TickDuration {
		t.Fatalf("墙钟早于基准时 delay = %v，期望 %v", delay, TickDuration)
	}

	// 空 Ring 弹出必须稳定返回无效 ID，不能移动头索引。
	var ring idRing
	if id, ok := ring.Pop(); ok || id != InvalidDeadlineID {
		t.Fatalf("空 Ring Pop() = %d/%v", id, ok)
	}
}

// assertPanic 验证内部不变量保护确实会终止错误路径。
func assertPanic(t *testing.T, name string, callback func()) {
	t.Helper()
	defer func() {
		if recover() == nil {
			t.Fatalf("%s 未触发 panic", name)
		}
	}()
	callback()
}
