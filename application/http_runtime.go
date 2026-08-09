package application

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"sync"

	"github.com/duanhf2012/origin/v3/diagnostics"
	"github.com/duanhf2012/origin/v3/errs"
)

type httpRuntimeState uint8

const (
	httpRuntimeStopped httpRuntimeState = iota
	httpRuntimeServing
	httpRuntimeClosing
	httpRuntimeFailed
)

// httpRuntime 是一个 Application 私有 HTTP Listener 的完整所有者。
//
// operationMu 串行化可能等待网络退出的 Start/Stop；mu 只保护 Serve goroutine 也会更新的
// 短状态。两个锁都属于具体 Application 实例，不形成包级可变状态。
type httpRuntime struct {
	operationMu sync.Mutex
	mu          sync.Mutex
	// requestSlotsOnce/requestSlots 只在使用方显式启用时建立该 Runtime 私有的固定请求额度。
	requestSlotsOnce sync.Once
	requestSlots     chan struct{}
	state            httpRuntimeState
	requested        string
	address          string
	listener         net.Listener
	server           *http.Server
	done             chan struct{}
	errorCode        errs.Code
}

// tryAcquireRequestSlot 无等待获取当前 Runtime 的一个活动请求额度。
func (runtime *httpRuntime) tryAcquireRequestSlot(limit int) bool {
	if runtime == nil || limit <= 0 {
		return false
	}
	runtime.requestSlotsOnce.Do(func() {
		runtime.requestSlots = make(chan struct{}, limit)
	})
	select {
	case runtime.requestSlots <- struct{}{}:
		return true
	default:
		return false
	}
}

// releaseRequestSlot 归还一次成功获取的额度；调用方必须与 tryAcquireRequestSlot 成对使用。
func (runtime *httpRuntime) releaseRequestSlot() {
	<-runtime.requestSlots
}

// httpRuntimeErrors 把同一 Listener 生命周期映射到所属 HTTP 子系统的稳定错误族。
type httpRuntimeErrors struct {
	unavailableCode errs.Code
	stateConflict   error
	// redactAddress 禁止把 Resolve/Listen 的原始地址及系统错误带入返回值和生命周期日志。
	redactAddress bool
}

// pprofHTTPRuntimeErrors 保留 pprof 已发布的 Diagnostics 错误族语义。
func pprofHTTPRuntimeErrors() httpRuntimeErrors {
	return httpRuntimeErrors{
		unavailableCode: errs.CodeDiagnosticsUnavailable,
		stateConflict:   errs.ErrDiagnosticsStateConflict,
	}
}

// start 使用既有 Diagnostics 错误族启动 pprof Listener。
func (runtime *httpRuntime) start(address string, server *http.Server) error {
	return runtime.startWithErrors(address, server, pprofHTTPRuntimeErrors())
}

// startWithErrors 串行启动 Listener，并由调用方指定当前 HTTP 子系统的稳定错误族。
func (runtime *httpRuntime) startWithErrors(
	address string,
	server *http.Server,
	runtimeErrors httpRuntimeErrors,
) error {
	if runtime == nil || address == "" || server == nil || server.Handler == nil {
		return errs.ErrInvalidArgument
	}
	if runtimeErrors.unavailableCode == errs.CodeOK || runtimeErrors.stateConflict == nil {
		return errs.ErrInvalidArgument
	}
	if _, err := net.ResolveTCPAddr("tcp", address); err != nil {
		if runtimeErrors.redactAddress {
			return errs.ErrInvalidArgument
		}
		return errs.Wrap(errs.CodeInvalidArgument, err)
	}

	runtime.operationMu.Lock()
	defer runtime.operationMu.Unlock()
	runtime.mu.Lock()
	if runtime.state == httpRuntimeServing {
		if address == runtime.requested || address == runtime.address {
			runtime.mu.Unlock()
			return nil
		}
		runtime.mu.Unlock()
		return runtimeErrors.stateConflict
	}
	if runtime.state == httpRuntimeClosing {
		runtime.mu.Unlock()
		return runtimeErrors.stateConflict
	}
	runtime.mu.Unlock()

	listener, err := net.Listen("tcp", address)
	if err != nil {
		if runtimeErrors.redactAddress {
			return errs.New(runtimeErrors.unavailableCode)
		}
		return errs.Wrap(
			runtimeErrors.unavailableCode,
			fmt.Errorf("listen %q: %w", address, err),
		)
	}
	actual := listener.Addr().String()
	done := make(chan struct{})
	server.Addr = actual

	runtime.mu.Lock()
	runtime.state = httpRuntimeServing
	runtime.requested = address
	runtime.address = actual
	runtime.listener = listener
	runtime.server = server
	runtime.done = done
	runtime.errorCode = errs.CodeOK
	runtime.mu.Unlock()

	go runtime.serve(server, listener, done, runtimeErrors.unavailableCode)
	return nil
}

