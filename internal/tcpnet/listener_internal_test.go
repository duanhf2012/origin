package tcpnet

import (
	"context"
	"errors"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/duanhf2012/origin/v3/errs"
	"github.com/duanhf2012/origin/v3/internal/bufferpool"
)

func TestAcceptLoopTemporaryErrorCanBeInterrupted(t *testing.T) {
	t.Parallel()

	// 第一次 Accept 返回临时错误，Close 必须立即中断退避而不是等待最大退避结束。
	raw := newScriptedListener(scriptTemporary)
	listener := newTestListener(raw)
	go listener.acceptLoop()
	select {
	case <-raw.firstAccept:
	case <-time.After(testWaitTimeout):
		t.Fatal("AcceptLoop 没有取得临时错误")
	}

	started := time.Now()
	ctx, cancel := context.WithTimeout(context.Background(), testWaitTimeout)
	defer cancel()
	if err := listener.Close(ctx); err != nil {
		t.Fatalf("临时错误后的 Close 失败：%v", err)
	}
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("Close 没有及时中断 Accept 退避：%s", elapsed)
	}
}

func TestAcceptLoopPermanentErrorBecomesListenerCause(t *testing.T) {
	t.Parallel()

	// 永久 Accept 错误应关闭 Listener，并作为后续 Close 的稳定原因保存。
	raw := newScriptedListener(scriptPermanent)
	listener := newTestListener(raw)
	go listener.acceptLoop()
	select {
	case <-listener.done:
	case <-time.After(testWaitTimeout):
		t.Fatal("永久 Accept 错误后 Listener 未完成")
	}

	ctx, cancel := context.WithTimeout(context.Background(), testWaitTimeout)
	defer cancel()
	if err := listener.Close(ctx); !errs.IsCode(err, errs.CodeTransportUnavailable) {
		t.Fatalf("永久 Accept 终态=%v", err)
	}
}

func TestListenerCloseFailureIsReported(t *testing.T) {
	t.Parallel()

	// 底层 Close 即使返回错误也必须先唤醒 AcceptLoop，并把错误保存为 Listener 终态。
	raw := newScriptedListener(scriptBlocking)
	raw.closeErr = errors.New("close failed")
	listener := newTestListener(raw)
	go listener.acceptLoop()
	select {
	case <-raw.firstAccept:
	case <-time.After(testWaitTimeout):
		t.Fatal("AcceptLoop 没有开始阻塞")
	}

	ctx, cancel := context.WithTimeout(context.Background(), testWaitTimeout)
	defer cancel()
	if err := listener.Close(ctx); !errs.IsCode(err, errs.CodeTransportUnavailable) {
		t.Fatalf("Listener Close 错误=%v", err)
	}
}

func TestConfigureTCPRejectsNonTCPAndSupportsDisabledKeepAlive(t *testing.T) {
	t.Parallel()

	// net.Pipe 不是 TCP socket，内部配置入口必须返回稳定 Transport 错误。
	left, right := net.Pipe()
	options := smallConnectionOptions(bufferpool.NewPool(bufferpool.Options{}))
	if err := configureTCP(left, options); !errs.IsCode(err, errs.CodeTransportUnavailable) {
		t.Fatalf("configureTCP(net.Pipe) error=%v", err)
	}
	_ = left.Close()
	_ = right.Close()

	// 使用真实回环 TCPConn 覆盖 KeepAlive=0 的显式关闭分支。
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("创建原始 Listener 失败：%v", err)
	}
	defer listener.Close()
	accepted := make(chan net.Conn, 1)
	go func() {
		conn, acceptErr := listener.Accept()
		if acceptErr == nil {
			accepted <- conn
		}
	}()
	client, err := net.Dial("tcp", listener.Addr().String())
	if err != nil {
		t.Fatalf("创建原始 TCP 客户端失败：%v", err)
	}
	defer client.Close()
	server := <-accepted
	defer server.Close()

	options.KeepAlive = 0
	if err := configureTCP(client, options); err != nil {
		t.Fatalf("关闭 KeepAlive 配置失败：%v", err)
	}
}

