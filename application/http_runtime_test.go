package application

import (
	"context"
	"errors"
	"net"
	"net/http"
	"testing"
	"time"

	"github.com/duanhf2012/origin/v3/errs"
)

// TestHTTPRuntimeAdminPolicyUnexpectedExit 固定同一个 Runtime 在 Admin 模式下把异常 Serve 退出
// 标记为 CodeAdminUnavailable；既有 Diagnostics 默认模式由原测试继续锁定为 8001。
func TestHTTPRuntimeAdminPolicyUnexpectedExit(t *testing.T) {
	var runtime httpRuntime
	server := &http.Server{Handler: http.NewServeMux()}
	if err := runtime.startWithErrors(
		"127.0.0.1:0",
		server,
		adminHTTPRuntimeErrors(),
	); err != nil {
		t.Fatalf("startWithErrors() error = %v", err)
	}

	// 直接关闭 Listener 模拟 Serve 非预期退出，并等待 Runtime 自己发布终态。
	runtime.mu.Lock()
	listener := runtime.listener
	done := runtime.done
	runtime.mu.Unlock()
	if err := listener.Close(); err != nil {
		t.Fatalf("close listener: %v", err)
	}
	<-done
	snapshot := runtime.snapshot()
	if snapshot.State != "failed" || snapshot.ErrorCode != errs.CodeAdminUnavailable ||
		snapshot.Address != "" {
		t.Fatalf("Admin failed snapshot = %+v", snapshot)
	}
	if err := runtime.stopWithErrors(context.Background(), adminHTTPRuntimeErrors()); err != nil {
		t.Fatalf("stop failed Runtime error = %v", err)
	}
}

// TestHTTPRuntimeAdminForcedStopReleasesResources 防止关闭 Context 已取消时留下 Handler、Serve
// goroutine 或端口；Runtime 必须强制 Close，并仍保留调用方取消错误而非伪造 Admin 错误。
func TestHTTPRuntimeAdminForcedStopReleasesResources(t *testing.T) {
	entered := make(chan struct{})
	exited := make(chan struct{})
	mux := http.NewServeMux()
	mux.HandleFunc("/block", func(_ http.ResponseWriter, request *http.Request) {
		close(entered)
		<-request.Context().Done()
		close(exited)
	})
	var runtime httpRuntime
	if err := runtime.startWithErrors(
		"127.0.0.1:0",
		&http.Server{Handler: mux},
		adminHTTPRuntimeErrors(),
	); err != nil {
		t.Fatalf("startWithErrors() error = %v", err)
	}
	address, ok := runtime.addressSnapshot()
	if !ok {
		t.Fatal("Runtime address was not published")
	}

	requestDone := make(chan struct{})
	go func() {
		defer close(requestDone)
		response, _ := (&http.Client{Timeout: time.Second}).Get("http://" + address + "/block")
		if response != nil {
			_ = response.Body.Close()
		}
	}()
	<-entered
	stopContext, cancel := context.WithCancel(context.Background())
	cancel()
	if err := runtime.stopWithErrors(stopContext, adminHTTPRuntimeErrors()); !errors.Is(
		err,
		context.Canceled,
	) {
		t.Fatalf("forced stop error = %v, want context.Canceled", err)
	}
	<-exited
	<-requestDone
	if _, ok := runtime.addressSnapshot(); ok {
		t.Fatal("Runtime address remains published after forced stop")
	}

	// Stop 返回前必须释放端口，真实重新绑定验证没有遗留 Listener。
	listener, err := net.Listen("tcp", address)
	if err != nil {
		t.Fatalf("released address cannot be rebound: %v", err)
	}
	_ = listener.Close()
}
