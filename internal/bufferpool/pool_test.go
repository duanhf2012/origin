package bufferpool

import (
	"fmt"
	"runtime"
	"runtime/debug"
	"sync"
	"testing"
)

func TestAcquireCapacityBuckets(t *testing.T) {
	t.Parallel()

	// 样本覆盖零长度、各类向上取整、最大档位和超大分配。
	pool := NewPool(Options{})
	tests := []struct {
		name         string
		size         int
		wantCapacity int
	}{
		{name: "zero", size: 0, wantCapacity: 0},
		{name: "minimum", size: 1, wantCapacity: 16},
		{name: "sixteen", size: 16, wantCapacity: 16},
		{name: "seventeen", size: 17, wantCapacity: 32},
		{name: "thirty_two", size: 32, wantCapacity: 32},
		{name: "thirty_three", size: 33, wantCapacity: 64},
		{name: "one_kib", size: 1024, wantCapacity: 1024},
		{name: "one_kib_plus_one", size: 1025, wantCapacity: 2048},
		{name: "maximum_pooled", size: 64 * 1024, wantCapacity: 64 * 1024},
		{name: "oversize", size: 64*1024 + 1, wantCapacity: 64*1024 + 1},
	}

	// 每个样本同时验证调用方可见长度和底层容量。
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			buf := pool.Acquire(test.size)
			data := buf.Bytes()
			if got := len(data); got != test.size {
				t.Fatalf("Bytes 长度=%d，期望=%d", got, test.size)
			}
			if got := cap(data); got != test.wantCapacity {
				t.Fatalf("Bytes 容量=%d，期望=%d", got, test.wantCapacity)
			}
			buf.Release()
		})
	}
}

func TestEveryBucketBoundary(t *testing.T) {
	t.Parallel()

	// 逐档验证最小请求和恰好档位容量都落入同一个桶。
	pool := NewPool(Options{})
	for index := 0; index < bucketCount; index++ {
		capacity := bucketCapacity(index)
		minimumSize := 1
		if index > 0 {
			minimumSize = bucketCapacity(index-1) + 1
		}
		for _, size := range []int{minimumSize, capacity} {
			buf := pool.Acquire(size)
			if got := cap(buf.Bytes()); got != capacity {
				t.Fatalf("档位=%d，请求=%d，容量=%d，期望=%d", index, size, got, capacity)
			}
			buf.Release()
		}
	}
}

func TestAcquireNegativeSizePanics(t *testing.T) {
	t.Parallel()

	// 负长度违反内部不变量，必须立即 panic。
	assertPanics(t, func() {
		NewPool(Options{}).Acquire(-1)
	})
}

func TestNilPoolPanics(t *testing.T) {
	t.Parallel()

	// nil Pool 不能隐式创建全局实例。
	assertPanics(t, func() {
		var pool *Pool
		pool.Acquire(1)
	})
}

func TestReleaseIsNilSafeAndDetectsImmediateReuse(t *testing.T) {
	t.Parallel()

	// nil Buffer 的 Release 允许用于统一 defer 清理。
	var nilBuffer *Buffer
	nilBuffer.Release()

	// 取得并首次释放一个开启统计的档位对象。
	pool := NewPool(Options{TrackUsage: true})
	buf := pool.Acquire(16)
	buf.Release()

	// 同一所有者在对象重新被取得前重复释放，不应重复扣减统计或污染池。
	buf.Release()
	stats := pool.Stats()
	if stats.InUseBuffers != 0 || stats.InUseCapacityBytes != 0 {
		t.Fatalf("重复释放后的统计未归零：%+v", stats)
	}
}

func TestBytesAfterReleasePanics(t *testing.T) {
	t.Parallel()

	// 释放后保留旧指针，用于验证所有权失效检查。
	buf := NewPool(Options{}).Acquire(16)
	buf.Release()
	assertPanics(t, func() {
		buf.Bytes()
	})
}

func TestReleaseDoesNotClearPooledData(t *testing.T) {
	t.Parallel()

	// 写入已知字节后释放，验证默认低延迟路径不额外清零。
	buf := NewPool(Options{}).Acquire(16)
	buf.Bytes()[0] = 0x7f
	buf.Release()

	// 测试位于同包内，可以检查池内对象以验证默认不清零约束；
	// 生产调用方在 Release 后不得再访问该对象。
	if got := buf.data[0]; got != 0x7f {
		t.Fatalf("释放时意外清零数据：got=%x", got)
	}
	if buf.size != 0 {
		t.Fatalf("释放后有效长度=%d，期望=0", buf.size)
	}
}

