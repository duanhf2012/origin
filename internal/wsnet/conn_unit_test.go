package wsnet

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net"
	"testing"
	"time"

	gorillaws "github.com/gorilla/websocket"

	"github.com/duanhf2012/origin/v3/errs"
	"github.com/duanhf2012/origin/v3/internal/bufferpool"
)

func TestReadMessageGrowthBoundariesAndErrors(t *testing.T) {
	pool, options := testConnectionOptions(t, BinaryMessage, 1024)
	conn := &Conn{options: options}
	for _, size := range []int{0, 1, 255, 256, 257, 512, 1024} {
		payload := bytes.Repeat([]byte{byte(size)}, size)
		packet, err := conn.readMessage(bytes.NewReader(payload))
		if err != nil {
			t.Fatalf("size %d: %v", size, err)
		}
		if !bytes.Equal(packet.Bytes(), payload) {
			t.Fatalf("size %d payload mismatch", size)
		}
		packet.Release()
	}
	if packet, err := conn.readMessage(bytes.NewReader(make([]byte, 1025))); !errors.Is(err, errs.ErrTransportMessageTooLarge) || packet != nil {
		t.Fatalf("oversize=(%v,%v)", packet, err)
	}
	wantErr := errors.New("read failed")
	if packet, err := conn.readMessage(errorReader{err: wantErr}); !errors.Is(err, wantErr) || packet != nil {
		t.Fatalf("reader error=(%v,%v)", packet, err)
	}
	if packet, err := conn.readMessage(noProgressReader{}); !errors.Is(err, io.ErrNoProgress) || packet != nil {
		t.Fatalf("no progress=(%v,%v)", packet, err)
	}
	assertPoolEmpty(t, pool)
}

func TestConnectionAccessorsWaitAndSendValidation(t *testing.T) {
	pool, options := testConnectionOptions(t, TextMessage, 16)
	conn := &Conn{
		options:    options,
		localAddr:  testAddr("local"),
		remoteAddr: testAddr("remote"),
		done:       make(chan struct{}),
	}
	if conn.LocalAddr().String() != "local" || conn.RemoteAddr().String() != "remote" || conn.Done() == nil {
		t.Fatal("连接地址或 Done 外观无效")
	}
	if (*Conn)(nil).Done() != nil || (*Conn)(nil).Cause() != errs.ErrTransportClosed ||
		(*Conn)(nil).Writable() || !(*Conn)(nil).SendStats().Closed {
		t.Fatal("nil Conn 外观无效")
	}
	if conn.Cause() != nil {
		t.Fatal("运行中 Cause 应为空")
	}
	if err := conn.Send(nil); !errs.IsCode(err, errs.CodeInvalidArgument) {
		t.Fatalf("Send(nil)=%v", err)
	}
	tooLarge := pool.Acquire(17)
	if err := conn.Send(tooLarge); !errors.Is(err, errs.ErrTransportMessageTooLarge) {
		t.Fatalf("Send oversize=%v", err)
	}
	tooLarge.Release()
	invalidText := pool.Acquire(1)
	invalidText.Bytes()[0] = 0xff
	if err := conn.Send(invalidText); !errors.Is(err, errs.ErrTransportProtocol) {
		t.Fatalf("Send invalid text=%v", err)
	}
	invalidText.Release()

	if err := (*Conn)(nil).Wait(context.Background()); !errs.IsCode(err, errs.CodeInvalidArgument) {
		t.Fatalf("nil Wait=%v", err)
	}
	if err := conn.Wait(nil); !errs.IsCode(err, errs.CodeInvalidArgument) {
		t.Fatalf("Wait(nil)=%v", err)
	}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if err := conn.Wait(canceled); !errs.IsCode(err, errs.CodeCanceled) {
		t.Fatalf("Wait(canceled)=%v", err)
	}
	conn.cause = errs.ErrTransportClosed
	close(conn.done)
	if err := conn.Wait(context.Background()); !errors.Is(err, errs.ErrTransportClosed) ||
		!errors.Is(conn.Cause(), errs.ErrTransportClosed) {
		t.Fatalf("closed Wait/Cause=(%v,%v)", err, conn.Cause())
	}
	assertPoolEmpty(t, pool)
}

