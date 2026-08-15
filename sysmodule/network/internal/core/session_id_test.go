package core

import (
	"bytes"
	"context"
	cryptorand "crypto/rand"
	"errors"
	"io"
	"net"
	"testing"

	"github.com/duanhf2012/origin/v3/errs"
	"github.com/duanhf2012/origin/v3/internal/bufferpool"
	public "github.com/duanhf2012/origin/v3/sysmodule/network"
)

func TestNewSessionIDEncodesUUIDV4(t *testing.T) {
	source := []byte{
		0x00, 0x01, 0x02, 0x03,
		0x04, 0x05,
		0x06, 0x07,
		0x08, 0x09,
		0x0a, 0x0b, 0x0c, 0x0d, 0x0e, 0x0f,
	}
	id, err := newSessionID(bytes.NewReader(source))
	if err != nil {
		t.Fatal(err)
	}
	const want public.SessionID = "00010203-0405-4607-8809-0a0b0c0d0e0f"
	if id != want {
		t.Fatalf("newSessionID() = %q, want %q", id, want)
	}
}

func TestNewSessionIDRejectsUnavailableRandomSource(t *testing.T) {
	tests := []struct {
		name   string
		source io.Reader
	}{
		{name: "nil"},
		{name: "short", source: bytes.NewReader(make([]byte, 15))},
		{name: "error", source: errorSessionIDReader{}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			id, err := newSessionID(test.source)
			if err == nil || id != "" {
				t.Fatalf("newSessionID() = %q, %v", id, err)
			}
		})
	}
}

func TestNewSessionIDDoesNotCollideAcrossIndependentCalls(t *testing.T) {
	const count = 16_384
	seen := make(map[public.SessionID]struct{}, count)
	for index := 0; index < count; index++ {
		id, err := newSessionID(cryptorand.Reader)
		if err != nil {
			t.Fatal(err)
		}
		if id == "" {
			t.Fatal("newSessionID() returned empty ID")
		}
		if _, exists := seen[id]; exists {
			t.Fatalf("duplicate SessionID %q", id)
		}
		seen[id] = struct{}{}
	}
}

func TestRuntimeNewSessionRejectsRandomFailureAndRepeatedCollision(t *testing.T) {
	t.Run("random failure", func(t *testing.T) {
		runtime := newSessionIDTestRuntime(errorSessionIDReader{}, 1)
		session, err := runtime.NewSession(sessionIDTestConn{})
		if session != nil || !errs.IsCode(err, errs.CodeInternal) {
			t.Fatalf("NewSession() = %v, %v", session, err)
		}
	})

	t.Run("repeated collision", func(t *testing.T) {
		// 第一段创建活动 Session，后四段固定产生相同 UUID，覆盖全部有界重试。
		source := bytes.NewReader(make([]byte, 16*(maxSessionIDGenerationAttempts+1)))
		runtime := newSessionIDTestRuntime(source, 2)
		first, err := runtime.NewSession(sessionIDTestConn{})
		if err != nil || first == nil {
			t.Fatalf("first NewSession() = %v, %v", first, err)
		}
		second, err := runtime.NewSession(sessionIDTestConn{})
		if second != nil || !errs.IsCode(err, errs.CodeInternal) {
			t.Fatalf("colliding NewSession() = %v, %v", second, err)
		}
		if len(runtime.sessions) != 1 || runtime.sessions[first.id] != first {
			t.Fatalf("collision changed sessions: %+v", runtime.sessions)
		}
	})
}

