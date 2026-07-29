package node

import (
	"fmt"
	"testing"
	"time"

	internaldiscovery "github.com/duanhf2012/origin/v3/internal/discovery"
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

// BenchmarkBuildDiscoveryActions 保存监听器首次补发和 Node 会话替换的冷路径净变化成本。
func BenchmarkBuildDiscoveryActions(b *testing.B) {
	const instanceCount = 100
	oldState := make(
		map[internaldiscovery.InstanceKey]*internaldiscovery.Instance,
		instanceCount,
	)
	newState := make(
		map[internaldiscovery.InstanceKey]*internaldiscovery.Instance,
		instanceCount,
	)
	for index := 0; index < instanceCount; index++ {
		serviceName := fmt.Sprintf("Service%03d", index)
		key := internaldiscovery.InstanceKey{
			NodeID:      "game-1",
			ServiceName: serviceName,
		}
		oldState[key] = &internaldiscovery.Instance{
			NodeID:      "game-1",
			SessionID:   10,
			ServiceName: serviceName,
			State:       internaldiscovery.ServiceStateRunning,
		}
		newState[key] = &internaldiscovery.Instance{
			NodeID:      "game-1",
			SessionID:   11,
			ServiceName: serviceName,
			State:       internaldiscovery.ServiceStateRunning,
		}
	}

	b.Run("initial_sync", func(b *testing.B) {
		empty := map[internaldiscovery.InstanceKey]*internaldiscovery.Instance{}
		b.ReportAllocs()
		b.ResetTimer()
		for index := 0; index < b.N; index++ {
			if actions := buildDiscoveryActions(empty, newState); len(actions) != 1 {
				b.Fatalf("initial actions = %d", len(actions))
			}
		}
	})
	b.Run("session_replacement", func(b *testing.B) {
		b.ReportAllocs()
		b.ResetTimer()
		for index := 0; index < b.N; index++ {
			if actions := buildDiscoveryActions(oldState, newState); len(actions) != 2 {
				b.Fatalf("replacement actions = %d", len(actions))
			}
		}
	})
}
