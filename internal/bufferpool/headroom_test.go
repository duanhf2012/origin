package bufferpool

import "testing"

// TestBufferHeadroomLifecycle 验证前置、丢弃和释放始终操作同一底层 Buffer。
func TestBufferHeadroomLifecycle(t *testing.T) {
	// 开启统计可以同时验证 headroom 计入容量，但最终只归还一次。
	pool := NewPool(Options{TrackUsage: true})
	buffer := pool.AcquireWithHeadroom(4, 8)
	copy(buffer.Bytes(), []byte{1, 2, 3, 4})

	if got := buffer.Headroom(); got != 8 {
		t.Fatalf("初始 headroom=%d，期望 8", got)
	}
	prefix, ok := buffer.Prepend(6)
	if !ok {
		t.Fatal("合法 Prepend 被拒绝")
	}
	copy(prefix, []byte{9, 8, 7, 6, 5, 4})
	if got := buffer.Bytes(); len(got) != 10 ||
		got[0] != 9 || got[5] != 4 || got[6] != 1 || got[9] != 4 {
		t.Fatalf("Prepend 后数据错误: %v", got)
	}

	// 丢弃完整协议前缀后应恢复原业务 payload 视图，且剩余 headroom 包含旧前缀。
	if !buffer.DiscardPrefix(6) {
		t.Fatal("合法 DiscardPrefix 被拒绝")
	}
	if got := buffer.Bytes(); len(got) != 4 ||
		got[0] != 1 || got[3] != 4 {
		t.Fatalf("DiscardPrefix 后 payload 错误: %v", got)
	}
	if got := buffer.Headroom(); got != 8 {
		t.Fatalf("恢复后的 headroom=%d，期望 8", got)
	}

	buffer.Release()
	stats := pool.Stats()
	if stats.InUseBuffers != 0 || stats.InUseCapacityBytes != 0 {
		t.Fatalf("释放后统计未归零: %+v", stats)
	}
}

// TestBufferHeadroomRejectsInvalidViewChanges 验证非法视图修改不会破坏后续数据和释放。
func TestBufferHeadroomRejectsInvalidViewChanges(t *testing.T) {
	pool := NewPool(Options{})
	buffer := pool.AcquireWithHeadroom(3, 2)
	copy(buffer.Bytes(), []byte{1, 2, 3})

	// 两种越界都必须在修改状态前失败。
	if _, ok := buffer.Prepend(3); ok {
		t.Fatal("超过 headroom 的 Prepend 应失败")
	}
	if buffer.DiscardPrefix(4) {
		t.Fatal("超过有效长度的 DiscardPrefix 应失败")
	}
	if got := buffer.Bytes(); len(got) != 3 ||
		got[0] != 1 || got[2] != 3 {
		t.Fatalf("非法操作修改了有效视图: %v", got)
	}
	buffer.Release()
}

// TestBufferZeroPayloadWithHeadroom 验证空业务 payload 仍可原地写入协议头。
func TestBufferZeroPayloadWithHeadroom(t *testing.T) {
	pool := NewPool(Options{})
	buffer := pool.AcquireWithHeadroom(0, 13)
	if len(buffer.Bytes()) != 0 || buffer.Headroom() != 13 {
		t.Fatalf("空 payload 初始视图错误: len=%d headroom=%d", len(buffer.Bytes()), buffer.Headroom())
	}
	header, ok := buffer.Prepend(13)
	if !ok || len(header) != 13 || len(buffer.Bytes()) != 13 {
		t.Fatalf("空 payload 前置失败: ok=%v header=%d total=%d", ok, len(header), len(buffer.Bytes()))
	}
	buffer.Release()
}
