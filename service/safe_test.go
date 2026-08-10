package service

import (
	"errors"
	"testing"

	"github.com/duanhf2012/origin/v3/errs"
)

func TestGoSafeLifecycleAndPanicBoundary(t *testing.T) {
	var nilService *Service
	if err := nilService.GoSafe(func() {}); !errors.Is(err, errs.ErrInvalidArgument) {
		t.Fatalf("nil Service.GoSafe() error = %v", err)
	}
	target := &Service{}
	if err := target.GoSafe(nil); !errors.Is(err, errs.ErrInvalidArgument) {
		t.Fatalf("GoSafe(nil) error = %v", err)
	}

	for _, state := range []State{StateStarting, StateRunning, StateRetired} {
		runtime := &testRuntime{nodeID: "node-1", name: "SafeService", state: state}
		current := &Service{}
		if err := BindRuntime(current, runtime); err != nil {
			t.Fatal(err)
		}
		done := make(chan struct{})
		if err := current.GoSafe(func() { close(done) }); err != nil {
			t.Fatalf("GoSafe() in %s error = %v", state, err)
		}
		waitSignal(t, done)
	}

	panicRuntime := &testRuntime{nodeID: "node-1", name: "SafeService", state: StateRunning}
	panicTarget := &Service{}
	if err := BindRuntime(panicTarget, panicRuntime); err != nil {
		t.Fatal(err)
	}
	panicDone := make(chan struct{})
	if err := panicTarget.GoSafe(func() {
		defer close(panicDone)
		panic("background job")
	}); err != nil {
		t.Fatalf("GoSafe(panic) error = %v", err)
	}
	waitSignal(t, panicDone)

	for _, test := range []struct {
		state State
		want  error
	}{
		{state: StateCreated, want: errs.ErrServiceNotReady},
		{state: StateStopping, want: errs.ErrServiceStopping},
		{state: StateStopped, want: errs.ErrServiceStopped},
		{state: StateFailed, want: errs.ErrServiceFailed},
	} {
		runtime := &testRuntime{nodeID: "node-1", name: "SafeService", state: test.state}
		current := &Service{}
		if err := BindRuntime(current, runtime); err != nil {
			t.Fatal(err)
		}
		if err := current.GoSafe(func() {}); !errors.Is(err, test.want) {
			t.Fatalf("GoSafe() in %s error = %v, want %v", test.state, err, test.want)
		}
	}
}

func TestRunSafeReturnsNormallyAndConvertsPanic(t *testing.T) {
	var nilService *Service
	if err := nilService.RunSafe(func() {}); !errors.Is(err, errs.ErrInvalidArgument) {
		t.Fatalf("nil Service.RunSafe() error = %v", err)
	}
	target := &Service{}
	if err := target.RunSafe(nil); !errors.Is(err, errs.ErrInvalidArgument) {
		t.Fatalf("RunSafe(nil) error = %v", err)
	}
	called := false
	if err := target.RunSafe(func() { called = true }); err != nil || !called {
		t.Fatalf("RunSafe(success) error = %v, called = %v", err, called)
	}
	if err := target.RunSafe(func() { panic("job") }); !errs.IsCode(err, errs.CodeInternal) {
		t.Fatalf("RunSafe(panic) error = %v", err)
	}
}