func TestCloseCodeAndErrorNormalization(t *testing.T) {
	if closeCode(errs.ErrTransportMessageTooLarge) != gorillaws.CloseMessageTooBig ||
		closeCode(errs.ErrTransportProtocol) != gorillaws.CloseProtocolError ||
		closeCode(errs.ErrTransportOverloaded) != gorillaws.CloseTryAgainLater ||
		closeCode(slowClientError{}) != gorillaws.CloseTryAgainLater ||
		closeCode(errors.New("ordinary")) != gorillaws.CloseNormalClosure {
		t.Fatal("Close Code 映射异常")
	}
	if (slowClientError{}).Error() == "" || !(slowClientError{}).SlowClient() {
		t.Fatal("slowClientError 契约无效")
	}
	tests := []struct {
		err  error
		want error
	}{
		{err: gorillaws.ErrReadLimit, want: errs.ErrTransportMessageTooLarge},
		{err: &gorillaws.CloseError{Code: gorillaws.CloseNormalClosure}, want: errs.ErrTransportClosed},
		{err: &gorillaws.CloseError{Code: gorillaws.CloseMessageTooBig}, want: errs.ErrTransportMessageTooLarge},
		{err: &gorillaws.CloseError{Code: gorillaws.CloseProtocolError}, want: errs.ErrTransportProtocol},
		{err: &gorillaws.CloseError{Code: gorillaws.CloseInternalServerErr}, want: errs.ErrTransportUnavailable},
		{err: timeoutError{}, want: errs.ErrDeadlineExceeded},
		{err: io.EOF, want: errs.ErrTransportUnavailable},
	}
	for _, test := range tests {
		if got := normalizeIOError(test.err); !errors.Is(got, test.want) {
			t.Fatalf("normalizeIOError(%v)=%v want=%v", test.err, got, test.want)
		}
	}
	if normalizeIOError(nil) != nil || normalizeHandlerError(nil) != nil {
		t.Fatal("nil error normalization changed")
	}
	stable := errs.ErrTransportProtocol
	if normalizeHandlerError(stable) != stable || !errs.IsCode(normalizeHandlerError(errors.New("x")), errs.CodeInternal) {
		t.Fatal("Handler error normalization changed")
	}
	for _, test := range []struct {
		input error
		code  errs.Code
	}{{context.Canceled, errs.CodeCanceled}, {context.DeadlineExceeded, errs.CodeDeadlineExceeded}, {errors.New("x"), errs.CodeInternal}} {
		if !errs.IsCode(contextError(test.input), test.code) {
			t.Fatalf("contextError(%v)", test.input)
		}
	}
}

func TestHandlerPanicBoundariesAndWritableCallback(t *testing.T) {
	panicHandler := &panicTestHandler{}
	conn := &Conn{handler: panicHandler}
	if err := conn.callOnOpen(); !errs.IsCode(err, errs.CodeInternal) {
		t.Fatalf("OnOpen panic=%v", err)
	}
	packet := bufferpool.NewPool(bufferpool.Options{}).Acquire(0)
	if err := conn.callOnMessage(packet); !errs.IsCode(err, errs.CodeInternal) {
		t.Fatalf("OnMessage panic=%v", err)
	}
	packet.Release()
	if err := conn.callOnClose(nil); !errs.IsCode(err, errs.CodeInternal) {
		t.Fatalf("OnClose panic=%v", err)
	}
	if err := conn.callOnWritableChanged(panicHandler, false); !errs.IsCode(err, errs.CodeInternal) {
		t.Fatalf("OnWritable panic=%v", err)
	}

	recorder := &writableTestHandler{values: make(chan bool, 1)}
	conn.handler = recorder
	conn.notifyWritableChanged(false)
	if got := <-recorder.values; got {
		t.Fatal("writable callback value=true")
	}
	conn.handler = newRecordingHandler()
	conn.notifyWritableChanged(true) // Handler 未实现 WritableHandler 时为空操作。
	if addrString(nil) != "" || addrString(testAddr("x")) != "x" {
		t.Fatal("addrString 结果异常")
	}
}