func TestRuntimeNewSessionAdmissionAndCollisionRetry(t *testing.T) {
	t.Run("invalid arguments", func(t *testing.T) {
		var nilRuntime *Runtime
		if session, err := nilRuntime.NewSession(sessionIDTestConn{}); session != nil ||
			!errors.Is(err, errs.ErrInvalidArgument) {
			t.Fatalf("nil Runtime NewSession() = %v, %v", session, err)
		}
		runtime := newSessionIDTestRuntime(bytes.NewReader(make([]byte, 16)), 1)
		if session, err := runtime.NewSession(nil); session != nil ||
			!errors.Is(err, errs.ErrInvalidArgument) {
			t.Fatalf("nil Conn NewSession() = %v, %v", session, err)
		}
	})

	t.Run("stopping and capacity reject before random", func(t *testing.T) {
		stopping := newSessionIDTestRuntime(errorSessionIDReader{}, 1)
		stopping.stopping = true
		if session, err := stopping.NewSession(sessionIDTestConn{}); session != nil ||
			!errors.Is(err, errs.ErrTransportOverloaded) || stopping.rejected.Load() != 1 {
			t.Fatalf("stopping NewSession() = %v, %v rejected=%d", session, err, stopping.rejected.Load())
		}

		full := newSessionIDTestRuntime(errorSessionIDReader{}, 1)
		full.sessions["existing"] = &Session{}
		if session, err := full.NewSession(sessionIDTestConn{}); session != nil ||
			!errors.Is(err, errs.ErrTransportOverloaded) || full.rejected.Load() != 1 {
			t.Fatalf("full NewSession() = %v, %v rejected=%d", session, err, full.rejected.Load())
		}
	})

	t.Run("collision retries with a new UUID", func(t *testing.T) {
		zero := make([]byte, 16)
		ones := bytes.Repeat([]byte{1}, 16)
		source := bytes.NewReader(append(append(zero, zero...), ones...))
		runtime := newSessionIDTestRuntime(source, 2)
		first, err := runtime.NewSession(sessionIDTestConn{})
		if err != nil {
			t.Fatal(err)
		}
		second, err := runtime.NewSession(sessionIDTestConn{})
		if err != nil {
			t.Fatal(err)
		}
		if first.id == "" || second.id == "" || first.id == second.id || len(runtime.sessions) != 2 {
			t.Fatalf("first=%q second=%q sessions=%d", first.id, second.id, len(runtime.sessions))
		}
		if session, ok := runtime.Session(""); session != nil || ok {
			t.Fatalf("Session(empty) = %v, %v", session, ok)
		}
		if session, ok := runtime.Session(second.id); !ok || session != second {
			t.Fatalf("Session(second) = %v, %v", session, ok)
		}
		if runtime.CloseSession("missing", nil) || !runtime.CloseSession(second.id, nil) {
			t.Fatal("CloseSession lookup semantics changed")
		}
	})
}

func BenchmarkNewSessionID(b *testing.B) {
	b.ReportAllocs()
	for b.Loop() {
		id, err := newSessionID(cryptorand.Reader)
		if err != nil {
			b.Fatal(err)
		}
		benchmarkSessionIDSink = id
	}
}

type errorSessionIDReader struct{}

func (errorSessionIDReader) Read([]byte) (int, error) {
	return 0, errors.New("random source unavailable")
}

var benchmarkSessionIDSink public.SessionID

func newSessionIDTestRuntime(source io.Reader, maxSessions int) *Runtime {
	ctx, cancel := context.WithCancel(context.Background())
	return &Runtime{
		ctx:             ctx,
		cancel:          cancel,
		sessionIDSource: source,
		options: public.EndpointOptions{
			MaxSessions: maxSessions,
		},
		sessions: make(map[public.SessionID]*Session),
	}
}

type sessionIDTestConn struct{}

func (sessionIDTestConn) LocalAddr() net.Addr           { return nil }
func (sessionIDTestConn) RemoteAddr() net.Addr          { return nil }
func (sessionIDTestConn) Send(*bufferpool.Buffer) error { return nil }
func (sessionIDTestConn) Close()                        {}
func (sessionIDTestConn) Writable() bool                { return true }
func (sessionIDTestConn) Stats() TransportStats         { return TransportStats{} }