func TestOversizeAndZeroReleaseDropReferences(t *testing.T) {
	t.Parallel()

	// 同时取得两个不进入固定档位池的特殊对象。
	pool := NewPool(Options{TrackUsage: true})
	zero := pool.Acquire(0)
	oversize := pool.Acquire(64*1024 + 1)

	// 释放后这两类对象应主动断开数组和 Pool 引用。
	zero.Release()
	oversize.Release()

	for name, buf := range map[string]*Buffer{
		"zero":     zero,
		"oversize": oversize,
	} {
		if buf.data != nil {
			t.Errorf("%s Buffer 释放后仍保留数据引用", name)
		}
		if buf.owner != nil {
			t.Errorf("%s Buffer 释放后仍保留 Pool 引用", name)
		}
	}
	// 使用量统计也必须完全配平。
	if stats := pool.Stats(); stats.InUseBuffers != 0 || stats.InUseCapacityBytes != 0 {
		t.Fatalf("释放后统计未归零：%+v", stats)
	}
}

func TestAdoptTransfersSliceWithoutPooling(t *testing.T) {
	t.Parallel()

	// 构造长度小于容量的独占 Slice，验证 Adopt 保留有效长度和真实内存计费。
	pool := NewPool(Options{TrackUsage: true})
	data := make([]byte, 3, 32)
	copy(data, []byte{1, 2, 3})
	buffer := pool.Adopt(data)
	if got := buffer.Bytes(); len(got) != 3 || got[0] != 1 || got[2] != 3 {
		t.Fatalf("Adopt 后数据错误：%v", got)
	}
	if got := buffer.Capacity(); got != 32 {
		t.Fatalf("Adopt Capacity=%d，期望=32", got)
	}
	stats := pool.Stats()
	if stats.AdoptedInUse != 1 || stats.AdoptedBytes != 32 ||
		stats.InUseBuffers != 1 || stats.InUseCapacityBytes != 32 {
		t.Fatalf("Adopt 统计错误：%+v", stats)
	}

	// 释放只断开外部数组和 Pool 引用，不把它放入固定档位。
	buffer.Release()
	if buffer.data != nil || buffer.owner != nil {
		t.Fatal("Adopt Buffer 释放后仍持有外部引用")
	}
	assertTrackingEmpty(t, pool)
}

func TestBufferResizeWithinCapacity(t *testing.T) {
	t.Parallel()

	// 17 字节请求进入 32 字节档位；先缩为零，再扩展并写满新增区域。
	buffer := NewPool(Options{}).Acquire(17)
	if got := buffer.Capacity(); got != 32 {
		t.Fatalf("Capacity=%d，期望=32", got)
	}
	if !buffer.Resize(0) || len(buffer.Bytes()) != 0 {
		t.Fatal("Resize(0) 未清空有效视图")
	}
	if !buffer.Resize(32) {
		t.Fatal("容量内 Resize 被拒绝")
	}
	for index := range buffer.Bytes() {
		buffer.Bytes()[index] = byte(index)
	}
	if buffer.Resize(33) || buffer.Resize(-1) {
		t.Fatal("越界 Resize 应失败")
	}
	if len(buffer.Bytes()) != 32 || buffer.Bytes()[31] != 31 {
		t.Fatal("失败的 Resize 修改了有效视图")
	}
	buffer.Release()
}

func TestTrackingDisabledReturnsZeroSnapshot(t *testing.T) {
	t.Parallel()

	// 默认 Pool 即使存在未释放对象，也不执行原子统计。
	pool := NewPool(Options{})
	buf := pool.Acquire(1024)
	stats := pool.Stats()
	if stats.Enabled {
		t.Fatal("零值 Options 不应开启统计")
	}
	if stats.InUseBuffers != 0 || stats.InUseCapacityBytes != 0 || stats.Buckets != nil {
		t.Fatalf("关闭统计时应返回零值快照：%+v", stats)
	}
	// 最后归还对象，避免测试向 sync.Pool 留下活跃所有权。
	buf.Release()
}

