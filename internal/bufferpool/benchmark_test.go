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
	for _, size := range benchmarkSizes() {
		b.Run(strconv.Itoa(size), func(b *testing.B) {
			b.Cleanup(func() {
				benchmarkEscapeSink.Store(nil)
			})
			b.ReportAllocs()
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
	benchmarkPool(b, false, false)
}

func BenchmarkPoolParallel(b *testing.B) {
	benchmarkPool(b, false, true)
}

func BenchmarkTrackedPool(b *testing.B) {
	benchmarkPool(b, true, false)
}

func BenchmarkTrackedPoolParallel(b *testing.B) {
	benchmarkPool(b, true, true)
}

func benchmarkPool(b *testing.B, trackUsage, parallel bool) {
	b.Helper()

	for _, size := range benchmarkSizes() {
		b.Run(strconv.Itoa(size), func(b *testing.B) {
			pool := NewPool(Options{TrackUsage: trackUsage})
			b.ReportAllocs()
			b.ResetTimer()

			if parallel {
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
				stats := pool.Stats()
				if stats.InUseBuffers != 0 || stats.InUseCapacityBytes != 0 {
					b.Fatalf("Benchmark 结束后仍有未归还 Buffer：%+v", stats)
				}
			}
		})
	}
}

func benchmarkSizes() []int {
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
	benchmarkEscapeSink.Store(&data[0])
}