func (runtime *httpRuntime) serve(
	server *http.Server,
	listener net.Listener,
	done chan struct{},
	unavailableCode errs.Code,
) {
	serveErr := server.Serve(listener)
	runtime.mu.Lock()
	if runtime.server == server {
		if runtime.state == httpRuntimeServing &&
			!errors.Is(serveErr, http.ErrServerClosed) {
			runtime.state = httpRuntimeFailed
			runtime.errorCode = unavailableCode
			runtime.address = ""
			runtime.requested = ""
			runtime.listener = nil
			runtime.server = nil
		}
		close(done)
	}
	runtime.mu.Unlock()
}

// stop 使用既有 Diagnostics 错误族停止 pprof Listener。
func (runtime *httpRuntime) stop(ctx context.Context) error {
	return runtime.stopWithErrors(ctx, pprofHTTPRuntimeErrors())
}

// stopWithErrors 关闭 Listener、等待 Serve 退出，并按所属子系统映射关闭失败。
func (runtime *httpRuntime) stopWithErrors(
	ctx context.Context,
	runtimeErrors httpRuntimeErrors,
) error {
	if runtime == nil || ctx == nil {
		return errs.ErrInvalidArgument
	}
	if runtimeErrors.unavailableCode == errs.CodeOK || runtimeErrors.stateConflict == nil {
		return errs.ErrInvalidArgument
	}
	runtime.operationMu.Lock()
	defer runtime.operationMu.Unlock()

	runtime.mu.Lock()
	switch runtime.state {
	case httpRuntimeStopped:
		runtime.mu.Unlock()
		return nil
	case httpRuntimeFailed:
		runtime.resetLocked()
		runtime.mu.Unlock()
		return nil
	case httpRuntimeClosing:
		// operationMu 保证正常控制路径不会同时进入 Closing；该分支只作为状态防御。
		runtime.mu.Unlock()
		return runtimeErrors.stateConflict
	}
	runtime.state = httpRuntimeClosing
	server := runtime.server
	done := runtime.done
	runtime.mu.Unlock()

	shutdownErr := server.Shutdown(ctx)
	if shutdownErr != nil {
		// Context 耗尽后仍强制关闭 Listener，保证端口和 Serve goroutine 不泄漏。
		_ = server.Close()
	}
	if done != nil {
		<-done
	}

	runtime.mu.Lock()
	if runtime.server == server {
		runtime.resetLocked()
	}
	runtime.mu.Unlock()
	if shutdownErr == nil {
		return nil
	}
	if cause := context.Cause(ctx); cause != nil {
		return errs.Wrap(errs.CodeOf(cause), cause)
	}
	return errs.Wrap(runtimeErrors.unavailableCode, shutdownErr)
}

func (runtime *httpRuntime) resetLocked() {
	runtime.state = httpRuntimeStopped
	runtime.requested = ""
	runtime.address = ""
	runtime.listener = nil
	runtime.server = nil
	runtime.done = nil
	runtime.errorCode = errs.CodeOK
}

func (runtime *httpRuntime) addressSnapshot() (string, bool) {
	if runtime == nil {
		return "", false
	}
	runtime.mu.Lock()
	defer runtime.mu.Unlock()
	if runtime.state != httpRuntimeServing || runtime.address == "" {
		return "", false
	}
	return runtime.address, true
}

func (runtime *httpRuntime) snapshot() diagnostics.ServerSnapshot {
	if runtime == nil {
		return diagnostics.ServerSnapshot{State: "stopped"}
	}
	runtime.mu.Lock()
	defer runtime.mu.Unlock()
	return diagnostics.ServerSnapshot{
		State:     httpRuntimeStateText(runtime.state),
		Address:   runtime.address,
		ErrorCode: runtime.errorCode,
	}
}

func httpRuntimeStateText(state httpRuntimeState) string {
	switch state {
	case httpRuntimeServing:
		return "serving"
	case httpRuntimeClosing:
		return "closing"
	case httpRuntimeFailed:
		return "failed"
	default:
		return "stopped"
	}
}
