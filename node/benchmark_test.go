package node

import (
	"testing"
	"time"

	originlog "github.com/duanhf2012/origin/v3/log"
)

// BenchmarkServiceLookup 建立本地 Service O(1) 查询的 M7 性能基线。
func BenchmarkServiceLookup(b *testing.B) {
	events := make([]string, 0)
	target := &lifecycleService{label: "player", events: &events}
	current, err := New(
		Config{ID: "benchmark-1", Services: []string{"player"}},
		[]ServiceBinding{{
			Name:     "player",
			Template: "lifecycleService",
			Service:  target,
		}},
		originlog.NewNop(),
		Options{
			MaxTimersPerNode: 3_000_000,
			TimerLocation:    time.Local,
		},
	)
	if err != nil {
		b.Fatalf("创建 Benchmark Node: %v", err)
	}

	// 查询只读取构造后不再变化的 Map，不应产生临时对象。
	b.ReportAllocs()
	b.ResetTimer()
	for index := 0; index < b.N; index++ {
		if _, exists := current.Service("player"); !exists {
			b.Fatal("未找到 player Service")
		}
	}
}

// BenchmarkTimerSlotAcquireRelease 测量 Node 共享额度和单调 ID 的原子热路径。
func BenchmarkTimerSlotAcquireRelease(b *testing.B) {
	current := &Node{
		timerResources: nodeTimerResources{
			maxTimers:     3_000_000,
			timerLocation: time.Local,
		},
	}

	b.ReportAllocs()
	b.ResetTimer()
	for index := 0; index < b.N; index++ {
		if _, ok := current.acquireTimerSlot(); !ok {
			b.Fatal("申请 Timer 额度失败")
		}
		current.releaseTimerSlot()
	}
}
