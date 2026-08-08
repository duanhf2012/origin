package service

import (
	"context"
	"testing"
	"time"

	"github.com/duanhf2012/origin/v3/diagnostics"
	originlog "github.com/duanhf2012/origin/v3/log"
)

type applicationTestRuntime struct {
	application ApplicationRuntime
}

func (*applicationTestRuntime) NodeID() string                             { return "game-1" }
func (*applicationTestRuntime) ServiceName() string                        { return "PlayerService" }
func (*applicationTestRuntime) State() State                               { return StateInitializing }
func (*applicationTestRuntime) Logger() originlog.Logger                   { return originlog.NewNop() }
func (*applicationTestRuntime) LookupLocalService(string) (IService, bool) { return nil, false }
func (*applicationTestRuntime) AcquireTimerSlot() (TimerID, bool)          { return 1, true }
func (*applicationTestRuntime) ReleaseTimerSlot()                          {}
func (*applicationTestRuntime) TimerLimit() int                            { return 1 }
func (*applicationTestRuntime) TimerLocation() *time.Location              { return time.UTC }
func (*applicationTestRuntime) Failure() error                             { return nil }
func (*applicationTestRuntime) ReportFailure(error)                        {}
func (runtime *applicationTestRuntime) Application() ApplicationRuntime {
	return runtime.application
}

type applicationTestFacade struct{}

func (*applicationTestFacade) Diagnostics() diagnostics.Snapshot {
	return diagnostics.Snapshot{SchemaVersion: 1}
}
func (*applicationTestFacade) StartDiagnosticsServer(string) error         { return nil }
func (*applicationTestFacade) StopDiagnosticsServer(context.Context) error { return nil }
func (*applicationTestFacade) DiagnosticsAddress() (string, bool)          { return "127.0.0.1:6061", true }
func (*applicationTestFacade) StartPprof(string) error                     { return nil }
func (*applicationTestFacade) StopPprof(context.Context) error             { return nil }
func (*applicationTestFacade) PprofAddress() (string, bool)                { return "127.0.0.1:6060", true }

var _ ApplicationRuntime = (*applicationTestFacade)(nil)

// TestApplicationReturnsOnlyBoundFacade 防止零值样本持有全局 Application，真实绑定实例则
// 必须返回 Node 注入的同一个最小接口。
func TestApplicationReturnsOnlyBoundFacade(t *testing.T) {
	var zero Service
	if got := zero.Application(); got != nil {
		t.Fatalf("zero Application() = %T", got)
	}

	target := &Service{}
	facade := &applicationTestFacade{}
	if err := BindRuntime(target, &applicationTestRuntime{application: facade}); err != nil {
		t.Fatalf("BindRuntime() error = %v", err)
	}
	if got := target.Application(); got != facade {
		t.Fatalf("bound Application() = %T, want same facade", got)
	}
}

// TestApplicationWithoutOptionalProviderIsNil 保持既有最小 Runtime 替身兼容，不强迫第三方
// 测试 Runtime 新增 Application 方法。
func TestApplicationWithoutOptionalProviderIsNil(t *testing.T) {
	target := &Service{}
	runtime := &applicationTestRuntime{}
	if err := BindRuntime(target, structWithoutApplication{runtime}); err != nil {
		t.Fatalf("BindRuntime() error = %v", err)
	}
	if got := target.Application(); got != nil {
		t.Fatalf("Application() = %T, want nil", got)
	}
}

// structWithoutApplication 显式只转发原 Runtime，证明可选桥不会扩张 Runtime 接口。
type structWithoutApplication struct{ *applicationTestRuntime }

func (structWithoutApplication) Application() {}
