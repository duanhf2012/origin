package timerwheel

import (
	"math"
	"testing"
	"time"
)

func TestChooseLevelBoundaries(t *testing.T) {
	t.Parallel()

	// 每层最后一个可容纳 Tick 和下一层首个 Tick 都必须准确定位。
	tests := []struct {
		name     string
		deadline uint64
		want     int
	}{
		{name: "current", deadline: 100, want: 0},
		{name: "l0 last", deadline: 100 + (1<<8 - 1), want: 0},
		{name: "l1 first", deadline: 100 + 1<<8, want: 1},
		{name: "l1 last", deadline: 100 + (1<<16 - 1), want: 1},
		{name: "l2 first", deadline: 100 + 1<<16, want: 2},
		{name: "l3 first", deadline: 100 + 1<<24, want: 3},
		{name: "l4 first", deadline: 100 + 1<<32, want: 4},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := chooseLevel(test.deadline, 100); got != test.want {
				t.Fatalf("chooseLevel() = %d，期望 %d", got, test.want)
			}
		})
	}
}

func TestBitmapNextSetSlotWraps(t *testing.T) {
	t.Parallel()

	// 位图同时包含当前槽之后和回绕后的槽，必须总是选择最短正距离。
	var words [bitmapWords]uint64
	words[0] |= uint64(1) << 3
	words[3] |= uint64(1) << (250 - 192)
	slot, distance, ok := nextSetSlot(&words, 200)
	if !ok || slot != 250 || distance != 50 {
		t.Fatalf("向后查找 = slot:%d distance:%d ok:%v", slot, distance, ok)
	}
	slot, distance, ok = nextSetSlot(&words, 250)
	if !ok || slot != 3 || distance != 9 {
		t.Fatalf("回绕查找 = slot:%d distance:%d ok:%v", slot, distance, ok)
	}
}

func TestWheelInsertRemoveMaintainsBitmapAndOrder(t *testing.T) {
	t.Parallel()

	// 两个同 Tick 条目尾插同一桶，移除后顺序和位图必须逐步更新。
	var wheel timingWheel
	first := &timerEntry{deadlineTick: 7, state: entryScheduled}
	second := &timerEntry{deadlineTick: 7, state: entryScheduled}
	if level := wheel.insertLocked(first, 0); level != 0 {
		t.Fatalf("first level = %d", level)
	}
	wheel.insertLocked(second, 0)
	bucket := &wheel.buckets[0][7]
	if bucket.head != first || bucket.tail != second || bucket.count != 2 {
		t.Fatal("桶尾插顺序错误")
	}
	if wheel.nonEmpty[0][0]&(uint64(1)<<7) == 0 {
		t.Fatal("非空位图没有设置")
	}

	wheel.removeLocked(first)
	if bucket.head != second || bucket.tail != second || bucket.count != 1 {
		t.Fatal("移除头节点后桶状态错误")
	}
	wheel.removeLocked(second)
	if bucket.head != nil || bucket.tail != nil || bucket.count != 0 {
		t.Fatal("清空桶后仍保留节点")
	}
	if wheel.nonEmpty[0][0]&(uint64(1)<<7) != 0 {
		t.Fatal("空桶位图没有清除")
	}
}

func TestTickConversionsAtMaximumDuration(t *testing.T) {
	t.Parallel()

	// 普通边界验证 floor/ceil，最大 Duration 验证最后不完整 Tick 饱和。
	if floorTick(TickDuration-time.Nanosecond) != 0 ||
		ceilTick(TickDuration-time.Nanosecond) != 1 ||
		floorTick(TickDuration) != 1 ||
		ceilTick(TickDuration) != 1 {
		t.Fatal("Tick floor/ceil 边界错误")
	}
	maximum := time.Duration(math.MaxInt64)
	tick := ceilTick(maximum)
	if durationAtTick(tick) != maximum {
		t.Fatalf("最大 Duration Tick 没有饱和：tick=%d duration=%v", tick, durationAtTick(tick))
	}
}

func TestIDRingGrowthWrapAndClear(t *testing.T) {
	t.Parallel()

	// 先超过初始容量触发增长，再弹出一部分并回绕追加。
	var ring idRing
	for id := DeadlineID(1); id <= 20; id++ {
		ring.Push(id)
	}
	for want := DeadlineID(1); want <= 10; want++ {
		got, ok := ring.Pop()
		if !ok || got != want {
			t.Fatalf("Pop() = %d/%v，期望 %d", got, ok, want)
		}
	}
	for id := DeadlineID(21); id <= 40; id++ {
		ring.Push(id)
	}
	for want := DeadlineID(11); want <= 40; want++ {
		got, ok := ring.Pop()
		if !ok || got != want {
			t.Fatalf("回绕 Pop() = %d/%v，期望 %d", got, ok, want)
		}
	}
	if ring.Len() != 0 || ring.head != 0 {
		t.Fatalf("清空后 Ring 状态 = size:%d head:%d", ring.Len(), ring.head)
	}

	// Clear 返回真实数量，并把全部有效槽位归零。
	for id := DeadlineID(1); id <= 5; id++ {
		ring.Push(id)
	}
	if got := ring.Clear(); got != 5 {
		t.Fatalf("Clear() = %d，期望 5", got)
	}
	for _, id := range ring.items {
		if id != InvalidDeadlineID {
			t.Fatal("Clear 后底层数组仍保留 ID")
		}
	}
}