func TestInternalErrorHelpers(t *testing.T) {
	t.Parallel()

	// nil Handler 错误应原样保持 nil；未知 Context 错误按内部错误处理。
	if err := normalizeHandlerError(nil); err != nil {
		t.Fatalf("normalizeHandlerError(nil)=%v", err)
	}
	plain := errors.New("plain")
	if err := contextError(plain); !errs.IsCode(err, errs.CodeInternal) {
		t.Fatalf("contextError(plain)=%v", err)
	}
	if got := addrString(nil); got != "" {
		t.Fatalf("addrString(nil)=%q", got)
	}
	if got := maxFramePayload(3); got != 0 {
		t.Fatalf("maxFramePayload(3)=%d", got)
	}
}

func TestListenerExposesPermanentAcceptFailure(t *testing.T) {
	t.Parallel()

	raw := newScriptedListener(scriptPermanent)
	listener := newTestListener(raw)
	go listener.acceptLoop()

	select {
	case <-listener.AcceptDone():
	case <-time.After(time.Second):
		t.Fatal("永久 Accept 失败后 AcceptDone 未关闭")
	}
	if !errs.IsCode(listener.Cause(), errs.CodeTransportUnavailable) {
		t.Fatalf("Listener.Cause() = %v", listener.Cause())
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := listener.Close(ctx); !errs.IsCode(err, errs.CodeTransportUnavailable) {
		t.Fatalf("Listener.Close() error = %v", err)
	}
}

// acceptScript 指定 scriptedListener 的 Accept 行为。
type acceptScript uint8

const (
	// scriptTemporary 先返回一次临时错误，随后等待关闭。
	scriptTemporary acceptScript = iota
	// scriptPermanent 立即返回永久错误。
	scriptPermanent
	// scriptBlocking 从第一次调用开始等待关闭。
	scriptBlocking
)

// scriptedListener 是仅供 AcceptLoop 状态机测试使用的 net.Listener。
type scriptedListener struct {
	script acceptScript

	mu       sync.Mutex
	calls    int
	closeErr error

	firstAccept chan struct{}
	firstOnce   sync.Once
	closed      chan struct{}
	closeOnce   sync.Once
}

// newScriptedListener 初始化脚本和同步信号。
func newScriptedListener(script acceptScript) *scriptedListener {
	return &scriptedListener{
		script:      script,
		firstAccept: make(chan struct{}),
		closed:      make(chan struct{}),
	}
}

// Accept 按脚本返回临时、永久或关闭错误。
func (listener *scriptedListener) Accept() (net.Conn, error) {
	listener.firstOnce.Do(func() {
		close(listener.firstAccept)
	})
	listener.mu.Lock()
	call := listener.calls
	listener.calls++
	script := listener.script
	listener.mu.Unlock()

	switch {
	case script == scriptPermanent:
		return nil, errors.New("permanent accept failure")
	case script == scriptTemporary && call == 0:
		return nil, temporaryTestError{}
	default:
		<-listener.closed
		return nil, net.ErrClosed
	}
}

// Close 唤醒阻塞 Accept，并返回测试指定错误。
func (listener *scriptedListener) Close() error {
	listener.closeOnce.Do(func() {
		close(listener.closed)
	})
	return listener.closeErr
}

// Addr 返回固定测试地址。
func (listener *scriptedListener) Addr() net.Addr {
	return testAddr("listener")
}

// temporaryTestError 同时实现 net.Error 的临时错误语义。
type temporaryTestError struct{}

// Error 返回稳定测试文本。
func (temporaryTestError) Error() string {
	return "temporary accept failure"
}

// Timeout 表示该临时错误并非 Deadline。
func (temporaryTestError) Timeout() bool {
	return false
}

// Temporary 告知 AcceptLoop 可以退避重试。
func (temporaryTestError) Temporary() bool {
	return true
}

// newTestListener 使用脚本 net.Listener 构造完整内部 Listener。
func newTestListener(raw net.Listener) *Listener {
	// 本辅助函数与 Listen 保持相同字段不变量，但不绑定真实端口。
	pool := bufferpool.NewPool(bufferpool.Options{TrackUsage: true})
	options := DefaultListenOptions(pool)
	options.Connection.MaxMessageSize = 64
	return &Listener{
		raw:        raw,
		addr:       raw.Addr(),
		options:    options,
		handler:    newRecordingHandler(),
		logger:     options.Connection.Logger,
		conns:      make(map[*Conn]struct{}),
		closingCh:  make(chan struct{}),
		acceptDone: make(chan struct{}),
		done:       make(chan struct{}),
	}
}
