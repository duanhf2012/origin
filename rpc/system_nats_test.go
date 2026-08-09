package rpc

import (
	"context"
	"testing"
	"time"

	"github.com/duanhf2012/origin/v3/errs"
	"github.com/duanhf2012/origin/v3/internal/bufferpool"
	"github.com/duanhf2012/origin/v3/internal/timerwheel"
	originlog "github.com/duanhf2012/origin/v3/log"
	"github.com/nats-io/nats-server/v2/server"
)

// TestSystemNATSReusesNodeConnection verifies that the reserved Discovery subjects
// share each Node's RPC NATS connection and never require duplicated per-Node URLs.
func TestSystemNATSReusesNodeConnection(t *testing.T) {
	broker := startSystemNATSServer(t)
	pool := bufferpool.NewPool(bufferpool.Options{TrackUsage: true})
	engine, err := timerwheel.New(timerwheel.DefaultOptions())
	if err != nil {
		t.Fatalf("timerwheel.New() error = %v", err)
	}
	if err := engine.Start(); err != nil {
		t.Fatalf("Engine.Start() error = %v", err)
	}
	defer engine.Close()

	serverRuntime := newSystemNATSRuntime(t, "discovery-1", broker.ClientURL(), pool)
	serverHandler := newSystemTestHandler()
	serverHandler.onMessage = func(peer SystemPeer, payload []byte) {
		if err := peer.Send(append([]byte("ack:"), payload...)); err != nil {
			t.Errorf("server SystemPeer.Send() error = %v", err)
		}
	}
	if err := serverRuntime.BindSystemHandler(serverHandler); err != nil {
		t.Fatalf("server BindSystemHandler() error = %v", err)
	}
	if err := serverRuntime.Freeze(); err != nil {
		t.Fatalf("server Freeze() error = %v", err)
	}
	if err := serverRuntime.StartNetwork(context.Background(), engine); err != nil {
		t.Fatalf("server StartNetwork() error = %v", err)
	}
	defer serverRuntime.Close(context.Background())

	clientRuntime := newSystemNATSRuntime(t, "player-1", broker.ClientURL(), pool)
	if err := clientRuntime.Freeze(); err != nil {
		t.Fatalf("client Freeze() error = %v", err)
	}
	if err := clientRuntime.StartNetwork(context.Background(), engine); err != nil {
		t.Fatalf("client StartNetwork() error = %v", err)
	}
	defer clientRuntime.Close(context.Background())

	clientHandler := newSystemTestHandler()
	peer, err := clientRuntime.DialSystem(context.Background(), SystemTarget{
		NodeID: "discovery-1",
	}, clientHandler)
	if err != nil {
		t.Fatalf("DialSystem() error = %v", err)
	}
	if err := peer.Send([]byte("hello")); err != nil {
		t.Fatalf("SystemPeer.Send() error = %v", err)
	}
	select {
	case payload := <-serverHandler.messages:
		if string(payload) != "hello" {
			t.Fatalf("server payload = %q", payload)
		}
	case <-time.After(time.Second):
		t.Fatal("server did not receive NATS system message")
	}
	select {
	case payload := <-clientHandler.messages:
		if string(payload) != "ack:hello" {
			t.Fatalf("client payload = %q", payload)
		}
	case <-time.After(time.Second):
		t.Fatal("client did not receive NATS system response")
	}
}

// TestSystemNATSRemoteCloseNotifiesServer verifies that a normal client-side
// close removes the server-side peer as well. NATS has no connection-close
// callback for a peer that only communicates through request/reply subjects,
// so the control plane must carry this cleanup explicitly.
func TestSystemNATSRemoteCloseNotifiesServer(t *testing.T) {
	broker := startSystemNATSServer(t)
	pool := bufferpool.NewPool(bufferpool.Options{TrackUsage: true})
	engine, err := timerwheel.New(timerwheel.DefaultOptions())
	if err != nil {
		t.Fatalf("timerwheel.New() error = %v", err)
	}
	if err := engine.Start(); err != nil {
		t.Fatalf("Engine.Start() error = %v", err)
	}
	defer engine.Close()

	serverRuntime := newSystemNATSRuntime(t, "discovery-1", broker.ClientURL(), pool)
	serverHandler := newSystemTestHandler()
	if err := serverRuntime.BindSystemHandler(serverHandler); err != nil {
		t.Fatalf("server BindSystemHandler() error = %v", err)
	}
	if err := serverRuntime.Freeze(); err != nil {
		t.Fatalf("server Freeze() error = %v", err)
	}
	if err := serverRuntime.StartNetwork(context.Background(), engine); err != nil {
		t.Fatalf("server StartNetwork() error = %v", err)
	}
	defer serverRuntime.Close(context.Background())

	clientRuntime := newSystemNATSRuntime(t, "player-1", broker.ClientURL(), pool)
	if err := clientRuntime.Freeze(); err != nil {
		t.Fatalf("client Freeze() error = %v", err)
	}
	if err := clientRuntime.StartNetwork(context.Background(), engine); err != nil {
		t.Fatalf("client StartNetwork() error = %v", err)
	}
	defer clientRuntime.Close(context.Background())

	peer, err := clientRuntime.DialSystem(
		context.Background(),
		SystemTarget{NodeID: "discovery-1"},
		newSystemTestHandler(),
	)
	if err != nil {
		t.Fatalf("DialSystem() error = %v", err)
	}
	if err := peer.Send([]byte("hello")); err != nil {
		t.Fatalf("SystemPeer.Send() error = %v", err)
	}
	select {
	case <-serverHandler.messages:
	case <-time.After(time.Second):
		t.Fatal("server did not receive initial system message")
	}

	peer.Close()
	select {
	case <-serverHandler.closed:
	case <-time.After(time.Second):
		t.Fatal("server did not receive remote system close")
	}
}