func TestInternalValidationAndInvalidEntryPoints(t *testing.T) {
	_, valid := testConnectionOptions(t, BinaryMessage, 16)
	invalids := []ConnectionOptions{
		{},
		func() ConnectionOptions { value := valid; value.MessageType = 99; return value }(),
		func() ConnectionOptions { value := valid; value.MaxMessageSize = 0; return value }(),
		func() ConnectionOptions { value := valid; value.WriteTimeout = 0; return value }(),
		func() ConnectionOptions { value := valid; value.PingInterval = time.Second; return value }(),
	}
	for _, options := range invalids {
		if err := validateConnectionOptions(options); !errs.IsCode(err, errs.CodeInvalidConfig) {
			t.Fatalf("invalid options error=%v", err)
		}
	}
	if _, err := Dial(nil, "ws://example/ws", DialOptions{}, newRecordingHandler()); !errs.IsCode(err, errs.CodeInvalidArgument) {
		t.Fatalf("Dial(nil)=%v", err)
	}
	if _, err := Dial(context.Background(), "", DialOptions{}, newRecordingHandler()); !errs.IsCode(err, errs.CodeInvalidArgument) {
		t.Fatalf("Dial(empty)=%v", err)
	}
	if _, err := Dial(context.Background(), "ws://example/ws", DialOptions{}, nil); !errs.IsCode(err, errs.CodeInvalidArgument) {
		t.Fatalf("Dial(nil handler)=%v", err)
	}
	if _, err := Listen("", ListenOptions{}, newRecordingHandler()); !errs.IsCode(err, errs.CodeInvalidArgument) {
		t.Fatalf("Listen(empty)=%v", err)
	}
	if _, err := Listen("127.0.0.1:0", ListenOptions{}, nil); !errs.IsCode(err, errs.CodeInvalidArgument) {
		t.Fatalf("Listen(nil handler)=%v", err)
	}
	if (*Listener)(nil).Addr() != nil || (*Listener)(nil).RejectedConnections() != 0 {
		t.Fatal("nil Listener 外观异常")
	}
	if err := (*Listener)(nil).Close(context.Background()); !errs.IsCode(err, errs.CodeInvalidArgument) {
		t.Fatalf("nil Listener.Close=%v", err)
	}
}

type errorReader struct{ err error }

func (reader errorReader) Read([]byte) (int, error) { return 0, reader.err }

type noProgressReader struct{}

func (noProgressReader) Read([]byte) (int, error) { return 0, nil }

type timeoutError struct{}

func (timeoutError) Error() string   { return "timeout" }
func (timeoutError) Timeout() bool   { return true }
func (timeoutError) Temporary() bool { return true }

type panicTestHandler struct{}

func (*panicTestHandler) OnOpen(*Conn)                              { panic("open") }
func (*panicTestHandler) OnMessage(*Conn, *bufferpool.Buffer) error { panic("message") }
func (*panicTestHandler) OnClose(*Conn, error)                      { panic("close") }
func (*panicTestHandler) OnWritableChanged(*Conn, bool)             { panic("writable") }

type writableTestHandler struct {
	recordingHandler
	values chan bool
}

func (handler *writableTestHandler) OnWritableChanged(_ *Conn, value bool) {
	handler.values <- value
}

type testAddr string

func (address testAddr) Network() string { return "test" }
func (address testAddr) String() string  { return string(address) }

var _ net.Error = timeoutError{}
