package kcpnet

import (
	"context"
	"errors"
	"net"
	"testing"

	kcplib "github.com/xtaci/kcp-go/v5"

	"github.com/duanhf2012/origin/v3/errs"
	"github.com/duanhf2012/origin/v3/internal/bufferpool"
	"github.com/duanhf2012/origin/v3/internal/lengthframe"
)

func TestConnFacadeOwnershipAndWait(t *testing.T) {
	pool, options := testConnectionOptions(t, 16, lengthframe.BigEndian)
	handler := newRecordingHandler()
	ctx, cancel := context.WithTimeout(context.Background(), kcpTestTimeout)
	defer cancel()
	conn, err := Dial(ctx, "127.0.0.1:9", DialOptions{Connection: options}, handler)
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-handler.opened:
	case <-ctx.Done():
		t.Fatal("OnOpen 未完成")
	}
	if err := conn.Send(nil); !errors.Is(err, errs.ErrInvalidArgument) {
		t.Fatalf("Send(nil)=%v", err)
	}
	oversized := pool.Acquire(17)
	if err := conn.Send(oversized); !errors.Is(err, errs.ErrTransportMessageTooLarge) {
		t.Fatalf("oversized Send=%v", err)
	}
	// 失败不转移所有权，调用方仍能安全释放。
	oversized.Release()
	canceled, cancelWait := context.WithCancel(context.Background())
	cancelWait()
	if err := conn.Wait(canceled); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled Wait=%v", err)
	}
	conn.Close()
	conn.Close()
	if err := conn.Wait(ctx); !errors.Is(err, errs.ErrTransportClosed) {
		t.Fatalf("terminal Wait=%v", err)
	}
	assertPoolEmpty(t, pool)

	var nilConn *Conn
	packet := pool.Acquire(1)
	if nilConn.Done() != nil || nilConn.Cause() == nil || nilConn.Writable() ||
		!errors.Is(nilConn.Send(packet), errs.ErrInvalidArgument) {
		t.Fatal("nil Conn facade 不安全")
	}
	packet.Release()
	if stats := nilConn.SendStats(); !stats.Closed {
		t.Fatalf("nil Conn stats=%+v", stats)
	}
	if err := nilConn.Wait(ctx); !errors.Is(err, errs.ErrInvalidArgument) {
		t.Fatalf("nil Conn Wait=%v", err)
	}
	nilConn.Close()
}

func TestHandlerPanicsAndErrorsBecomeStableCause(t *testing.T) {
	t.Run("open panic", func(t *testing.T) {
		_, options := testConnectionOptions(t, 64, lengthframe.BigEndian)
		handler := newRecordingHandler()
		handler.onOpen = func(*Conn) { panic("open") }
		ctx, cancel := context.WithTimeout(context.Background(), kcpTestTimeout)
		defer cancel()
		conn, err := Dial(ctx, "127.0.0.1:9", DialOptions{Connection: options}, handler)
		if err != nil {
			t.Fatal(err)
		}
		if err := conn.Wait(ctx); !errors.Is(err, errs.ErrInternal) {
			t.Fatalf("open panic cause=%v", err)
		}
	})

	t.Run("message error", func(t *testing.T) {
		_, serverOptions := testConnectionOptions(t, 64, lengthframe.BigEndian)
		serverHandler := newRecordingHandler()
		serverHandler.onMessage = func(_ *Conn, packet *bufferpool.Buffer) error {
			packet.Release()
			return errors.New("handler failed")
		}
		listener, err := Listen("127.0.0.1:0", ListenOptions{
			MaxConnections: 1, Connection: serverOptions,
		}, serverHandler)
		if err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithTimeout(context.Background(), kcpTestTimeout)
		defer cancel()
		defer listener.Close(ctx)
		clientPool, clientOptions := testConnectionOptions(t, 64, lengthframe.BigEndian)
		clientHandler := newRecordingHandler()
		clientHandler.onOpen = func(conn *Conn) {
			packet := clientPool.Acquire(1)
			if err := conn.Send(packet); err != nil {
				packet.Release()
			}
		}
		client, err := Dial(ctx, listener.Addr().String(), DialOptions{Connection: clientOptions}, clientHandler)
		if err != nil {
			t.Fatal(err)
		}
		defer client.Close()
		select {
		case cause := <-serverHandler.closed:
			if !errors.Is(cause, errs.ErrInternal) {
				t.Fatalf("message error cause=%v", cause)
			}
		case <-ctx.Done():
			t.Fatal("等待 Handler error 关闭超时")
		}
	})

	t.Run("close panic", func(t *testing.T) {
		_, options := testConnectionOptions(t, 64, lengthframe.BigEndian)
		handler := &closePanicHandler{opened: make(chan struct{})}
		ctx, cancel := context.WithTimeout(context.Background(), kcpTestTimeout)
		defer cancel()
		conn, err := Dial(ctx, "127.0.0.1:9", DialOptions{Connection: options}, handler)
		if err != nil {
			t.Fatal(err)
		}
		<-handler.opened
		conn.Close()
		if err := conn.Wait(ctx); !errors.Is(err, errs.ErrTransportClosed) {
			t.Fatalf("close panic changed cause=%v", err)
		}
	})
}