// TestSystemNATSSessionSubjectsIsolateDuplicateNodeID catches control replies leaking between
// two processes that accidentally use the same NodeID with different process SessionIDs.
func TestSystemNATSSessionSubjectsIsolateDuplicateNodeID(t *testing.T) {
	broker := startSystemNATSServer(t)
	pool := bufferpool.NewPool(bufferpool.Options{TrackUsage: true})
	engine, err := timerwheel.New(timerwheel.DefaultOptions())
	if err != nil {
		t.Fatalf("timerwheel.New() error = %v", err)
	}
	if err := engine.Start(); err != nil {
		t.Fatalf("Engine.Start() error = %v", err)
	}
	defer engine.Close()

	serverRuntime := newSystemNATSRuntimeWithSession(
		t,
		"discovery-1",
		broker.ClientURL(),
		pool,
		1,
	)
	serverHandler := newSystemTestHandler()
	serverHandler.onMessage = func(peer SystemPeer, payload []byte) {
		if err := peer.Send(append([]byte("ack:"), payload...)); err != nil {
			t.Errorf("server SystemPeer.Send() error = %v", err)
		}
	}
	if err := serverRuntime.BindSystemHandler(serverHandler); err != nil {
		t.Fatalf("server BindSystemHandler() error = %v", err)
	}
	if err := serverRuntime.Freeze(); err != nil {
		t.Fatalf("server Freeze() error = %v", err)
	}
	if err := serverRuntime.StartNetwork(context.Background(), engine); err != nil {
		t.Fatalf("server StartNetwork() error = %v", err)
	}
	defer serverRuntime.Close(context.Background())

	firstRuntime := newSystemNATSRuntimeWithSession(
		t,
		"player-1",
		broker.ClientURL(),
		pool,
		101,
	)
	secondRuntime := newSystemNATSRuntimeWithSession(
		t,
		"player-1",
		broker.ClientURL(),
		pool,
		202,
	)
	for index, runtime := range []*Runtime{firstRuntime, secondRuntime} {
		if err := runtime.Freeze(); err != nil {
			t.Fatalf("client[%d] Freeze() error = %v", index, err)
		}
		if err := runtime.StartNetwork(context.Background(), engine); err != nil {
			t.Fatalf("client[%d] StartNetwork() error = %v", index, err)
		}
		defer runtime.Close(context.Background())
	}

	firstHandler := newSystemTestHandler()
	firstPeer, err := firstRuntime.DialSystem(
		context.Background(),
		SystemTarget{NodeID: "discovery-1"},
		firstHandler,
	)
	if err != nil {
		t.Fatalf("first DialSystem() error = %v", err)
	}
	secondHandler := newSystemTestHandler()
	_, err = secondRuntime.DialSystem(
		context.Background(),
		SystemTarget{NodeID: "discovery-1"},
		secondHandler,
	)
	if err != nil {
		t.Fatalf("second DialSystem() error = %v", err)
	}

	if err := firstPeer.Send([]byte("first")); err != nil {
		t.Fatalf("first SystemPeer.Send() error = %v", err)
	}
	select {
	case payload := <-firstHandler.messages:
		if string(payload) != "ack:first" {
			t.Fatalf("first client payload = %q", payload)
		}
	case <-time.After(time.Second):
		t.Fatal("first client did not receive its response")
	}
	select {
	case payload := <-secondHandler.messages:
		t.Fatalf("second Session received first Session response %q", payload)
	case <-time.After(100 * time.Millisecond):
	}
}

