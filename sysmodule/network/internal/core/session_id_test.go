package core

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net"
	"testing"
	"time"

	"github.com/duanhf2012/origin/v3/errs"
	"github.com/duanhf2012/origin/v3/internal/bufferpool"
	public "github.com/duanhf2012/origin/v3/sysmodule/network"
	"github.com/duanhf2012/origin/v3/util/identifier"
)

func TestRuntimeNewSessionRejectsRandomFailureAndRepeatedCollision(t *testing.T) {
	t.Run("random failure", func(t *testing.T) {
		runtime := newSessionIDTestRuntime(errorSessionIDReader{}, 1)
		session, err := runtime.NewSession(sessionIDTestConn{})
		if session != nil || !errs.IsCode(err, errs.CodeInternal) {
			t.Fatalf("NewSession() = %v, %v", session, err)
		}
	})

	t.Run("repeated collision", func(t *testing.T) {
		// 第一段创建活动 Session，后四段固定产生相同 ID，覆盖全部有界重试。
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

	t.Run("collision retries with a new ID", func(t *testing.T) {
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
		if first.id == "" || second.id == "" || first.id == second.id ||
			len(first.id) != identifier.TimeRandomLength || len(second.id) != identifier.TimeRandomLength ||
			len(runtime.sessions) != 2 {
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

type errorSessionIDReader struct{}

func (errorSessionIDReader) Read([]byte) (int, error) {
	return 0, errors.New("random source unavailable")
}

func newSessionIDTestRuntime(source io.Reader, maxSessions int) *Runtime {
	ctx, cancel := context.WithCancel(context.Background())
	return &Runtime{
		ctx:             ctx,
		cancel:          cancel,
		sessionIDSource: source,
		sessionIDNow: func() time.Time {
			return time.Unix(1_800_000_000, 0)
		},
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
