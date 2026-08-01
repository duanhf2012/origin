package errs_test

import (
	"context"
	"errors"
	"fmt"
	"runtime"
	"testing"

	"github.com/duanhf2012/origin/v3/errs"
)

func TestStableCodes(t *testing.T) {
	t.Parallel()

	// 表格显式锁定跨模块和跨语言可见的数值，防止插入常量造成漂移。
	tests := []struct {
		name string
		code errs.Code
		want errs.Code
	}{
		{name: "ok", code: errs.CodeOK, want: 0},
		{name: "canceled", code: errs.CodeCanceled, want: 1},
		{name: "deadline exceeded", code: errs.CodeDeadlineExceeded, want: 2},
		{name: "internal", code: errs.CodeInternal, want: 3},
		{name: "invalid argument", code: errs.CodeInvalidArgument, want: 4},
		{name: "invalid config", code: errs.CodeInvalidConfig, want: 5},
		{name: "process already running", code: errs.CodeProcessAlreadyRunning, want: 6},
		{name: "process control failed", code: errs.CodeProcessControlFailed, want: 7},
		{name: "service retired", code: errs.CodeServiceRetired, want: 1001},
		{name: "service stopping", code: errs.CodeServiceStopping, want: 1002},
		{name: "service stopped", code: errs.CodeServiceStopped, want: 1003},
		{name: "service queue full", code: errs.CodeServiceQueueFull, want: 1004},
		{name: "graceful shutdown timeout", code: errs.CodeGracefulShutdownTimeout, want: 1005},
		{name: "service not ready", code: errs.CodeServiceNotReady, want: 1006},
		{name: "service failed", code: errs.CodeServiceFailed, want: 1007},
		{name: "rpc no route", code: errs.CodeRPCNoRoute, want: 2001},
		{name: "rpc invalid route key", code: errs.CodeRPCInvalidRouteKey, want: 2002},
		{name: "rpc route selector failed", code: errs.CodeRPCRouteSelectorFailed, want: 2003},
		{name: "rpc contract mismatch", code: errs.CodeRPCContractMismatch, want: 2004},
		{name: "rpc method not found", code: errs.CodeRPCMethodNotFound, want: 2005},
		{name: "rpc encode failed", code: errs.CodeRPCEncodeFailed, want: 2006},
		{name: "rpc request decode failed", code: errs.CodeRPCRequestDecodeFailed, want: 2007},
		{name: "rpc response decode failed", code: errs.CodeRPCResponseDecodeFailed, want: 2008},
		{name: "rpc execution panic", code: errs.CodeRPCExecutionPanic, want: 2009},
		{
			name: "rpc broadcast partial failed",
			code: errs.CodeRPCBroadcastPartialFailed,
			want: 2010,
		},
		{
			name: "rpc broadcast failed",
			code: errs.CodeRPCBroadcastFailed,
			want: 2011,
		},
		{name: "transport unavailable", code: errs.CodeTransportUnavailable, want: 3001},
		{name: "transport closed", code: errs.CodeTransportClosed, want: 3002},
		{name: "transport overloaded", code: errs.CodeTransportOverloaded, want: 3003},
		{name: "transport protocol", code: errs.CodeTransportProtocol, want: 3004},
		{name: "transport message too large", code: errs.CodeTransportMessageTooLarge, want: 3005},
		{name: "discovery unavailable", code: errs.CodeDiscoveryUnavailable, want: 5001},
		{name: "discovery duplicate node", code: errs.CodeDiscoveryDuplicateNode, want: 5002},
		{name: "discovery capacity", code: errs.CodeDiscoveryCapacity, want: 5003},
		{name: "discovery snapshot invalid", code: errs.CodeDiscoverySnapshotInvalid, want: 5004},
		{name: "log closed", code: errs.CodeLogClosed, want: 7001},
		{name: "log output failed", code: errs.CodeLogOutputFailed, want: 7002},
	}

	// 所有数值在同一测试中集中验证。
	for _, test := range tests {
		if test.code != test.want {
			t.Errorf("%s code = %d, want %d", test.name, test.code, test.want)
		}
	}
}

