package node

import (
	"testing"

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