type closePanicHandler struct{ opened chan struct{} }

func (handler *closePanicHandler) OnOpen(*Conn) { close(handler.opened) }
func (*closePanicHandler) OnMessage(_ *Conn, packet *bufferpool.Buffer) error {
	packet.Release()
	return nil
}
func (*closePanicHandler) OnClose(*Conn, error) { panic("close") }

func TestOversizedRemoteDeclarationClosesConnection(t *testing.T) {
	_, options := testConnectionOptions(t, 64, lengthframe.BigEndian)
	handler := newRecordingHandler()
	listener, err := Listen("127.0.0.1:0", ListenOptions{
		MaxConnections: 1, Connection: options,
	}, handler)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), kcpTestTimeout)
	defer cancel()
	defer listener.Close(ctx)
	raw, err := kcplib.Dial(listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer raw.Close()
	if session, ok := raw.(*kcplib.UDPSession); ok {
		session.SetStreamMode(true)
		session.SetNoDelay(1, 10, 2, 1)
	}
	var header [4]byte
	lengthframe.Encode(&header, 65, lengthframe.Options{Size: 4, ByteOrder: lengthframe.BigEndian})
	if _, err := raw.Write(header[:]); err != nil {
		t.Fatal(err)
	}
	select {
	case cause := <-handler.closed:
		if !errors.Is(cause, errs.ErrTransportMessageTooLarge) {
			t.Fatalf("oversized declaration cause=%v", cause)
		}
	case <-ctx.Done():
		t.Fatal("等待超大声明关闭超时")
	}
}

func TestErrorHelpersAndWritablePanic(t *testing.T) {
	if normalizeIOError(nil) != nil {
		t.Fatal("normalizeIOError(nil) != nil")
	}
	if err := normalizeIOError(timeoutNetError{}); !errors.Is(err, errs.ErrDeadlineExceeded) {
		t.Fatalf("net timeout=%v", err)
	}
	if err := normalizeIOError(errors.New("timeout")); !errors.Is(err, errs.ErrDeadlineExceeded) {
		t.Fatalf("kcp timeout=%v", err)
	}
	if err := normalizeIOError(errors.New("io")); !errors.Is(err, errs.ErrTransportUnavailable) {
		t.Fatalf("io error=%v", err)
	}
	if normalizeHandlerError(nil) != nil {
		t.Fatal("normalizeHandlerError(nil) != nil")
	}
	if err := normalizeHandlerError(errs.ErrTransportProtocol); !errors.Is(err, errs.ErrTransportProtocol) {
		t.Fatalf("coded handler error=%v", err)
	}
	slow := slowClientError{}
	if slow.Error() == "" || slow.Code() != errs.CodeTransportOverloaded ||
		!slow.Is(errs.ErrTransportOverloaded) || !slow.SlowClient() {
		t.Fatal("slowClientError contract invalid")
	}
	conn := &Conn{}
	if err := conn.callOnWritableChanged(panicWritableHandler{}, false); !errors.Is(err, errs.ErrInternal) {
		t.Fatalf("writable panic=%v", err)
	}
	if addrString(nil) != "" || addrString(testNetAddr("remote")) != "remote" {
		t.Fatal("addrString result invalid")
	}
}

type timeoutNetError struct{}

func (timeoutNetError) Error() string   { return "deadline" }
func (timeoutNetError) Timeout() bool   { return true }
func (timeoutNetError) Temporary() bool { return true }

type panicWritableHandler struct{}

func (panicWritableHandler) OnWritableChanged(*Conn, bool) { panic("writable") }

type testNetAddr string

func (address testNetAddr) Network() string { return "test" }
func (address testNetAddr) String() string  { return string(address) }

var _ net.Error = timeoutNetError{}
var _ WritableHandler = panicWritableHandler{}