func TestTrackingSnapshot(t *testing.T) {
	t.Parallel()

	// 样本覆盖零长度、同档多个对象、相邻档、最大档和超大对象。
	pool := NewPool(Options{TrackUsage: true})
	buffers := []*Buffer{
		pool.Acquire(0),
		pool.Acquire(1),
		pool.Acquire(16),
		pool.Acquire(17),
		pool.Acquire(64 * 1024),
		pool.Acquire(64*1024 + 1),
	}

	// 先验证总量和特殊类型统计。
	stats := pool.Stats()
	if !stats.Enabled {
		t.Fatal("显式 TrackUsage=true 后统计未开启")
	}
	if got, want := stats.InUseBuffers, int64(len(buffers)); got != want {
		t.Fatalf("InUseBuffers=%d，期望=%d", got, want)
	}
	const wantCapacity = int64(16*2 + 32 + 64*1024 + (64*1024 + 1))
	if stats.InUseCapacityBytes != wantCapacity {
		t.Fatalf("InUseCapacityBytes=%d，期望=%d", stats.InUseCapacityBytes, wantCapacity)
	}
	if stats.ZeroSizeInUse != 1 {
		t.Fatalf("ZeroSizeInUse=%d，期望=1", stats.ZeroSizeInUse)
	}
	if stats.OversizeInUse != 1 || stats.OversizeBytes != 64*1024+1 {
		t.Fatalf("超大 Buffer 统计错误：%+v", stats)
	}
	if got := len(stats.Buckets); got != bucketCount {
		t.Fatalf("Buckets 长度=%d，期望=%d", got, bucketCount)
	}
	// 再抽查最小、第二和最大固定档位的计数。
	if stats.Buckets[0].Capacity != 16 || stats.Buckets[0].InUseBuffers != 2 {
		t.Fatalf("16 B 档位统计错误：%+v", stats.Buckets[0])
	}
	if stats.Buckets[1].Capacity != 32 || stats.Buckets[1].InUseBuffers != 1 {
		t.Fatalf("32 B 档位统计错误：%+v", stats.Buckets[1])
	}
	if stats.Buckets[bucketCount-1].Capacity != 64*1024 ||
		stats.Buckets[bucketCount-1].InUseBuffers != 1 {
		t.Fatalf("64 KiB 档位统计错误：%+v", stats.Buckets[bucketCount-1])
	}

	// 全部归还后使用统一辅助函数验证每个计数器归零。
	for _, buf := range buffers {
		buf.Release()
	}
	assertTrackingEmpty(t, pool)
}

func TestPoolsAreIsolated(t *testing.T) {
	t.Parallel()

	// 建立两个开启统计的独立实例，并分别持有同容量对象。
	first := NewPool(Options{TrackUsage: true})
	second := NewPool(Options{TrackUsage: true})
	firstBuffer := first.Acquire(64)
	secondBuffer := second.Acquire(64)

	if first.Stats().InUseBuffers != 1 || second.Stats().InUseBuffers != 1 {
		t.Fatal("独立 Pool 的统计相互影响")
	}

	// 释放第一个对象不能改变第二个 Pool 的计数。
	firstBuffer.Release()
	if first.Stats().InUseBuffers != 0 || second.Stats().InUseBuffers != 1 {
		t.Fatal("释放第一个 Pool 的 Buffer 影响了第二个 Pool")
	}
	// 最后释放第二个对象并验证配平。
	secondBuffer.Release()
	assertTrackingEmpty(t, second)
}

func TestConcurrentAcquireRelease(t *testing.T) {
	t.Parallel()

	// 固定工作数和迭代数，覆盖竞态检测同时保持测试耗时有界。
	const (
		workerCount = 16
		iterations  = 2000
	)
	// 请求尺寸跨越零长度、多个档位和超大路径。
	sizes := [...]int{0, 1, 16, 17, 64, 255, 1024, 4097, 32 * 1024, 64 * 1024, 64*1024 + 1}
	// 两个 Pool 交错使用，用于发现隐藏全局状态。
	pools := [...]*Pool{
		NewPool(Options{TrackUsage: true}),
		NewPool(Options{TrackUsage: true}),
	}

	// 每个 worker 独立选择 Pool 和尺寸，错误通过有界通道汇总。
	var wait sync.WaitGroup
	errors := make(chan error, workerCount)
	wait.Add(workerCount)
	for worker := 0; worker < workerCount; worker++ {
		go func(worker int) {
			defer wait.Done()
			pool := pools[worker%len(pools)]
			for iteration := 0; iteration < iterations; iteration++ {
				// 取得、写入、验证并在本轮内释放唯一所有权。
				size := sizes[(worker+iteration)%len(sizes)]
				buf := pool.Acquire(size)
				data := buf.Bytes()
				if len(data) > 0 {
					value := byte(worker + iteration)
					data[0] = value
					data[len(data)-1] = value
					if data[0] != value || data[len(data)-1] != value {
						errors <- fmt.Errorf("worker=%d iteration=%d 数据串用", worker, iteration)
						return
					}
				}
				buf.Release()
			}
		}(worker)
	}
	wait.Wait()
	close(errors)

	// goroutine 结束后汇总数据串用错误并检查两个 Pool 统计。
	for err := range errors {
		t.Error(err)
	}
	for _, pool := range pools {
		assertTrackingEmpty(t, pool)
	}
}

