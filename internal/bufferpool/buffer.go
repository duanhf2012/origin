package bufferpool

// Buffer 表示一个具有唯一所有者的可复用字节缓冲区。
//
// Buffer 可以在 goroutine 或组件之间转移所有权，但不能被多个所有者
// 同时访问。调用 Release 后，原持有者必须立即丢弃该指针。
type Buffer struct {
	// data 始终保留完整档位容量，size 决定当前使用者可见的有效长度。
	data []byte
	// owner 既标识来源 Pool，也让 Release 无需额外接收 Pool 参数。
	owner *Pool
	size  int
	// bucket 表示固定档位、零长度或超大 Buffer。
	bucket uint8
	// active 受单一所有权约束保护，不承担并发状态同步。
	active bool
}

// Bytes 返回当前 Buffer 的可写有效区域。
//
// 对 nil 或已经释放的 Buffer 调用 Bytes 表示框架内部使用错误，因此
// 直接 panic。返回切片只能在本 Buffer 的所有权释放前使用。
func (b *Buffer) Bytes() []byte {
	if b == nil || !b.active {
		panic("bufferpool: 不能访问 nil 或已经释放的 Buffer")
	}
	return b.data[:b.size]
}

// Release 释放 Buffer。
//
// nil 和在对象尚未被重新取得前的重复释放会被忽略。Release 不支持
// 与 Bytes 或另一次 Release 并发执行；正确调用后，旧指针立即失效。
func (b *Buffer) Release() {
	if b == nil || !b.active {
		return
	}

	// 先标记为失效，使同一所有者错误地再次释放时不会重复归还和扣减统计。
	b.active = false
	owner := b.owner
	bucketID := b.bucket
	capacity := cap(b.data)
	owner.releaseUsage(bucketID, capacity)
	b.size = 0

	if int(bucketID) < bucketCount {
		// 档位 Buffer 保留底层字节且不清零，下一次使用者必须覆盖自己的
		// 全部有效区域。放回来源 Pool 后，当前所有者不得再访问 b。
		owner.buckets[int(bucketID)].pool.Put(b)
		return
	}

	// 零长度和超大 Buffer 不进入档位池。清除引用可以避免旧句柄延长
	// Pool 或超大底层数组的生命周期。
	b.data = nil
	b.owner = nil
	b.bucket = 0
}
