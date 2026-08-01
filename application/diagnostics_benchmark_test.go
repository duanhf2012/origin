package application

import (
	"context"
	"strconv"
	"testing"
	"time"

	"github.com/duanhf2012/origin/v3/internal/bufferpool"
	originlog "github.com/duanhf2012/origin/v3/log"
	"github.com/duanhf2012/origin/v3/node"
	"github.com/duanhf2012/origin/v3/service"
)

// BenchmarkDiagnosticsAggregation 记录聚合冷路径随 Node/Service 数量增长的时间和分配。
// 最大场景使用 64×64 个静态 Service，验证快照只按真实输出线性分配且没有后台缓存。
func BenchmarkDiagnosticsAggregation(b *testing.B) {
	cases := []struct {
		name            string
		nodes           int
		servicesPerNode int
	}{
		{name: "Nodes0", nodes: 0, servicesPerNode: 0},
		{name: "Nodes1_Services1", nodes: 1, servicesPerNode: 1},
		{name: "Nodes64_Services1", nodes: 64, servicesPerNode: 1},
		{name: "Nodes1_Services64", nodes: 1, servicesPerNode: 64},
		{name: "Nodes64_Services64", nodes: 64, servicesPerNode: 64},
	}
	for _, current := range cases {
		b.Run(current.name, func(b *testing.B) {
			app := newDiagnosticsBenchmarkApplication(
				b,
				current.nodes,
				current.servicesPerNode,
			)
			b.ReportAllocs()
			b.ResetTimer()
			for b.Loop() {
				snapshot := app.Diagnostics()
				if len(snapshot.Nodes) != current.nodes {
					b.Fatalf("Diagnostics nodes = %d", len(snapshot.Nodes))
				}
			}
		})
	}
}

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
		bindings := make([]node.ServiceBinding, servicesPerNode)
		for serviceIndex := 0; serviceIndex < servicesPerNode; serviceIndex++ {
			name := "Service" + strconv.Itoa(serviceIndex)
			bindings[serviceIndex] = node.ServiceBinding{
				Name:     name,
				Template: "diagnosticsBenchmarkService",
				Service:  &diagnosticsBenchmarkService{},
			}
		}
		current, err := node.New(
			node.Config{
				ID:       "node-" + strconv.Itoa(nodeIndex),
				Services: []string{"configured"},
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
