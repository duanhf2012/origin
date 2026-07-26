package natsnet

import (
	"context"
	"errors"
	"net"
	"testing"
	"time"
)

// TestInitialDialerContext 验证初始 Dial 受 Context 控制且 finish 可以完整回收观察者。
func TestInitialDialerContext(t *testing.T) {
	t.Parallel()

	// 使用已经取消的 Context，DialContext 必须立即返回取消错误且不创建观察 goroutine。
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	dialer := newInitialDialer(ctx, time.Second)
	_, err := dialer.Dial("tcp", "127.0.0.1:1")
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Dial() error = %v", err)
	}
	dialer.finish()
}

// TestInitialDialerSwitchesAfterFinish 验证初始阶段结束后不再持有原 Context。
func TestInitialDialerSwitchesAfterFinish(t *testing.T) {
	t.Parallel()

	// 本地 Listener 提供一个确定成功的后续普通 Dial。
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Listen() error = %v", err)
	}
	defer listener.Close()

	ctx, cancel := context.WithCancel(context.Background())
	dialer := newInitialDialer(ctx, time.Second)
	dialer.finish()
	cancel()

	accepted := make(chan net.Conn, 1)
	go func() {
		raw, acceptErr := listener.Accept()
		if acceptErr == nil {
			accepted <- raw
		}
	}()
	raw, err := dialer.Dial("tcp", listener.Addr().String())
	if err != nil {
		t.Fatalf("finish 后 Dial() 仍受旧 Context 影响：%v", err)
	}
	raw.Close()
	select {
	case serverConn := <-accepted:
		serverConn.Close()
	case <-time.After(time.Second):
		t.Fatal("Listener 没有接受后续普通 Dial")
	}
}
