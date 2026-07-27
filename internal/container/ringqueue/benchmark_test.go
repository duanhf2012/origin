package ringqueue

import (
	"sync"
	"testing"
)

func BenchmarkRingQueue(b *testing.B) {
	// 使用与 Service Scheduler 相同的外部短锁，避免把“无锁算法”和“带锁 Channel”
	// 直接比较而得出失真的结论。
	queue, err := New[int](64, 20000)
	if err != nil {
		b.Fatalf("New() error = %v", err)
	}
	var mutex sync.Mutex

	b.ReportAllocs()
	b.ResetTimer()
	for index := 0; index < b.N; index++ {
		mutex.Lock()
		if !queue.Enqueue(index) {
			b.Fatal("Enqueue() reached unexpected limit")
		}
		_, ok := queue.Dequeue()
		mutex.Unlock()
		if !ok {
			b.Fatal("Dequeue() failed")
		}
	}
}

func BenchmarkReadyChannelComparison(b *testing.B) {
	// Channel 对照同样保留外部状态锁，因为 Scheduler 无论选择哪种容器都需要在该锁内
	// 原子更新 Accepted、Ready 和生命周期状态。
	ready := make(chan int, 20000)
	var mutex sync.Mutex

	b.ReportAllocs()
	b.ResetTimer()
	for index := 0; index < b.N; index++ {
		mutex.Lock()
		ready <- index
		<-ready
		mutex.Unlock()
	}
}