// TestRPCFixedErrors 验证 M11 新增错误码都复用固定哨兵，避免 RPC 高频失败动态分配。
func TestRPCFixedErrors(t *testing.T) {
	t.Parallel()

	tests := []struct {
		code errs.Code
		want error
	}{
		{errs.CodeRPCNoRoute, errs.ErrRPCNoRoute},
		{errs.CodeRPCInvalidRouteKey, errs.ErrRPCInvalidRouteKey},
		{errs.CodeRPCRouteSelectorFailed, errs.ErrRPCRouteSelectorFailed},
		{errs.CodeRPCContractMismatch, errs.ErrRPCContractMismatch},
		{errs.CodeRPCMethodNotFound, errs.ErrRPCMethodNotFound},
		{errs.CodeRPCEncodeFailed, errs.ErrRPCEncodeFailed},
		{errs.CodeRPCRequestDecodeFailed, errs.ErrRPCRequestDecodeFailed},
		{errs.CodeRPCResponseDecodeFailed, errs.ErrRPCResponseDecodeFailed},
		{errs.CodeRPCExecutionPanic, errs.ErrRPCExecutionPanic},
		{errs.CodeRPCBroadcastPartialFailed, errs.ErrRPCBroadcastPartialFailed},
		{errs.CodeRPCBroadcastFailed, errs.ErrRPCBroadcastFailed},
	}

	// 每个固定码必须返回同一个只读对象，并且 CodeOf 能恢复原始数值。
	for _, test := range tests {
		first := errs.New(test.code)
		second := errs.New(test.code)
		if first != test.want || second != test.want || first != second {
			t.Errorf("New(%d) 没有复用固定哨兵", test.code)
		}
		if got := errs.CodeOf(first); got != test.code {
			t.Errorf("CodeOf(New(%d)) = %d", test.code, got)
		}
	}
}

func TestNew(t *testing.T) {
	t.Parallel()

	// 成功码必须映射为 nil。
	if err := errs.New(errs.CodeOK); err != nil {
		t.Fatalf("New(CodeOK) = %v, want nil", err)
	}

	// 已登记错误码必须复用公共哨兵并提供稳定文本。
	tests := []struct {
		code errs.Code
		want error
		text string
	}{
		{code: errs.CodeCanceled, want: errs.ErrCanceled, text: "operation canceled"},
		{code: errs.CodeDeadlineExceeded, want: errs.ErrDeadlineExceeded, text: "deadline exceeded"},
		{code: errs.CodeInternal, want: errs.ErrInternal, text: "internal error"},
		{code: errs.CodeInvalidArgument, want: errs.ErrInvalidArgument, text: "invalid argument"},
		{code: errs.CodeInvalidConfig, want: errs.ErrInvalidConfig, text: "invalid config"},
		{
			code: errs.CodeProcessAlreadyRunning,
			want: errs.ErrProcessAlreadyRunning,
			text: "process already running",
		},
		{
			code: errs.CodeProcessControlFailed,
			want: errs.ErrProcessControlFailed,
			text: "process control failed",
		},
		{code: errs.CodeServiceRetired, want: errs.ErrServiceRetired, text: "service retired"},
		{code: errs.CodeServiceStopping, want: errs.ErrServiceStopping, text: "service stopping"},
		{code: errs.CodeServiceStopped, want: errs.ErrServiceStopped, text: "service stopped"},
		{
			code: errs.CodeServiceQueueFull,
			want: errs.ErrServiceQueueFull,
			text: "service queue full",
		},
		{
			code: errs.CodeGracefulShutdownTimeout,
			want: errs.ErrGracefulShutdownTimeout,
			text: "graceful shutdown timeout",
		},
		{
			code: errs.CodeServiceNotReady,
			want: errs.ErrServiceNotReady,
			text: "service not ready",
		},
		{code: errs.CodeServiceFailed, want: errs.ErrServiceFailed, text: "service failed"},
		{
			code: errs.CodeRPCBroadcastPartialFailed,
			want: errs.ErrRPCBroadcastPartialFailed,
			text: "rpc broadcast partially failed",
		},
		{
			code: errs.CodeRPCBroadcastFailed,
			want: errs.ErrRPCBroadcastFailed,
			text: "rpc broadcast failed",
		},
		{
			code: errs.CodeTransportUnavailable,
			want: errs.ErrTransportUnavailable,
			text: "transport unavailable",
		},
		{code: errs.CodeTransportClosed, want: errs.ErrTransportClosed, text: "transport closed"},
		{
			code: errs.CodeTransportOverloaded,
			want: errs.ErrTransportOverloaded,
			text: "transport overloaded",
		},
		{
			code: errs.CodeTransportProtocol,
			want: errs.ErrTransportProtocol,
			text: "transport protocol error",
		},
		{
			code: errs.CodeTransportMessageTooLarge,
			want: errs.ErrTransportMessageTooLarge,
			text: "transport message too large",
		},
		{
			code: errs.CodeDiscoveryUnavailable,
			want: errs.ErrDiscoveryUnavailable,
			text: "discovery unavailable",
		},
		{
			code: errs.CodeDiscoveryDuplicateNode,
			want: errs.ErrDiscoveryDuplicateNode,
			text: "discovery duplicate node",
		},
		{
			code: errs.CodeDiscoveryCapacity,
			want: errs.ErrDiscoveryCapacity,
			text: "discovery capacity exceeded",
		},
		{
			code: errs.CodeDiscoverySnapshotInvalid,
			want: errs.ErrDiscoverySnapshotInvalid,
			text: "discovery snapshot invalid",
		},
		{code: errs.CodeLogClosed, want: errs.ErrLogClosed, text: "log runtime closed"},
		{code: errs.CodeLogOutputFailed, want: errs.ErrLogOutputFailed, text: "log output failed"},
	}

	// 同时验证对象身份和外观文本。
	for _, test := range tests {
		got := errs.New(test.code)
		if got != test.want {
			t.Errorf("New(%d) did not reuse its sentinel", test.code)
		}
		if got.Error() != test.text {
			t.Errorf("New(%d).Error() = %q, want %q", test.code, got.Error(), test.text)
		}
	}
}

