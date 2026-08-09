package node

import (
	"context"
	"strconv"
	"testing"
	"time"

	originlog "github.com/duanhf2012/origin/v3/log"
)

// BenchmarkDiagnosticsSummaryDirectory 比较空目录与 4096 个远端实例的 Node Summary 成本。
// 远端快照和全部索引在计时前构建；采集循环只读取已发布的不可变目录统计。
func BenchmarkDiagnosticsSummaryDirectory(b *testing.B) {
	for _, instanceCount := range []int{0, 4096} {
		b.Run("RemoteInstances"+strconv.Itoa(instanceCount), func(b *testing.B) {
			current := newDiagnosticsDirectoryBenchmarkNode(b, instanceCount)
			b.ReportAllocs()
			b.ResetTimer()
			for b.Loop() {
				summary := current.DiagnosticsSummary()
				if summary.Directory.Services != instanceCount {
					b.Fatalf("Directory Services = %d, want %d", summary.Directory.Services, instanceCount)
				}
			}
		})
	}
}

// newDiagnosticsDirectoryBenchmarkNode 在计时外创建一个本地 Service 和指定规模的远端目录。
func newDiagnosticsDirectoryBenchmarkNode(b *testing.B, instanceCount int) *Node {
	b.Helper()
	target := &diagnosticsSummaryService{}
	current, err := New(
		Config{ID: "directory-benchmark", Services: []string{"local"}},
		[]ServiceBinding{{Name: "local", Template: "diagnosticsSummaryService", Service: target}},
		originlog.NewNop(),
		Options{MaxTimersPerNode: 8, TimerLocation: time.UTC},
	)
	if err != nil {
		b.Fatal(err)
	}
	if instanceCount != 0 {
		raw := diagnosticsDirectoryRawSnapshot(1, instanceCount/2, instanceCount-instanceCount/2)
		if _, _, err := current.discovery.directory.ApplySnapshot(raw); err != nil {
			b.Fatal(err)
		}
	}
	b.Cleanup(func() {
		if err := current.Rollback(context.Background()); err != nil {
			b.Errorf("Node.Rollback() error = %v", err)
		}
	})
	return current
}