func TestBurstMemoryRetention(t *testing.T) {
	// 固定 P 数量，降低 sync.Pool 每 P 缓存差异对结果的影响。
	oldProcs := runtime.GOMAXPROCS(4)
	defer runtime.GOMAXPROCS(oldProcs)

	baseline := heapAllocAfterGC()
	// 开启统计，既检查生命周期也用于 KeepAlive Pool。
	pool := NewPool(Options{TrackUsage: true})

	// 第一阶段制造大量最大档位对象并全部归还。
	const pooledCount = 512
	buffers := make([]*Buffer, pooledCount)
	for index := range buffers {
		buffers[index] = pool.Acquire(64 * 1024)
		buffers[index].Bytes()[0] = byte(index)
	}
	for index := range buffers {
		buffers[index].Release()
		buffers[index] = nil
	}

	// 超大 Buffer 不进入任何 sync.Pool，单独覆盖其释放路径。
	const oversizeCount = 128
	buffers = make([]*Buffer, oversizeCount)
	for index := range buffers {
		buffers[index] = pool.Acquire(128 * 1024)
		buffers[index].Bytes()[0] = byte(index)
	}
	for index := range buffers {
		buffers[index].Release()
		buffers[index] = nil
	}
	buffers = nil

	// 所有逻辑所有权必须已经归还，再触发 GC 观测活跃堆。
	assertTrackingEmpty(t, pool)
	after := heapAllocAfterGC()
	runtime.KeepAlive(pool)

	// Go 分配器会保留部分元数据和空闲 span，因此这里只防止突发内存
	// 持续成为活跃堆对象，不使用跨机器不稳定的精确相等断言。
	const allowedGrowth = uint64(24 * 1024 * 1024)
	if after > baseline+allowedGrowth {
		t.Fatalf("突发释放并 GC 后 HeapAlloc 增长过大：baseline=%d after=%d", baseline, after)
	}
	t.Logf("突发内存基线：baseline=%d B after=%d B delta=%d B", baseline, after, int64(after)-int64(baseline))
}

func assertTrackingEmpty(t *testing.T, pool *Pool) {
	t.Helper()

	// 先检查总量和两个特殊类型计数。
	stats := pool.Stats()
	if !stats.Enabled {
		t.Fatal("测试要求统计已开启")
	}
	if stats.InUseBuffers != 0 ||
		stats.InUseCapacityBytes != 0 ||
		stats.ZeroSizeInUse != 0 ||
		stats.OversizeInUse != 0 ||
		stats.OversizeBytes != 0 ||
		stats.AdoptedInUse != 0 ||
		stats.AdoptedBytes != 0 {
		t.Fatalf("Pool 仍有未归还 Buffer：%+v", stats)
	}
	// 再逐档检查，避免总量计算缺陷掩盖单桶负数或残留。
	for _, bucket := range stats.Buckets {
		if bucket.InUseBuffers != 0 {
			t.Fatalf("档位 %d 仍有 %d 个未归还 Buffer", bucket.Capacity, bucket.InUseBuffers)
		}
	}
}

func TestRetainedCapacity(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		size int
		want int
	}{
		{size: 0, want: 0},
		{size: 1, want: 16},
		{size: 16, want: 16},
		{size: 17, want: 32},
		{size: 64 * 1024, want: 64 * 1024},
		{size: 64*1024 + 1, want: 64*1024 + 1},
	} {
		if got := RetainedCapacity(test.size); got != test.want {
			t.Fatalf("RetainedCapacity(%d)=%d want=%d", test.size, got, test.want)
		}
	}
}

func assertPanics(t *testing.T, function func()) {
	t.Helper()

	// recover 只在 defer 中有效；没有 panic 时立即让当前测试失败。
	defer func() {
		if recover() == nil {
			t.Fatal("期望 panic，但函数正常返回")
		}
	}()
	// 执行调用方提供的违规操作。
	function()
}

func heapAllocAfterGC() uint64 {
	// 两轮 GC 清理 sync.Pool 的当前缓存和 victim 缓存，FreeOSMemory
	// 再尝试把未使用页归还操作系统。
	runtime.GC()
	runtime.GC()
	debug.FreeOSMemory()

	// GC 稳定后读取当前活跃堆字节，而不是累计分配量。
	var memory runtime.MemStats
	runtime.ReadMemStats(&memory)
	return memory.HeapAlloc
}