// TestSystemNATSRedialCreatesNewServerPeer catches a client-side disconnect
// whose old reply subject is still present on the server. A redial must create
// a fresh server peer so stateful protocols may send their initial Hello again.
func TestSystemNATSRedialCreatesNewServerPeer(t *testing.T) {
	broker := startSystemNATSServer(t)
	pool := bufferpool.NewPool(bufferpool.Options{TrackUsage: true})
	engine, err := timerwheel.New(timerwheel.DefaultOptions())
	if err != nil {
		t.Fatalf("timerwheel.New() error = %v", err)
	}
	if err := engine.Start(); err != nil {
		t.Fatalf("Engine.Start() error = %v", err)
	}
	defer engine.Close()

	serverRuntime := newSystemNATSRuntimeWithSession(
		t, "discovery-1", broker.ClientURL(), pool, 1,
	)
	serverHandler := newSystemTestHandler()
	serverHandler.onMessage = func(peer SystemPeer, payload []byte) {
		if err := peer.Send(append([]byte("ack:"), payload...)); err != nil {
			t.Errorf("server SystemPeer.Send() error = %v", err)
		}
	}
	if err := serverRuntime.BindSystemHandler(serverHandler); err != nil {
		t.Fatalf("server BindSystemHandler() error = %v", err)
	}
	if err := serverRuntime.Freeze(); err != nil {
		t.Fatalf("server Freeze() error = %v", err)
	}
	if err := serverRuntime.StartNetwork(context.Background(), engine); err != nil {
		t.Fatalf("server StartNetwork() error = %v", err)
	}
	defer serverRuntime.Close(context.Background())

	clientRuntime := newSystemNATSRuntimeWithSession(
		t, "player-1", broker.ClientURL(), pool, 101,
	)
	if err := clientRuntime.Freeze(); err != nil {
		t.Fatalf("client Freeze() error = %v", err)
	}
	if err := clientRuntime.StartNetwork(context.Background(), engine); err != nil {
		t.Fatalf("client StartNetwork() error = %v", err)
	}
	defer clientRuntime.Close(context.Background())

	firstHandler := newSystemTestHandler()
	first, err := clientRuntime.DialSystem(
		context.Background(),
		SystemTarget{NodeID: "discovery-1"},
		firstHandler,
	)
	if err != nil {
		t.Fatalf("first DialSystem() error = %v", err)
	}
	if err := first.Send([]byte("first")); err != nil {
		t.Fatalf("first Send() error = %v", err)
	}
	select {
	case <-serverHandler.opened:
	case <-time.After(time.Second):
		t.Fatal("server did not open the first peer")
	}
	select {
	case <-firstHandler.messages:
	case <-time.After(time.Second):
		t.Fatal("first peer did not receive its response")
	}

	// Simulate a one-sided transport loss: the client knows the peer is gone,
	// while the server has not received the best-effort close notification.
	first.(*natsSystemPeer).closeWith(errs.ErrTransportUnavailable)
	secondHandler := newSystemTestHandler()
	second, err := clientRuntime.DialSystem(
		context.Background(),
		SystemTarget{NodeID: "discovery-1"},
		secondHandler,
	)
	if err != nil {
		t.Fatalf("second DialSystem() error = %v", err)
	}
	if err := second.Send([]byte("second")); err != nil {
		t.Fatalf("second Send() error = %v", err)
	}
	select {
	case <-serverHandler.opened:
	case <-time.After(200 * time.Millisecond):
		t.Fatal("redial reused the stale server peer")
	}
	select {
	case payload := <-secondHandler.messages:
		if string(payload) != "ack:second" {
			t.Fatalf("second client payload = %q", payload)
		}
	case <-time.After(time.Second):
		t.Fatal("second peer did not receive its response")
	}
}

func newSystemNATSRuntime(
	t *testing.T,
	nodeID, url string,
	pool *bufferpool.Pool,
) *Runtime {
	return newSystemNATSRuntimeWithSession(t, nodeID, url, pool, 1)
}

func newSystemNATSRuntimeWithSession(
	t *testing.T,
	nodeID, url string,
	pool *bufferpool.Pool,
	sessionID uint64,
) *Runtime {
	t.Helper()
	runtime, err := NewRuntime(nodeID, pool, originlog.NewNop())
	if err != nil {
		t.Fatalf("NewRuntime() error = %v", err)
	}
	config := Config{
		Transport:        TransportNATS,
		MaxPayloadSize:   DefaultMaxPayloadSize,
		MaxBroadcastSize: DefaultMaxBroadcastSize,
		NATS:             DefaultNATSConfig(),
	}
	config.NATS.Namespace = "system-test"
	config.NATS.URLs = []string{url}
	if err := runtime.Configure(&config); err != nil {
		t.Fatalf("Configure() error = %v", err)
	}
	if err := runtime.EnableSystem(); err != nil {
		t.Fatalf("EnableSystem() error = %v", err)
	}
	if err := runtime.BindSessionID(sessionID); err != nil {
		t.Fatalf("BindSessionID() error = %v", err)
	}
	return runtime
}

func startSystemNATSServer(t *testing.T) *server.Server {
	t.Helper()
	running, err := server.NewServer(&server.Options{
		Host:       "127.0.0.1",
		Port:       -1,
		MaxPayload: MaxSystemMessageSize,
		NoLog:      true,
		NoSigs:     true,
	})
	if err != nil {
		t.Fatalf("server.NewServer() error = %v", err)
	}
	go running.Start()
	if !running.ReadyForConnections(time.Second) {
		running.Shutdown()
		t.Fatal("NATS server did not become ready")
	}
	t.Cleanup(running.Shutdown)
	return running
}
