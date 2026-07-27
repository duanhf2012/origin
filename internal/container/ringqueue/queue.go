// Package ringqueue 提供不包含并发策略的有界泛型环形队列。
//
// Queue 只负责元素存储、稳定 FIFO、渐进扩容和引用清理。调用方必须根据自己的状态机选择
// 合适的锁；这里不内置锁、Channel、日志或统计，避免公共算法与上层框架语义耦合。
package ringqueue

import (
	"errors"
	"fmt"
)

var (
	// ErrInvalidCapacity 表示构造参数不能形成一个有效的有界队列。
	ErrInvalidCapacity = errors.New("ringqueue: invalid capacity")
)

// Queue 是一个按需增长但永远不会超过 maxCapacity 的 FIFO 环形队列。
//
// Queue 不是并发安全的。零值只能查询，不能入队；正常使用应通过 New 构造。
type Queue[T any] struct {
	items       []T
	head        int
	size        int
	maxCapacity int
}

// New 创建一个初始容量较小、达到当前容量后按需增长的有界队列。
func New[T any](initialCapacity, maxCapacity int) (*Queue[T], error) {
	// 硬上限必须为正，初始容量必须落在零到硬上限之间。允许初始容量为零，便于冷路径延迟
	// 分配；首次入队时会分配一个槽位。
	if maxCapacity <= 0 || initialCapacity < 0 || initialCapacity > maxCapacity {
		return nil, fmt.Errorf(
			"%w: initial=%d max=%d",
			ErrInvalidCapacity,
			initialCapacity,
			maxCapacity,
		)
	}

	// 只分配调用方要求的初始槽位，不按硬上限预留，避免大量低负载 Service 为峰值配置
	// 提前占用内存。
	return &Queue[T]{
		items:       make([]T, initialCapacity),
		maxCapacity: maxCapacity,
	}, nil
}

// Len 返回当前已经保存的元素数量。
func (queue *Queue[T]) Len() int {
	if queue == nil {
		return 0
	}
	return queue.size
}

// Cap 返回当前已经分配的槽位数量，不是队列硬上限。
func (queue *Queue[T]) Cap() int {
	if queue == nil {
		return 0
	}
	return len(queue.items)
}

// MaxCap 返回构造时固定的队列硬上限。
func (queue *Queue[T]) MaxCap() int {
	if queue == nil {
		return 0
	}
	return queue.maxCapacity
}

// Enqueue 把元素追加到队尾；达到硬上限时返回 false，且不修改队列。
func (queue *Queue[T]) Enqueue(value T) bool {
	if queue == nil || queue.maxCapacity <= 0 {
		return false
	}

	// 当前槽位已经用完时先扩容。grow 保留原 FIFO 顺序，并保证不会超过硬上限。
	if queue.size == len(queue.items) && !queue.grow() {
		return false
	}

	// 队尾由 head 与有效长度共同确定；取模使回绕后的空槽继续被复用。
	tail := (queue.head + queue.size) % len(queue.items)
	queue.items[tail] = value
	queue.size++
	return true
}

// Dequeue 移除并返回队首元素；空队列返回 T 的零值和 false。
func (queue *Queue[T]) Dequeue() (T, bool) {
	var zero T
	if queue == nil || queue.size == 0 {
		return zero, false
	}

	// 先复制结果，再立即清零原槽位。该步骤对指针、闭包、Slice 和 Interface 尤其重要，
	// 否则已经出队的大对象仍会被底层数组引用到下一次覆盖。
	value := queue.items[queue.head]
	queue.items[queue.head] = zero
	queue.head++
	if queue.head == len(queue.items) {
		queue.head = 0
	}
	queue.size--

	// 空队列统一把 head 归零，简化后续调试和 Clear 后的状态检查。
	if queue.size == 0 {
		queue.head = 0
	}
	return value, true
}

// Clear 清空全部元素、释放槽位持有的引用，并返回被清除的元素数量。
//
// Clear 保留已经分配的槽位，便于同一个有界队列继续复用。
func (queue *Queue[T]) Clear() int {
	if queue == nil || queue.size == 0 {
		return 0
	}

	// 只遍历有效元素，而不是扫描完整硬上限；回绕状态同样按照 FIFO 下标清零。
	var zero T
	cleared := queue.size
	for offset := 0; offset < queue.size; offset++ {
		index := (queue.head + offset) % len(queue.items)
		queue.items[index] = zero
	}
	queue.head = 0
	queue.size = 0
	return cleared
}

// grow 把当前容量扩展到原来的两倍，并在新数组中恢复从零开始的连续 FIFO 布局。
func (queue *Queue[T]) grow() bool {
	if len(queue.items) >= queue.maxCapacity {
		return false
	}

	// 零容量首次增长到一；其他情况按两倍增长，并在最后一步截断到硬上限。
	newCapacity := len(queue.items) * 2
	if newCapacity == 0 {
		newCapacity = 1
	}
	if newCapacity > queue.maxCapacity {
		newCapacity = queue.maxCapacity
	}

	// 分两段复制可以同时处理连续和回绕布局。复制完成后旧数组不再被 Queue 引用，
	// head 归零，后续入队可直接追加到 size 位置。
	grown := make([]T, newCapacity)
	if queue.size > 0 {
		firstPart := min(queue.size, len(queue.items)-queue.head)
		copy(grown, queue.items[queue.head:queue.head+firstPart])
		copy(grown[firstPart:], queue.items[:queue.size-firstPart])
	}
	queue.items = grown
	queue.head = 0
	return true
}
