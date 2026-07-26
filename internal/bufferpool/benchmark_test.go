package bufferpool

import (
	"runtime"
	"strconv"
	"sync/atomic"
	"testing"
)

// benchmarkEscapeSink 只存在于 Benchmark，用于确保直接分配的底层数组
// 确实逃逸到堆。它不是生产运行时状态，并通过原子写保证基准可并发执行。
var benchmarkEscapeSink atomic.Pointer[byte]

func BenchmarkDirectEscaping(b *testing.B) {
	// 对全部代表性容量建立强制堆逃逸的直接分配基线。
	for _, size := range benchmarkSizes() {
		b.Run(strconv.Itoa(size), func(b *testing.B) {
			// 基准结束后清除全局逃逸指针，避免延长最后一个数组生命周期。
			b.Cleanup(func() {
				benchmarkEscapeSink.Store(nil)
			})
			b.ReportAllocs()
			// 触碰首尾字节，确保数组真实可用而不是只创建切片头。
			for index := 0; index < b.N; index++ {
				data := make([]byte, size)
				data[0] = byte(index)
				data[len(data)-1] = byte(index)
				forceByteSliceEscape(data)
			}
		})
	}
}

func BenchmarkPool(b *testing.B) {
	// 测量关闭统计的单 goroutine 池路径。
	benchmarkPool(b, false, false)
}

func BenchmarkPoolParallel(b *testing.B) {
	// 测量关闭统计的并行池路径。
	benchmarkPool(b, false, true)
}

func BenchmarkTrackedPool(b *testing.B) {
	// 测量开启统计后的单 goroutine 原子操作成本。
	benchmarkPool(b, true, false)
}

func BenchmarkTrackedPoolParallel(b *testing.B) {
	// 测量开启统计后的并行竞争成本。
	benchmarkPool(b, true, true)
}

func benchmarkPool(b *testing.B, trackUsage, parallel bool) {
	b.Helper()

	// 每个容量使用独立 Pool，避免上一个子基准缓存影响结果。
	for _, size := range benchmarkSizes() {
		b.Run(strconv.Itoa(size), func(b *testing.B) {
			pool := NewPool(Options{TrackUsage: trackUsage})
			// 构造开销不计入 Acquire/Release 热路径。
			b.ReportAllocs()
			b.ResetTimer()

			if parallel {
				// RunParallel 由 testing 控制工作 goroutine 数量。
				b.RunParallel(func(parallelBenchmark *testing.PB) {
					for parallelBenchmark.Next() {
						buf := pool.Acquire(size)
						data := buf.Bytes()
						data[0] = 1
						data[len(data)-1] = 1
						runtime.KeepAlive(data)
						buf.Release()
					}
				})
			} else {
				// 串行路径同样读写首尾并及时归还。
				for index := 0; index < b.N; index++ {
					buf := pool.Acquire(size)
					data := buf.Bytes()
					data[0] = byte(index)
					data[len(data)-1] = byte(index)
					runtime.KeepAlive(data)
					buf.Release()
				}
			}

			b.StopTimer()
			if trackUsage {
				// 基准结束后验证所有循环都完成配对释放。
				stats := pool.Stats()
				if stats.InUseBuffers != 0 || stats.InUseCapacityBytes != 0 {
					b.Fatalf("Benchmark 结束后仍有未归还 Buffer：%+v", stats)
				}
			}
		})
	}
}

func benchmarkSizes() []int {
	// 覆盖固定档位、小消息、最大池化边界和超大非池化路径。
	return []int{
		16,
		32,
		64,
		256,
		1024,
		4 * 1024,
		32 * 1024,
		64 * 1024,
		128 * 1024,
	}
}

// forceByteSliceEscape 模拟网络队列把底层字节交给当前调用栈之外的场景，
// 避免直接分配基线被编译器优化为栈上数组。
//
//go:noinline
func forceByteSliceEscape(data []byte) {
	// 保存首字节地址，迫使底层数组跨出当前调用栈。
	benchmarkEscapeSink.Store(&data[0])
}
