package discovery

import (
	"fmt"
	"testing"
)

// BenchmarkDirectoryFind100 保存一百个可见实例下 RPC 精确路由前置查询的零分配基线。
func BenchmarkDirectoryFind100(b *testing.B) {
	directory, nodeIDs := benchmarkDirectory(b, 100, false)
	b.ReportAllocs()
	b.ResetTimer()
	for index := 0; index < b.N; index++ {
		nodeID := nodeIDs[index%len(nodeIDs)]
		instance, exists := directory.Find(nodeID, "PlayerService")
		if !exists || instance.NodeID != nodeID {
			b.Fatalf("Find(%q) 失败", nodeID)
		}
	}
}

// BenchmarkDirectoryList 保存不同实例规模下按 ServiceName 返回内部只读候选的热路径基线。
func BenchmarkDirectoryList(b *testing.B) {
	for _, size := range []int{100, 1_000, 10_000} {
		b.Run(fmt.Sprintf("instances_%d", size), func(b *testing.B) {
			directory, _ := benchmarkDirectory(b, size, false)
			b.ReportAllocs()
			b.ResetTimer()
			for index := 0; index < b.N; index++ {
				if candidates := directory.List("PlayerService"); len(candidates) != size {
					b.Fatalf("List() len = %d", len(candidates))
				}
			}
		})
	}
}

// BenchmarkDirectoryApply 保存相同快照、单实例状态变化和完整重建三种冷路径成本。
func BenchmarkDirectoryApply(b *testing.B) {
	const size = 1_000
	raw, _ := benchmarkRawSnapshot(size)
	filter, _ := CompileFilter(false, nil)

	b.Run("equivalent", func(b *testing.B) {
		directory, _ := NewDirectory("local-1", filter)
		if _, _, err := directory.ApplySnapshot(raw); err != nil {
			b.Fatal(err)
		}
		b.ReportAllocs()
		b.ResetTimer()
		for index := 0; index < b.N; index++ {
			if _, published, err := directory.ApplySnapshot(raw); err != nil || published {
				b.Fatalf("equivalent ApplySnapshot() = (%v, %v)", published, err)
			}
		}
	})

	b.Run("single_state_change", func(b *testing.B) {
		// 使用两份独立快照，避免 Benchmark 校准轮次结束时修改共享 raw，导致下一轮首个
		// 操作与初始目录相同而被误判为“没有发布”。
		running, _ := benchmarkRawSnapshot(size)
		retired, _ := benchmarkRawSnapshot(size)
		retired.Nodes[0].Services[0].State = ServiceStateRetired
		directory, _ := NewDirectory("local-1", filter)
		if _, _, err := directory.ApplySnapshot(running); err != nil {
			b.Fatal(err)
		}
		b.ReportAllocs()
		b.ResetTimer()
		for index := 0; index < b.N; index++ {
			candidate := retired
			if index%2 != 0 {
				candidate = running
			}
			if _, published, err := directory.ApplySnapshot(candidate); err != nil || !published {
				b.Fatalf("changed ApplySnapshot() = (%v, %v)", published, err)
			}
		}
	})

	b.Run("full_rebuild", func(b *testing.B) {
		directory, _ := NewDirectory("local-1", filter)
		first, _ := benchmarkRawSnapshot(size)
		second, _ := benchmarkRawSnapshot(size)
		for nodeIndex := range second.Nodes {
			second.Nodes[nodeIndex].SessionID += "-next"
		}
		if _, _, err := directory.ApplySnapshot(first); err != nil {
			b.Fatal(err)
		}
		b.ReportAllocs()
		b.ResetTimer()
		for index := 0; index < b.N; index++ {
			candidate := first
			if index%2 == 0 {
				candidate = second
			}
			if _, published, err := directory.ApplySnapshot(candidate); err != nil || !published {
				b.Fatalf("rebuild ApplySnapshot() = (%v, %v)", published, err)
			}
		}
	})
}

// BenchmarkFilterMatch 保存预编译关注规则只执行 Map 精确查询的零分配基线。
func BenchmarkFilterMatch(b *testing.B) {
	services := []string{"PlayerService", "ChatService"}
	labels := map[string][]string{
		"region": {"cn-east", "cn-north"},
		"stage":  {"prod"},
	}
	filter, err := CompileFilter(true, []Rule{{
		Services:   &services,
		NodeLabels: &labels,
	}})
	if err != nil {
		b.Fatal(err)
	}
	node := RawNode{Labels: map[string]string{
		"region": "cn-east",
		"stage":  "prod",
	}}
	target := RawService{ServiceName: "PlayerService"}
	b.ReportAllocs()
	b.ResetTimer()
	for index := 0; index < b.N; index++ {
		if !filter.Match(node, target) {
			b.Fatal("Filter.Match() = false")
		}
	}
}

// benchmarkDirectory 创建给定数量的可见 PlayerService 并返回预生成 NodeID。
func benchmarkDirectory(
	b *testing.B,
	size int,
	retired bool,
) (*Directory, []string) {
	b.Helper()
	raw, nodeIDs := benchmarkRawSnapshot(size)
	if retired {
		for index := range raw.Nodes {
			raw.Nodes[index].Services[0].State = ServiceStateRetired
		}
	}
	filter, err := CompileFilter(false, nil)
	if err != nil {
		b.Fatal(err)
	}
	directory, err := NewDirectory("local-1", filter)
	if err != nil {
		b.Fatal(err)
	}
	if _, _, err := directory.ApplySnapshot(raw); err != nil {
		b.Fatal(err)
	}
	return directory, nodeIDs
}

// benchmarkRawSnapshot 预构建基准数据，避免把字符串格式化计入热查询结果。
func benchmarkRawSnapshot(size int) (RawSnapshot, []string) {
	result := RawSnapshot{Nodes: make([]RawNode, size)}
	nodeIDs := make([]string, size)
	for index := 0; index < size; index++ {
		nodeID := fmt.Sprintf("game-%05d", index)
		nodeIDs[index] = nodeID
		result.Nodes[index] = rawNode(
			nodeID,
			fmt.Sprintf("session-%05d", index),
			fmt.Sprintf("127.0.0.1:%d", 10_000+index),
			"PlayerService",
			1,
		)
	}
	return result, nodeIDs
}