func TestUnknownCode(t *testing.T) {
	t.Parallel()

	// 使用未登记值验证数值不会丢失且文本包含原始码。
	const code errs.Code = 777
	err := errs.New(code)

	if got := errs.CodeOf(err); got != code {
		t.Fatalf("CodeOf(err) = %d, want %d", got, code)
	}
	if got := err.Error(); got != "error code 777" {
		t.Fatalf("err.Error() = %q, want %q", got, "error code 777")
	}
}

func TestNewMessage(t *testing.T) {
	t.Parallel()

	// 先覆盖成功码和空消息两个退化分支。
	if err := errs.NewMessage(errs.CodeOK, "ignored"); err != nil {
		t.Fatalf("NewMessage(CodeOK, message) = %v, want nil", err)
	}
	if err := errs.NewMessage(errs.CodeInvalidArgument, ""); err != errs.ErrInvalidArgument {
		t.Fatalf("empty message did not reuse ErrInvalidArgument")
	}

	// 非空消息应保持公开文本，同时仍按稳定码匹配。
	err := errs.NewMessage(errs.CodeInvalidArgument, "player ID is empty")
	if got := err.Error(); got != "player ID is empty" {
		t.Fatalf("err.Error() = %q, want %q", got, "player ID is empty")
	}
	if !errs.IsCode(err, errs.CodeInvalidArgument) {
		t.Fatalf("IsCode(err, CodeInvalidArgument) = false")
	}
	if !errors.Is(err, errs.ErrInvalidArgument) {
		t.Fatalf("errors.Is(err, ErrInvalidArgument) = false")
	}
}

func TestWrap(t *testing.T) {
	t.Parallel()

	// 覆盖 CodeOK 透传和 nil cause 复用哨兵。
	cause := errors.New("storage unavailable")
	if err := errs.Wrap(errs.CodeOK, cause); err != cause {
		t.Fatalf("Wrap(CodeOK, cause) did not return cause")
	}
	if err := errs.Wrap(errs.CodeInternal, nil); err != errs.ErrInternal {
		t.Fatalf("Wrap(CodeInternal, nil) did not reuse ErrInternal")
	}

	// 真正包装同时保留稳定码、底层 cause、哨兵匹配和组合文本。
	err := errs.Wrap(errs.CodeInternal, cause)
	if got := errs.CodeOf(err); got != errs.CodeInternal {
		t.Fatalf("CodeOf(err) = %d, want %d", got, errs.CodeInternal)
	}
	if !errors.Is(err, cause) {
		t.Fatalf("errors.Is(err, cause) = false")
	}
	if !errors.Is(err, errs.ErrInternal) {
		t.Fatalf("errors.Is(err, ErrInternal) = false")
	}
	if got := err.Error(); got != "internal error: storage unavailable" {
		t.Fatalf("err.Error() = %q, want %q", got, "internal error: storage unavailable")
	}
}

