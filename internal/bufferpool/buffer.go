package bufferpool

// Buffer 表示一个具有唯一所有者的可复用字节缓冲区。
//
// Buffer 可以在 goroutine 或组件之间转移所有权，但不能被多个所有者
// 同时访问。调用 Release 后，原持有者必须立即丢弃该指针。
type Buffer struct {
	// data 始终保留完整档位容量，start 和 size 共同决定当前使用者可见的有效区域。
	data []byte
	// owner 既标识来源 Pool，也让 Release 无需额外接收 Pool 参数。
	owner *Pool
	// start 是当前有效区域在完整底层数组中的起点；它允许协议层原地前置或丢弃头部。
	start int
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
	// 访问前验证所有权仍然有效，尽早暴露释放后使用问题。
	if b == nil || !b.active {
		panic("bufferpool: 不能访问 nil 或已经释放的 Buffer")
	}
	// 只暴露当前有效视图，隐藏前置空间和档位向上取整得到的多余容量。
	return b.data[b.start : b.start+b.size]
}

// Capacity 返回当前有效区域从 start 到底层数组末尾可使用的总容量。
//
// 网络容量预算使用该值而不是 len(Bytes())，避免 2 次幂档位和外部 Slice 的保留空间被低估。
func (b *Buffer) Capacity() int {
	// 与 Bytes 保持相同所有权检查，释放后的旧句柄不能读取下一任使用者容量。
	if b == nil || !b.active {
		panic("bufferpool: 不能查询 nil 或已经释放的 Buffer")
	}
	return cap(b.data) - b.start
}

// Resize 在当前有效区域的可用容量内修改可见长度。
//
// Resize 不分配、不移动 start，也不初始化扩展区域；扩展后调用方必须在转移所有权前写满新增字节。
// 越界或负长度返回 false，且 Buffer 保持不变。
func (b *Buffer) Resize(size int) bool {
	// 先验证唯一所有权，再检查完整边界，防止失败时留下部分修改。
	if b == nil || !b.active {
		panic("bufferpool: 不能修改 nil 或已经释放的 Buffer")
	}
	if size < 0 || size > cap(b.data)-b.start {
		return false
	}
	b.size = size
	return true
}

// Headroom 返回当前有效区域之前仍可用于原地前置的字节数。
func (b *Buffer) Headroom() int {
	// 与 Bytes 保持相同的有效性检查，避免释放后的旧句柄读取下一任所有者状态。
	if b == nil || !b.active {
		panic("bufferpool: 不能访问 nil 或已经释放的 Buffer")
	}
	return b.start
}

// Prepend 从当前 headroom 中原地扩展 size 个前缀字节。
//
// 返回的 Slice 只覆盖新增加的前缀，调用方必须在转移 Buffer 所有权前写满它。空间不足或
// size 为负数时返回 false，且 Buffer 视图保持不变。
func (b *Buffer) Prepend(size int) ([]byte, bool) {
	// 先验证 Buffer 所有权，再在修改 start/size 前完整检查边界。
	if b == nil || !b.active {
		panic("bufferpool: 不能修改 nil 或已经释放的 Buffer")
	}
	if size < 0 || size > b.start {
		return nil, false
	}

	// 零长度前置保持幂等；非零时返回恰好覆盖新增区域的可写视图。
	oldStart := b.start
	b.start -= size
	b.size += size
	return b.data[b.start:oldStart], true
}

// DiscardPrefix 从当前有效区域丢弃 size 个前缀字节。
//
// 该操作只移动视图，不复制数据，也不改变 Release 最终归还的完整底层容量。size 越界或
// 为负数时返回 false，且 Buffer 视图保持不变。
func (b *Buffer) DiscardPrefix(size int) bool {
	// 收到远端非法协议头时必须在状态改变前失败，保证错误路径仍能安全 Release。
	if b == nil || !b.active {
		panic("bufferpool: 不能修改 nil 或已经释放的 Buffer")
	}
	if size < 0 || size > b.size {
		return false
	}
	b.start += size
	b.size -= size
	return true
}

// Release 释放 Buffer。
//
// nil 和在对象尚未被重新取得前的重复释放会被忽略。Release 不支持
// 与 Bytes 或另一次 Release 并发执行；正确调用后，旧指针立即失效。
func (b *Buffer) Release() {
	// nil 与尚未重新取得前的重复释放保持幂等，方便 defer 清理。
	if b == nil || !b.active {
		return
	}

	// 先标记为失效，使同一所有者错误地再次释放时不会重复归还和扣减统计。
	b.active = false
	// 在清理字段前保存归还池、档位和容量，供统计及归还逻辑使用。
	owner := b.owner
	bucketID := b.bucket
	capacity := cap(b.data)
	owner.releaseUsage(bucketID, capacity)
	// 有效视图不跨所有者保留，下一次 Acquire 会重新赋值。
	b.start = 0
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
