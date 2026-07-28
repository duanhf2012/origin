package rpc

import (
	"context"
	"errors"
	"testing"

	"github.com/duanhf2012/origin/v3/errs"
	"github.com/duanhf2012/origin/v3/internal/bufferpool"
	originlog "github.com/duanhf2012/origin/v3/log"
	"github.com/duanhf2012/origin/v3/service"
)

var runtimeTestFingerprint = ContractFingerprint{1}

type runtimeTestService struct {
	service.Service
}

type runtimeTestDispatcher struct {
	panicCall bool
	fail      error
}

func (dispatcher *runtimeTestDispatcher) ContractID() ContractID {
	return 1
}

func (dispatcher *runtimeTestDispatcher) Fingerprint() ContractFingerprint {
	return runtimeTestFingerprint
}

func (dispatcher *runtimeTestDispatcher) Dispatch(
	_ context.Context,
	methodID MethodID,
	kind CallKind,
	_ []byte,
	response ResponseWriter,
) (ResponseWriter, error) {
	if dispatcher.panicCall {
		panic("runtime test")
	}
	if dispatcher.fail != nil {
		return response, dispatcher.fail
	}
	if methodID != 1 {
		return response, errs.ErrRPCMethodNotFound
	}
	if kind == CallNotify {
		return response, nil
	}
	target, err := (&response).Allocate(1)
	if err != nil {
		return response, err
	}
	target[0] = 7
	return response, nil
}

func TestRuntimeRegistrationResolutionAndClose(t *testing.T) {
	t.Parallel()
	pool := bufferpool.NewPool(bufferpool.Options{TrackUsage: true})
	if _, err := NewRuntime("", pool, originlog.NewNop()); !errors.Is(
		err,
		errs.ErrInvalidArgument,
	) {
		t.Fatalf("invalid NewRuntime() error = %v", err)
	}
	runtime, err := NewRuntime("game-1", pool, originlog.NewNop())
	if err != nil {
		t.Fatal(err)
	}
	target := &runtimeTestService{}
	dispatcher := &runtimeTestDispatcher{}
	if err := runtime.RegisterService("PlayerService", target, dispatcher); err != nil {
		t.Fatal(err)
	}
	if err := runtime.RegisterService("PlainService", target, nil); err != nil {
		t.Fatal(err)
	}
	if err := runtime.RegisterService("PlayerService", target, dispatcher); !errors.Is(
		err,
		errs.ErrInvalidArgument,
	) {
		t.Fatalf("duplicate RegisterService() error = %v", err)
	}
	if _, err := runtime.resolve(
		ToService("PlayerService"),
		1,
		runtimeTestFingerprint,
	); !errors.Is(err, errs.ErrServiceNotReady) {
		t.Fatalf("pre-Freeze resolve() error = %v", err)
	}
	if err := runtime.Freeze(); err != nil {
		t.Fatal(err)
	}

	if _, err := runtime.resolve(
		ToServiceOnNode("other", "PlayerService"),
		1,
		runtimeTestFingerprint,
	); !errors.Is(err, errs.ErrRPCNoRoute) {
		t.Fatalf("other-node resolve() error = %v", err)
	}
	if _, err := runtime.resolve(
		ToService("Missing"),
		1,
		runtimeTestFingerprint,
	); !errors.Is(err, errs.ErrRPCNoRoute) {
		t.Fatalf("missing resolve() error = %v", err)
	}
	if _, err := runtime.resolve(
		ToService("PlainService"),
		1,
		runtimeTestFingerprint,
	); !errors.Is(err, errs.ErrRPCContractMismatch) {
		t.Fatalf("plain resolve() error = %v", err)
	}
	if _, err := runtime.resolve(
		ToService("PlayerService"),
		2,
		runtimeTestFingerprint,
	); !errors.Is(err, errs.ErrRPCContractMismatch) {
		t.Fatalf("contract resolve() error = %v", err)
	}

	runtime.Close()
	if _, err := runtime.resolve(
		ToService("PlayerService"),
		1,
		runtimeTestFingerprint,
	); !errors.Is(err, errs.ErrServiceStopped) {
		t.Fatalf("closed resolve() error = %v", err)
	}
	if err := runtime.RegisterService("Late", target, nil); !errors.Is(
		err,
		errs.ErrServiceNotReady,
	) {
		t.Fatalf("late RegisterService() error = %v", err)
	}
}

