package node

import (
	"context"
	"fmt"
	"testing"
	"time"

	internaldiscovery "github.com/duanhf2012/origin/v3/internal/discovery"
	originlog "github.com/duanhf2012/origin/v3/log"
	"github.com/duanhf2012/origin/v3/service"
)

type benchmarkRetirementService struct{ service.Service }

func BenchmarkServiceRetireResumeWithPublicationAck(b *testing.B) {
	current, services := benchmarkRetirementNode(b, 1)
	target := services[0]
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		if err := target.Retire(context.Background()); err != nil {
			b.Fatal(err)
		}
		if err := target.Resume(context.Background()); err != nil {
			b.Fatal(err)
		}
	}
	_ = current
}

func BenchmarkNodeBatchRetireResume(b *testing.B) {
	for _, serviceCount := range []int{100, 1000} {
		b.Run(fmt.Sprintf("services_%d", serviceCount), func(b *testing.B) {
			current, _ := benchmarkRetirementNode(b, serviceCount)
			b.ReportAllocs()
			b.ResetTimer()
			for b.Loop() {
				if err := current.Retire(context.Background()); err != nil {
					b.Fatal(err)
				}
				if err := current.Resume(context.Background()); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

func benchmarkRetirementNode(
	b *testing.B,
	serviceCount int,
) (*Node, []*benchmarkRetirementService) {
	b.Helper()
	services := make([]*benchmarkRetirementService, serviceCount)
	names := make([]string, serviceCount)
	bindings := make([]ServiceBinding, serviceCount)
	for index := range serviceCount {
		name := fmt.Sprintf("Service%04d", index)
		target := &benchmarkRetirementService{}
		services[index] = target
		names[index] = name
		bindings[index] = ServiceBinding{
			Name:     name,
			Template: "benchmarkRetirementService",
			Service:  target,
		}
	}
	current, err := New(
		Config{ID: "retirement-benchmark", Services: names},
		bindings,
		originlog.NewNop(),
		Options{
			MaxTimersPerNode: max(64, serviceCount),
			TimerLocation:    time.UTC,
			DiscoverySource:  internaldiscovery.NewSource(),
		},
	)
	if err != nil {
		b.Fatal(err)
	}
	if err := current.Start(context.Background()); err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() {
		if current.State() == StateReady {
			_ = current.Stop(context.Background())
		}
	})
	return current, services
}
