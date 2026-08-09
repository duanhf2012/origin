package main

import (
	"context"
	"testing"
)

// fakePprofRuntime 让测试直接驱动状态转换，不启动 Listener，也不等待真实 Timer。
type fakePprofRuntime struct {
	adminAddress string
	pprofAddress string
	adminRunning bool
	pprofRunning bool
}

// StartPprof 记录运行中重新打开的地址。
func (runtime *fakePprofRuntime) StartPprof(address string) error {
	runtime.pprofAddress = address
	runtime.pprofRunning = true
	return nil
}

// StopPprof 只关闭 pprof，故意不改变独立的 Admin 状态。
func (runtime *fakePprofRuntime) StopPprof(context.Context) error {
	runtime.pprofRunning = false
	runtime.pprofAddress = ""
	return nil
}

// PprofAddress 返回受控 pprof 状态。
func (runtime *fakePprofRuntime) PprofAddress() (string, bool) {
	return runtime.pprofAddress, runtime.pprofRunning
}

// AdminAddress 返回受控 Admin 状态。
func (runtime *fakePprofRuntime) AdminAddress() (string, bool) {
	return runtime.adminAddress, runtime.adminRunning
}

// TestPprofRuntimeSequence 不使用 sleep，直接验证关闭、重开、查询、再关闭及 Listener 独立性。
func TestPprofRuntimeSequence(t *testing.T) {
	runtime := &fakePprofRuntime{
		adminAddress: "127.0.0.1:6064",
		pprofAddress: "127.0.0.1:6060",
		adminRunning: true,
		pprofRunning: true,
	}
	if err := stopPprof(t.Context(), runtime); err != nil {
		t.Fatalf("first stop error = %v", err)
	}
	if _, running := runtime.PprofAddress(); running {
		t.Fatal("pprof remained running after first stop")
	}
	if address, running := runtime.AdminAddress(); !running || address != "127.0.0.1:6064" {
		t.Fatalf("admin after pprof stop = %q, %t", address, running)
	}

	address, err := restartPprof(runtime, "127.0.0.1:6060")
	if err != nil || address != "127.0.0.1:6060" {
		t.Fatalf("restart result = %q, %v", address, err)
	}
	if err := stopPprof(t.Context(), runtime); err != nil {
		t.Fatalf("second stop error = %v", err)
	}
	if _, running := runtime.PprofAddress(); running {
		t.Fatal("pprof remained running after second stop")
	}
	if _, running := runtime.AdminAddress(); !running {
		t.Fatal("stopping pprof changed independent admin listener")
	}
}
