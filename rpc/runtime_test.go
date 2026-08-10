package rpc

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/duanhf2012/origin/v3/errs"
	"github.com/duanhf2012/origin/v3/internal/bufferpool"
	originlog "github.com/duanhf2012/origin/v3/log"
	"github.com/duanhf2012/origin/v3/service"
)

type runtimeContextKey string

// TestRPCContextCombinesExecutionControlAndBusinessValues 验证本地 RPC Dispatcher 看到
// 目标执行身份、调用控制 Deadline，以及调用方只读业务值的既定优先级。
func TestRPCContextCombinesExecutionControlAndBusinessValues(t *testing.T) {
	executionKey := runtimeContextKey("execution")
	callerKey := runtimeContextKey("caller")
	execution := context.WithValue(context.Background(), executionKey, "target")
	execution = context.WithValue(execution, callerKey, "target-wins")
	deadline := time.Now().Add(time.Second)
	control, cancel := context.WithDeadline(context.Background(), deadline)
	defer cancel()
	values := context.WithValue(context.Background(), callerKey, "caller")
	values = context.WithValue(values, runtimeContextKey("fallback"), "value")
	combined := &rpcContext{execution: execution, control: control, values: values}

	if got, exists := combined.Deadline(); !exists || !got.Equal(deadline) {
		t.Fatalf("Deadline() = %v, %v", got, exists)
	}
	if got := combined.Value(executionKey); got != "target" {
		t.Fatalf("execution Value = %v", got)
	}
	if got := combined.Value(callerKey); got != "target-wins" {
		t.Fatalf("value precedence = %v", got)
	}
	if got := combined.Value(runtimeContextKey("fallback")); got != "value" {
		t.Fatalf("fallback Value = %v", got)
	}
}

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

	if err := runtime.Close(context.Background()); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
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
		0,
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
		0,
	); !errors.Is(err, errs.ErrInvalidArgument) {
		t.Fatalf("failed dispatchRequest() error = %v", err)
	}
	endpoint.dispatcher = &runtimeTestDispatcher{panicCall: true}
	if _, err := runtime.dispatchRequest(
		context.Background(),
		endpoint,
		1,
		nil,
		0,
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