func TestRuntimeDirectDispatcherSuccessFailureAndPanic(t *testing.T) {
	t.Parallel()
	pool := bufferpool.NewPool(bufferpool.Options{TrackUsage: true})
	runtime, err := NewRuntime("game-1", pool, originlog.NewNop())
	if err != nil {
		t.Fatal(err)
	}
	endpoint := serviceEndpoint{
		serviceName: "PlayerService",
		dispatcher:  &runtimeTestDispatcher{},
	}

	response, err := runtime.dispatchRequest(
		context.Background(),
		endpoint,
		1,
		nil,
	)
	if err != nil || response == nil || response.Bytes()[0] != 7 {
		t.Fatalf("dispatchRequest() response=%v error=%v", response, err)
	}
	response.Release()

	endpoint.dispatcher = &runtimeTestDispatcher{fail: errs.ErrInvalidArgument}
	if _, err := runtime.dispatchRequest(
		context.Background(),
		endpoint,
		1,
		nil,
	); !errors.Is(err, errs.ErrInvalidArgument) {
		t.Fatalf("failed dispatchRequest() error = %v", err)
	}
	endpoint.dispatcher = &runtimeTestDispatcher{panicCall: true}
	if _, err := runtime.dispatchRequest(
		context.Background(),
		endpoint,
		1,
		nil,
	); !errors.Is(err, errs.ErrRPCExecutionPanic) {
		t.Fatalf("panic dispatchRequest() error = %v", err)
	}

	// Notify 的普通错误和 panic 都只记录目标侧诊断，调用者不接收完成状态。
	endpoint.dispatcher = &runtimeTestDispatcher{fail: errs.ErrInvalidArgument}
	runtime.dispatchNotify(context.Background(), endpoint, 1, nil)
	endpoint.dispatcher = &runtimeTestDispatcher{panicCall: true}
	runtime.dispatchNotify(context.Background(), endpoint, 1, nil)
	if stats := pool.Stats(); stats.InUseBuffers != 0 {
		t.Fatalf("dispatcher Buffer stats = %+v", stats)
	}
}

func TestZeroClientAndLateLocalCallRelease(t *testing.T) {
	t.Parallel()
	pool := bufferpool.NewPool(bufferpool.Options{TrackUsage: true})
	zero := Client{}

	buffer := pool.Acquire(1)
	if err := zero.Await(
		context.Background(),
		1,
		buffer,
		func([]byte) error { return nil },
	); !errors.Is(err, errs.ErrInvalidArgument) {
		t.Fatalf("zero Await() error = %v", err)
	}
	buffer = pool.Acquire(1)
	if err := zero.Async(
		context.Background(),
		1,
		buffer,
		func(context.Context, []byte, error) {},
	); !errors.Is(err, errs.ErrInvalidArgument) {
		t.Fatalf("zero Async() error = %v", err)
	}
	buffer = pool.Acquire(1)
	if err := zero.Notify(
		context.Background(),
		1,
		buffer,
	); !errors.Is(err, errs.ErrInvalidArgument) {
		t.Fatalf("zero Notify() error = %v", err)
	}

	// 调用方超时放弃后，目标晚到的响应由 localCall 完成路径立即释放。
	call := newAwaitCall()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := call.wait(ctx); !errors.Is(err, errs.ErrCanceled) {
		t.Fatalf("abandoned wait() error = %v", err)
	}
	late := pool.Acquire(16)
	call.complete(late, nil)
	if stats := pool.Stats(); stats.InUseBuffers != 0 {
		t.Fatalf("late response stats = %+v", stats)
	}
}
