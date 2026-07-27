package timerwheel

import (
	"math"
	"math/bits"
)

const (
	// slotBits 固定每层使用八位 Deadline Tick。
	slotBits = 8
	// bitmapWords 使用四个 64 位字覆盖一层的 256 个槽。
	bitmapWords = SlotsPerLevel / 64
)

// wheelBucket 保存当前槽的侵入式双向链表。
type wheelBucket struct {
	head  *timerEntry
	tail  *timerEntry
	count uint64
}

// timingWheel 保存五层固定桶和用于快速寻找下一非空槽的位图。
type timingWheel struct {
	buckets  [LevelCount][SlotsPerLevel]wheelBucket
	nonEmpty [LevelCount][bitmapWords]uint64
}

// insertLocked 根据当前 Tick 把条目放入最低可容纳层。
func (wheel *timingWheel) insertLocked(entry *timerEntry, currentTick uint64) int {
	// 已过期条目仍放入 L0 当前槽，由当前推进轮次统一裁决，避免调用栈内执行到期逻辑。
	level := chooseLevel(entry.deadlineTick, currentTick)
	shift := uint(level * slotBits)
	slot := int((entry.deadlineTick >> shift) & (SlotsPerLevel - 1))
	bucket := &wheel.buckets[level][slot]

	// 桶内尾插保持相同 Tick 条目的登记和级联顺序。
	entry.level = uint8(level)
	entry.slot = uint8(slot)
	entry.wheelPrev = bucket.tail
	entry.wheelNext = nil
	if bucket.tail == nil {
		bucket.head = entry
	} else {
		bucket.tail.wheelNext = entry
	}
	bucket.tail = entry
	bucket.count++
	wheel.setNonEmpty(level, slot)
	return level
}

// removeLocked 从条目记录的已知桶中 O(1) 移除。
func (wheel *timingWheel) removeLocked(entry *timerEntry) int {
	level := int(entry.level)
	slot := int(entry.slot)
	bucket := &wheel.buckets[level][slot]

	// 分别修复前后节点或桶头尾，再清除条目持有的桶引用。
	if entry.wheelPrev == nil {
		bucket.head = entry.wheelNext
	} else {
		entry.wheelPrev.wheelNext = entry.wheelNext
	}
	if entry.wheelNext == nil {
		bucket.tail = entry.wheelPrev
	} else {
		entry.wheelNext.wheelPrev = entry.wheelPrev
	}
	entry.wheelPrev = nil
	entry.wheelNext = nil
	bucket.count--
	if bucket.count == 0 {
		wheel.clearNonEmpty(level, slot)
	}
	return level
}

// nextEventTickLocked 返回当前 Tick 之后最近的非空 L0 到期点或高层级联边界。
func (wheel *timingWheel) nextEventTickLocked(currentTick uint64) (uint64, bool) {
	var nearest uint64
	found := false
	for level := 0; level < LevelCount; level++ {
		shift := uint(level * slotBits)
		currentUnit := currentTick >> shift
		currentSlot := int(currentUnit & (SlotsPerLevel - 1))
		slot, distance, exists := nextSetSlot(&wheel.nonEmpty[level], currentSlot)
		if !exists {
			continue
		}

		// distance 至少为 1；高层候选必须落在该槽开始的级联边界。
		if currentUnit > math.MaxUint64-distance {
			continue
		}
		candidateUnit := currentUnit + distance
		if shift > 0 && candidateUnit > math.MaxUint64>>shift {
			continue
		}
		candidate := candidateUnit << shift

		// 位图槽号应与计算出的候选槽一致；不一致表示内部定位逻辑损坏。
		if int(candidateUnit&(SlotsPerLevel-1)) != slot {
			panic("timerwheel: 下一非空槽计算错误")
		}
		if !found || candidate < nearest {
			nearest = candidate
			found = true
		}
	}
	return nearest, found
}

// setNonEmpty 把指定槽标记为非空。
func (wheel *timingWheel) setNonEmpty(level, slot int) {
	word := slot >> 6
	bit := uint(slot & 63)
	wheel.nonEmpty[level][word] |= uint64(1) << bit
}

// clearNonEmpty 把已经没有条目的槽从位图移除。
func (wheel *timingWheel) clearNonEmpty(level, slot int) {
	word := slot >> 6
	bit := uint(slot & 63)
	wheel.nonEmpty[level][word] &^= uint64(1) << bit
}

// chooseLevel 根据剩余 Tick 返回最低可容纳层。
func chooseLevel(deadlineTick, currentTick uint64) int {
	if deadlineTick <= currentTick {
		return 0
	}
	delta := deadlineTick - currentTick
	for level := 0; level < LevelCount-1; level++ {
		// 第 level 层能够直接表示小于 2^(8*(level+1)) 的剩余 Tick。
		limitShift := uint((level + 1) * slotBits)
		if delta < uint64(1)<<limitShift {
			return level
		}
	}
	return LevelCount - 1
}

// nextSetSlot 在 currentSlot 之后循环查找第一个非空槽。
//
// 返回的 distance 范围为 1～256；当前槽只有在完整转过一轮后才可能再次被选择。
func nextSetSlot(
	words *[bitmapWords]uint64,
	currentSlot int,
) (slot int, distance uint64, ok bool) {
	// 先查找当前槽之后到 255 的区间。
	if candidate, exists := firstSetFrom(words, currentSlot+1); exists {
		return candidate, uint64(candidate - currentSlot), true
	}
	// 再从零开始查找回绕区间；只有全部位图为空时才没有结果。
	if candidate, exists := firstSetFrom(words, 0); exists {
		return candidate, uint64(SlotsPerLevel - currentSlot + candidate), true
	}
	return 0, 0, false
}

// firstSetFrom 返回 start 及其之后第一个置位槽。
func firstSetFrom(words *[bitmapWords]uint64, start int) (int, bool) {
	if start < 0 {
		start = 0
	}
	if start >= SlotsPerLevel {
		return 0, false
	}

	// 第一个字需要屏蔽 start 之前的低位，后续字可以直接寻找最低置位。
	wordIndex := start >> 6
	word := words[wordIndex] & (math.MaxUint64 << uint(start&63))
	if word != 0 {
		return wordIndex*64 + bits.TrailingZeros64(word), true
	}
	for wordIndex++; wordIndex < bitmapWords; wordIndex++ {
		word = words[wordIndex]
		if word != 0 {
			return wordIndex*64 + bits.TrailingZeros64(word), true
		}
	}
	return 0, false
}