func TestCodeOf(t *testing.T) {
	t.Parallel()

	// 表格覆盖直接错误、包装链、Context 兼容和普通错误兜底。
	tests := []struct {
		name string
		err  error
		want errs.Code
	}{
		{name: "nil", err: nil, want: errs.CodeOK},
		{name: "origin error", err: errs.ErrInvalidConfig, want: errs.CodeInvalidConfig},
		{
			name: "wrapped origin error",
			err:  fmt.Errorf("load config: %w", errs.ErrInvalidConfig),
			want: errs.CodeInvalidConfig,
		},
		{name: "context canceled", err: context.Canceled, want: errs.CodeCanceled},
		{
			name: "wrapped context deadline",
			err:  fmt.Errorf("request: %w", context.DeadlineExceeded),
			want: errs.CodeDeadlineExceeded,
		},
		{name: "plain error", err: errors.New("plain"), want: errs.CodeInternal},
	}

	// 每个样本同时验证 CodeOf 和 IsCode 的一致性。
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			if got := errs.CodeOf(test.err); got != test.want {
				t.Fatalf("CodeOf(err) = %d, want %d", got, test.want)
			}
			if !errs.IsCode(test.err, test.want) {
				t.Fatalf("IsCode(err, %d) = false", test.want)
			}
		})
	}
}

func TestContextCompatibility(t *testing.T) {
	t.Parallel()

	// Origin 取消和超时哨兵必须分别匹配标准库，且不能交叉匹配。
	if !errors.Is(errs.ErrCanceled, context.Canceled) {
		t.Fatalf("errors.Is(ErrCanceled, context.Canceled) = false")
	}
	if !errors.Is(errs.ErrDeadlineExceeded, context.DeadlineExceeded) {
		t.Fatalf("errors.Is(ErrDeadlineExceeded, context.DeadlineExceeded) = false")
	}
	if errors.Is(errs.ErrCanceled, context.DeadlineExceeded) {
		t.Fatalf("ErrCanceled unexpectedly matches context.DeadlineExceeded")
	}
}

func TestErrorsAsCoder(t *testing.T) {
	t.Parallel()

	// 把动态消息错误放入普通 fmt 包装链。
	err := fmt.Errorf("outer: %w", errs.NewMessage(errs.CodeInvalidConfig, "missing node"))

	// errors.As 应能取得内部 Coder 并读取原始稳定码。
	var coder errs.Coder
	if !errors.As(err, &coder) {
		t.Fatalf("errors.As(err, Coder) = false")
	}
	if got := coder.Code(); got != errs.CodeInvalidConfig {
		t.Fatalf("coder.Code() = %d, want %d", got, errs.CodeInvalidConfig)
	}
}

func TestFixedErrorDoesNotAllocate(t *testing.T) {
	// 重复获取已登记哨兵，测量热路径是否保持零分配。
	var err error
	allocs := testing.AllocsPerRun(1000, func() {
		err = errs.New(errs.CodeInternal)
	})
	runtime.KeepAlive(err)

	// 分配次数必须严格为零。
	if allocs != 0 {
		t.Fatalf("New(CodeInternal) allocations = %f, want 0", allocs)
	}
}

func BenchmarkNewFixed(b *testing.B) {
	// 报告固定错误获取路径的分配和耗时。
	b.ReportAllocs()

	// 保存结果并在循环后 KeepAlive，防止编译器删除调用。
	var err error
	for b.Loop() {
		err = errs.New(errs.CodeInternal)
	}
	runtime.KeepAlive(err)
}

func BenchmarkCodeOfFixed(b *testing.B) {
	// 报告直接 Coder 快路径的分配和耗时。
	b.ReportAllocs()

	// 保存结果并在循环后 KeepAlive，防止编译器删除调用。
	var code errs.Code
	for b.Loop() {
		code = errs.CodeOf(errs.ErrInternal)
	}
	runtime.KeepAlive(code)
}
