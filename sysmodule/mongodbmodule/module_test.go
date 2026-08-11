package mongodbmodule

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/duanhf2012/origin/v3/errs"
	mongooptions "go.mongodb.org/mongo-driver/v2/mongo/options"
)

func TestLifecyclePublishesHandlesAndStopsIdempotently(t *testing.T) {
	t.Parallel()
	runtime := newFakeRuntime()
	module := configuredTestModule(runtime)
	if module.Client() != nil || module.Database() != nil || module.Collection("players") != nil {
		t.Fatal("handles are visible before start")
	}
	if module.Collection("") != nil {
		t.Fatal("empty collection name returned a handle")
	}
	if err := module.OnInit(); err != nil {
		t.Fatal(err)
	}
	if err := module.OnStart(context.Background()); err != nil {
		t.Fatal(err)
	}
	if module.Client() == nil || module.Database() == nil || module.Collection("players") == nil {
		t.Fatal("running module did not publish handles")
	}
	if err := module.Ping(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := module.OnStop(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := module.OnStop(context.Background()); err != nil {
		t.Fatalf("second stop error = %v", err)
	}
	if runtime.disconnectCalls != 1 {
		t.Fatalf("disconnect calls = %d, want 1", runtime.disconnectCalls)
	}
	if module.Client() != nil || module.Database() != nil || module.Collection("players") != nil {
		t.Fatal("handles remain visible after stop")
	}
}

func TestStartFailureRollsBackRuntime(t *testing.T) {
	t.Parallel()
	runtime := newFakeRuntime()
	runtime.pingErr = errFake
	runtime.disconnectErr = errors.New("disconnect failed")
	module := configuredTestModule(runtime)
	err := module.OnStart(context.Background())
	if !errors.Is(err, errFake) || !strings.Contains(err.Error(), "disconnect failed") {
		t.Fatalf("OnStart error = %v", err)
	}
	if runtime.disconnectCalls != 1 || module.Client() != nil {
		t.Fatalf("failed start cleanup calls=%d client=%v", runtime.disconnectCalls, module.Client())
	}
	if err := module.OnStart(context.Background()); !errs.IsCode(err, errs.CodeInvalidArgument) {
		t.Fatalf("restart after failed start error = %v", err)
	}
}

func TestFactoryFailuresAndLifecycleArguments(t *testing.T) {
	t.Parallel()
	config := Config{URI: "mongodb://localhost", Database: "game"}
	failed, err := New(config, withRuntimeFactoryForTest(func(*mongooptions.ClientOptions) (clientRuntime, error) {
		return nil, errFake
	}))
	if err != nil {
		t.Fatal(err)
	}
	if err := failed.OnStart(context.Background()); !errors.Is(err, errFake) {
		t.Fatalf("factory error = %v", err)
	}

	empty, err := New(config, withRuntimeFactoryForTest(func(*mongooptions.ClientOptions) (clientRuntime, error) {
		return nil, nil
	}))
	if err != nil {
		t.Fatal(err)
	}
	if err := empty.OnStart(context.Background()); !errs.IsCode(err, errs.CodeInternal) {
		t.Fatalf("nil runtime error = %v", err)
	}

	var nilModule *Module
	for name, got := range map[string]error{
		"nil init":  nilModule.OnInit(),
		"nil start": nilModule.OnStart(context.Background()),
		"nil stop":  nilModule.OnStop(context.Background()),
		"nil ping":  nilModule.Ping(context.Background()),
	} {
		if !errs.IsCode(got, errs.CodeInvalidArgument) && name != "nil ping" {
			t.Errorf("%s error = %v", name, got)
		}
	}
	module := configuredTestModule(newFakeRuntime())
	if err := module.OnStart(nil); !errs.IsCode(err, errs.CodeInvalidArgument) {
		t.Fatalf("OnStart(nil) error = %v", err)
	}
	if err := module.OnStop(nil); !errs.IsCode(err, errs.CodeInvalidArgument) {
		t.Fatalf("OnStop(nil) error = %v", err)
	}
	if err := module.Ping(nil); !errs.IsCode(err, errs.CodeInvalidArgument) {
		t.Fatalf("Ping(nil) error = %v", err)
	}
}

func TestStopReturnsDriverErrorAndClearsRuntime(t *testing.T) {
	t.Parallel()
	runtime := newFakeRuntime()
	runtime.disconnectErr = errFake
	module := configuredTestModule(runtime)
	if err := module.OnStart(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := module.OnStop(context.Background()); !errors.Is(err, errFake) {
		t.Fatalf("OnStop error = %v", err)
	}
	if module.Client() != nil {
		t.Fatal("runtime remains visible after Disconnect error")
	}
}
