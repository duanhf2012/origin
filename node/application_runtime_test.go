package node

import (
	"context"
	"testing"
	"time"

	"github.com/duanhf2012/origin/v3/diagnostics"
	"github.com/duanhf2012/origin/v3/service"
)

type nodeApplicationFacade struct{}

func (*nodeApplicationFacade) Diagnostics() diagnostics.Snapshot {
	return diagnostics.Snapshot{SchemaVersion: 1}
}
func (*nodeApplicationFacade) StartDiagnosticsServer(string) error         { return nil }
func (*nodeApplicationFacade) StopDiagnosticsServer(context.Context) error { return nil }
func (*nodeApplicationFacade) DiagnosticsAddress() (string, bool)          { return "", false }
func (*nodeApplicationFacade) StartPprof(string) error                     { return nil }
func (*nodeApplicationFacade) StopPprof(context.Context) error             { return nil }
func (*nodeApplicationFacade) PprofAddress() (string, bool)                { return "", false }

// TestServiceApplicationAvailableFromOnInit 验证 Node 在任何业务生命周期回调前完成外观装配。
func TestServiceApplicationAvailableFromOnInit(t *testing.T) {
	events := make([]string, 0, 4)
	facade := &nodeApplicationFacade{}
	target := &lifecycleService{label: "PlayerService", events: &events}
	target.onInit = func() {
		if got := target.Application(); got != facade {
			t.Fatalf("OnInit Application() = %T", got)
		}
	}
	current := newTestNodeWithOptions(
		t,
		Options{
			MaxTimersPerNode: 64,
			TimerLocation:    time.UTC,
			Application:      facade,
		},
		target,
	)
	if err := current.Start(context.Background()); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	if got := target.Application(); got != facade {
		t.Fatalf("running Application() = %T", got)
	}
}

var _ service.ApplicationRuntime = (*nodeApplicationFacade)(nil)
