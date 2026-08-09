package main

import (
	"context"
	"fmt"
	"testing"
)

// fakeAdminRuntime 记录启停状态，让测试只验证公开 API 的调用顺序，不创建真实 Listener。
type fakeAdminRuntime struct {
	address string
	running bool
	starts  int
	stops   int
}

func (runtime *fakeAdminRuntime) StartAdminServer(address string) error {
	if runtime.running {
		if runtime.address == address {
			return nil // 同一地址重复 Start 是幂等的。
		}
		return fmt.Errorf("cannot change Admin address while listener is running")
	}
	runtime.address = address
	runtime.running = true
	runtime.starts++
	return nil
}

func (runtime *fakeAdminRuntime) StopAdminServer(context.Context) error {
	runtime.address = ""
	runtime.running = false
	runtime.stops++
	return nil
}

func (runtime *fakeAdminRuntime) AdminAddress() (string, bool) {
	return runtime.address, runtime.running
}

// TestAdminRuntimeSequence 直接驱动“打开、查询、关闭、重开、再次关闭”，不依赖 AfterFunc 或 sleep。
func TestAdminRuntimeSequence(t *testing.T) {
	runtime := &fakeAdminRuntime{}
	if _, running := runtime.AdminAddress(); running {
		t.Fatal("Admin unexpectedly started before StartAdminServer")
	}

	address, err := startAdmin(runtime, "127.0.0.1:6065")
	if err != nil || address != "127.0.0.1:6065" {
		t.Fatalf("first StartAdminServer result = %q, %v", address, err)
	}
	if err := stopAdmin(t.Context(), runtime); err != nil {
		t.Fatalf("first StopAdminServer error = %v", err)
	}
	if address, running := runtime.AdminAddress(); running || address != "" {
		t.Fatalf("AdminAddress after stop = %q, %t", address, running)
	}

	address, err = startAdmin(runtime, "127.0.0.1:6065")
	if err != nil || address != "127.0.0.1:6065" {
		t.Fatalf("second StartAdminServer result = %q, %v", address, err)
	}
	if err := stopAdmin(t.Context(), runtime); err != nil {
		t.Fatalf("second StopAdminServer error = %v", err)
	}
	if runtime.starts != 2 || runtime.stops != 2 {
		t.Fatalf("starts/stops = %d/%d, want 2/2", runtime.starts, runtime.stops)
	}
}
