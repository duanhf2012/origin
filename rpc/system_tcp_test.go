package rpc

import (
	"context"
	"net"
	"testing"
	"time"

	"github.com/duanhf2012/origin/v3/internal/bufferpool"
	"github.com/duanhf2012/origin/v3/internal/timerwheel"
	originlog "github.com/duanhf2012/origin/v3/log"
)

type systemTestHandler struct {
	opened    chan SystemPeer
	messages  chan []byte
	closed    chan error
	onMessage func(SystemPeer, []byte)
}

func newSystemTestHandler() *systemTestHandler {
	return &systemTestHandler{
		opened:   make(chan SystemPeer, 4),
		messages: make(chan []byte, 4),
		closed:   make(chan error, 4),
	}
}

func (handler *systemTestHandler) OnSystemOpen(peer SystemPeer) {
	handler.opened <- peer
}

func (handler *systemTestHandler) OnSystemMessage(peer SystemPeer, payload []byte) {
	copyPayload := append([]byte(nil), payload...)
	handler.messages <- copyPayload
	if handler.onMessage != nil {
		handler.onMessage(peer, copyPayload)
	}
}

func (handler *systemTestHandler) OnSystemClose(_ SystemPeer, cause error) {
	handler.closed <- cause
}

// TestSystemTCPReusesBusinessListener verifies that Discovery control traffic reaches the
// server over the Node's existing RPC listen address without entering business RPC routing.
func TestSystemTCPReusesBusinessListener(t *testing.T) {
	serverAddress := reserveTCPAddress(t)
	clientAddress := reserveTCPAddress(t)
	pool := bufferpool.NewPool(bufferpool.Options{TrackUsage: true})
	engine, err := timerwheel.New(timerwheel.DefaultOptions())
	if err != nil {
		t.Fatalf("timerwheel.New() error = %v", err)
	}
	if err := engine.Start(); err != nil {
		t.Fatalf("Engine.Start() error = %v", err)
	}
	defer engine.Close()

	server := newSystemTCPRuntime(t, "discovery-1", serverAddress, pool)
	serverHandler := newSystemTestHandler()
	serverHandler.onMessage = func(peer SystemPeer, payload []byte) {
		if err := peer.Send(append([]byte("ack:"), payload...)); err != nil {
			t.Errorf("server SystemPeer.Send() error = %v", err)
		}
	}
	if err := server.BindSystemHandler(serverHandler); err != nil {
		t.Fatalf("server BindSystemHandler() error = %v", err)
	}
	if err := server.Freeze(); err != nil {
		t.Fatalf("server Freeze() error = %v", err)
	}
	if err := server.StartNetwork(context.Background(), engine); err != nil {
		t.Fatalf("server StartNetwork() error = %v", err)
	}
	defer server.Close(context.Background())

	client := newSystemTCPRuntime(t, "player-1", clientAddress, pool)
	if err := client.Freeze(); err != nil {
		t.Fatalf("client Freeze() error = %v", err)
	}
	if err := client.StartNetwork(context.Background(), engine); err != nil {
		t.Fatalf("client StartNetwork() error = %v", err)
	}
	defer client.Close(context.Background())

	clientHandler := newSystemTestHandler()
	peer, err := client.DialSystem(context.Background(), SystemTarget{
		NodeID:  "discovery-1",
		Address: serverAddress,
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
		t.Fatal("server did not receive system message")
	}
	select {
	case payload := <-clientHandler.messages:
		if string(payload) != "ack:hello" {
			t.Fatalf("client payload = %q", payload)
		}
	case <-time.After(time.Second):
		t.Fatal("client did not receive system response")
	}
}

func newSystemTCPRuntime(
	t *testing.T,
	nodeID, address string,
	pool *bufferpool.Pool,
) *Runtime {
	t.Helper()
	runtime, err := NewRuntime(nodeID, pool, originlog.NewNop())
	if err != nil {
		t.Fatalf("NewRuntime() error = %v", err)
	}
	config := DefaultConfig()
	config.TCP.Listen = address
	config.TCP.Advertise = address
	if err := runtime.Configure(&config); err != nil {
		t.Fatalf("Configure() error = %v", err)
	}
	if err := runtime.EnableSystem(); err != nil {
		t.Fatalf("EnableSystem() error = %v", err)
	}
	return runtime
}

func reserveTCPAddress(t *testing.T) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("net.Listen() error = %v", err)
	}
	address := listener.Addr().String()
	if err := listener.Close(); err != nil {
		t.Fatalf("listener.Close() error = %v", err)
	}
	return address
}
