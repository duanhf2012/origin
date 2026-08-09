package application

import (
	"context"
	"encoding/json"
	"strconv"
	"testing"
	"time"

	"github.com/duanhf2012/origin/v3/internal/bufferpool"
	originlog "github.com/duanhf2012/origin/v3/log"
	"github.com/duanhf2012/origin/v3/node"
	"github.com/duanhf2012/origin/v3/service"
)

type diagnosticsBenchmarkCase struct {
	name            string
	nodes           int
	servicesPerNode int
}

// diagnosticsBenchmarkCases 返回 0/1/64 Node 与 0/1/64 Service 的完整笛卡尔积。
func diagnosticsBenchmarkCases() []diagnosticsBenchmarkCase {
	counts := [...]int{0, 1, 64}
	cases := make([]diagnosticsBenchmarkCase, 0, len(counts)*len(counts))
	for _, nodes := range counts {
		for _, services := range counts {
			cases = append(cases, diagnosticsBenchmarkCase{
				name:            "Nodes" + strconv.Itoa(nodes) + "_Services" + strconv.Itoa(services),
				nodes:           nodes,
				servicesPerNode: services,
			})
		}
	}
	return cases
}

// BenchmarkDiagnosticsSummary 记录低基数采集随 Node 和 Service 数增长的时间与分配。
func BenchmarkDiagnosticsSummary(b *testing.B) {
	for _, current := range diagnosticsBenchmarkCases() {
		b.Run(current.name, func(b *testing.B) {
			app := newDiagnosticsBenchmarkApplication(b, current.nodes, current.servicesPerNode)
			payload, err := json.Marshal(app.DiagnosticsSummary())
			if err != nil {
				b.Fatal(err)
			}
			b.ReportAllocs()
			b.ResetTimer()
			for b.Loop() {
				summary := app.DiagnosticsSummary()
				if len(summary.Nodes) != current.nodes {
					b.Fatalf("DiagnosticsSummary nodes = %d", len(summary.Nodes))
				}
			}
			b.StopTimer()
			b.ReportMetric(float64(len(payload)), "response-bytes")
		})
	}
}

// BenchmarkDiagnosticsFull 记录兼容 Full v2 采集随逐 Service DTO 增长的时间与分配。
func BenchmarkDiagnosticsFull(b *testing.B) {
	for _, current := range diagnosticsBenchmarkCases() {
		b.Run(current.name, func(b *testing.B) {
			app := newDiagnosticsBenchmarkApplication(b, current.nodes, current.servicesPerNode)
			payload, err := json.Marshal(app.Diagnostics())
			if err != nil {
				b.Fatal(err)
			}
			b.ReportAllocs()
			b.ResetTimer()
			for b.Loop() {
				snapshot := app.Diagnostics()
				if len(snapshot.Nodes) != current.nodes {
					b.Fatalf("Diagnostics nodes = %d", len(snapshot.Nodes))
				}
			}
			b.StopTimer()
			b.ReportMetric(float64(len(payload)), "response-bytes")
		})
	}
}

// BenchmarkDiagnosticsSummaryJSON 记录一次 Summary 真实采集加 JSON 编码的完整请求成本。
func BenchmarkDiagnosticsSummaryJSON(b *testing.B) {
	for _, current := range diagnosticsBenchmarkCases() {
		b.Run(current.name, func(b *testing.B) {
			app := newDiagnosticsBenchmarkApplication(b, current.nodes, current.servicesPerNode)
			var payload []byte
			b.ReportAllocs()
			b.ResetTimer()
			for b.Loop() {
				var err error
				payload, err = json.Marshal(app.DiagnosticsSummary())
				if err != nil {
					b.Fatal(err)
				}
			}
			b.StopTimer()
			b.ReportMetric(float64(len(payload)), "response-bytes")
		})
	}
}

// BenchmarkDiagnosticsFullJSON 记录一次 Full v2 真实采集加 JSON 编码的完整请求成本。
func BenchmarkDiagnosticsFullJSON(b *testing.B) {
	for _, current := range diagnosticsBenchmarkCases() {
		b.Run(current.name, func(b *testing.B) {
			app := newDiagnosticsBenchmarkApplication(b, current.nodes, current.servicesPerNode)
			var payload []byte
			b.ReportAllocs()
			b.ResetTimer()
			for b.Loop() {
				var err error
				payload, err = json.Marshal(app.Diagnostics())
				if err != nil {
					b.Fatal(err)
				}
			}
			b.StopTimer()
			b.ReportMetric(float64(len(payload)), "response-bytes")
		})
	}
}

// newDiagnosticsBenchmarkApplication 在计时循环外构造静态 Node/Service fixture，确保结果
// 只包含采集和编码本身；Cleanup 逆序回收每个 Node 的 TimerEngine。
func newDiagnosticsBenchmarkApplication(
	b *testing.B,
	nodeCount int,
	servicesPerNode int,
) *Application {
	b.Helper()
	pool := bufferpool.NewPool(bufferpool.Options{})
	app := New()
	app.appName = "diagnostics-benchmark"
	app.startedAt = time.Now()
	app.bufferPool = pool
	app.state.Store(uint32(StateRunning))
	app.nodes = make([]*node.Node, 0, nodeCount)
	for nodeIndex := 0; nodeIndex < nodeCount; nodeIndex++ {
		// 生产配置禁止空 Node；矩阵的零 Service 边界使用 Node 已公开的 nil 诊断语义，
		// 仍会真实测量 Node DTO 输出，同时不伪造一个无法由配置构造的运行实例。
		if servicesPerNode == 0 {
			app.nodes = append(app.nodes, nil)
			continue
		}
		bindings := make([]node.ServiceBinding, servicesPerNode)
		configured := make([]string, servicesPerNode)
		for serviceIndex := 0; serviceIndex < servicesPerNode; serviceIndex++ {
			name := "Service" + strconv.Itoa(serviceIndex)
			configured[serviceIndex] = name
			bindings[serviceIndex] = node.ServiceBinding{
				Name:     name,
				Template: "diagnosticsBenchmarkService",
				Service:  &diagnosticsBenchmarkService{},
			}
		}
		current, err := node.New(
			node.Config{
				ID:       "node-" + strconv.Itoa(nodeIndex),
				Services: configured,
			},
			bindings,
			originlog.NewNop(),
			node.Options{
				MaxTimersPerNode: 4096,
				TimerLocation:    time.UTC,
				BufferPool:       pool,
			},
		)
		if err != nil {
			b.Fatalf("node.New() error = %v", err)
		}
		app.nodes = append(app.nodes, current)
	}
	b.Cleanup(func() {
		for index := len(app.nodes) - 1; index >= 0; index-- {
			if app.nodes[index] == nil {
				continue
			}
			if err := app.nodes[index].Rollback(context.Background()); err != nil {
				b.Errorf("Node.Rollback() error = %v", err)
			}
		}
	})
	return app
}

type diagnosticsBenchmarkService struct {
	service.Service
}
