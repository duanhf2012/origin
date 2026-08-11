package websocket

import (
	"context"
	"crypto/tls"
	"net/http"
	"testing"
	"time"

	"github.com/duanhf2012/origin/v3/errs"
	"github.com/duanhf2012/origin/v3/sysmodule/network"
)

func TestDefaultOptionsAreValidAndBounded(t *testing.T) {
	handler := network.HandlerFuncs{}
	server := DefaultServerOptions(handler)
	if err := validateServerOptions(server); err != nil {
		t.Fatal(err)
	}
	if server.Path != "/ws" || server.MessageType != BinaryMessage ||
		server.PingInterval <= 0 || server.PongTimeout <= server.PingInterval {
		t.Fatalf("server defaults=%+v", server)
	}
	dial := DefaultDialOptions(handler)
	if err := validateDialOptions("ws://127.0.0.1/ws", dial); err != nil {
		t.Fatal(err)
	}
	if dial.Network.MaxSessions != 1 {
		t.Fatalf("dial max sessions=%d", dial.Network.MaxSessions)
	}
	client := DefaultClientOptions(handler)
	if err := validateClientOptions("wss://example.test/ws", client); err != nil {
		t.Fatal(err)
	}
}

func TestOptionsRejectInvalidWebSocketSpecificValues(t *testing.T) {
	tests := []struct {
		name      string
		configure func(*ServerOptions)
	}{
		{name: "path empty", configure: func(options *ServerOptions) { options.Path = "" }},
		{name: "path query", configure: func(options *ServerOptions) { options.Path = "/ws?q=1" }},
		{name: "message type", configure: func(options *ServerOptions) { options.MessageType = 99 }},
		{name: "handshake", configure: func(options *ServerOptions) { options.HandshakeTimeout = 0 }},
		{name: "ping without pong", configure: func(options *ServerOptions) { options.PongTimeout = 0 }},
		{name: "pong too short", configure: func(options *ServerOptions) { options.PongTimeout = options.PingInterval }},
		{name: "subprotocol blank", configure: func(options *ServerOptions) { options.Subprotocols = []string{""} }},
		{name: "subprotocol separator", configure: func(options *ServerOptions) { options.Subprotocols = []string{"origin/v1"} }},
		{name: "subprotocol duplicate", configure: func(options *ServerOptions) { options.Subprotocols = []string{"v1", "v1"} }},
		{name: "tls no certificate", configure: func(options *ServerOptions) { options.TLSConfig = &tls.Config{} }},
		{name: "extension header", configure: func(options *ServerOptions) {
			options.ResponseHeader = http.Header{"sec-websocket-extensions": []string{"x"}}
		}},
		{name: "protocol response header", configure: func(options *ServerOptions) {
			options.ResponseHeader = http.Header{"sec-websocket-protocol": []string{"v1"}}
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			options := DefaultServerOptions(network.HandlerFuncs{})
			test.configure(&options)
			if err := validateServerOptions(options); !errs.IsCode(err, errs.CodeInvalidConfig) {
				t.Fatalf("error=%v", err)
			}
		})
	}
}

func TestDialOptionsRejectURLHeaderTLSAndCapacity(t *testing.T) {
	handler := network.HandlerFuncs{}
	for _, rawURL := range []string{"", "http://example.test/ws", "ws:///missing", "ws://u:p@example.test/ws"} {
		if err := validateDialOptions(rawURL, DefaultDialOptions(handler)); err == nil {
			t.Fatalf("URL %q unexpectedly accepted", rawURL)
		}
	}
	options := DefaultDialOptions(handler)
	options.TLSConfig = &tls.Config{MinVersion: tls.VersionTLS12}
	if err := validateDialOptions("ws://example.test/ws", options); err == nil {
		t.Fatal("ws URL accepted TLSConfig")
	}
	options = DefaultDialOptions(handler)
	options.Network.MaxSessions = 2
	if err := validateDialOptions("ws://example.test/ws", options); err == nil {
		t.Fatal("Dial accepted max_sessions=2")
	}
	options = DefaultDialOptions(handler)
	options.Header = http.Header{"sec-websocket-protocol": []string{"v1"}}
	if err := validateDialOptions("ws://example.test/ws", options); err == nil {
		t.Fatal("Dial accepted reserved Header")
	}
}

func TestConstructorsFreezeReferenceOptions(t *testing.T) {
	serverOptions := DefaultServerOptions(network.HandlerFuncs{})
	serverOptions.Subprotocols = []string{"v1"}
	serverOptions.ResponseHeader = http.Header{"X-Test": []string{"before"}}
	server, err := NewServer("127.0.0.1:0", serverOptions)
	if err != nil {
		t.Fatal(err)
	}
	serverOptions.Subprotocols[0] = "changed"
	serverOptions.ResponseHeader.Set("X-Test", "changed")
	if server.options.Subprotocols[0] != "v1" || server.options.ResponseHeader.Get("X-Test") != "before" {
		t.Fatal("Server 未冻结 Slice/Header")
	}

	dialOptions := DefaultDialOptions(network.HandlerFuncs{})
	dialOptions.Header = http.Header{"X-Test": []string{"before"}}
	dialOptions.Subprotocols = []string{"v1"}
	dialer, err := NewDialer("ws://127.0.0.1/ws", dialOptions)
	if err != nil {
		t.Fatal(err)
	}
	dialOptions.Header.Set("X-Test", "changed")
	dialOptions.Subprotocols[0] = "changed"
	if dialer.options.Header.Get("X-Test") != "before" || dialer.options.Subprotocols[0] != "v1" {
		t.Fatal("Dialer 未冻结 Header/Slice")
	}
}

func TestClientReconnectValidation(t *testing.T) {
	options := DefaultClientOptions(network.HandlerFuncs{})
	options.Reconnect.MaxAttempts = 0
	if err := validateClientOptions("ws://127.0.0.1/ws", options); err == nil {
		t.Fatal("Client accepted zero reconnect attempts")
	}
	options = DefaultClientOptions(network.HandlerFuncs{})
	options.Reconnect.InitialDelay = 2 * time.Second
	options.Reconnect.MaxDelay = time.Second
	if err := validateClientOptions("ws://127.0.0.1/ws", options); err == nil {
		t.Fatal("Client accepted decreasing reconnect delay")
	}
}

func TestNilAndUnstartedEndpointFacade(t *testing.T) {
	var nilServer *Server
	if nilServer.Addr() != nil || nilServer.SessionCount() != 0 ||
		nilServer.CloseSession(1, nil) || nilServer.Stats() != (network.EndpointStats{}) {
		t.Fatal("nil Server 外观异常")
	}
	if _, ok := nilServer.Session(1); ok {
		t.Fatal("nil Server 返回 Session")
	}
	server := &Server{}
	if err := server.OnStop(context.Background()); err != nil || server.Addr() != nil ||
		server.SessionCount() != 0 || server.Stats() != (network.EndpointStats{}) {
		t.Fatalf("未启动 Server 外观异常：%v", err)
	}

	var nilClient *Client
	if _, ok := nilClient.Session(); ok || nilClient.State().State != network.ClientStopped ||
		nilClient.Stats() != (network.EndpointStats{}) {
		t.Fatal("nil Client 外观异常")
	}
	client := &Client{}
	if err := client.OnStop(context.Background()); err != nil {
		t.Fatalf("未启动 Client.OnStop=%v", err)
	}
	if _, ok := client.Session(); ok || client.Stats() != (network.EndpointStats{}) {
		t.Fatal("未启动 Client 外观异常")
	}
}

func TestConstructorsRejectInvalidTargetsAndRetryDelayIsBounded(t *testing.T) {
	if _, err := NewServer("", DefaultServerOptions(network.HandlerFuncs{})); err == nil {
		t.Fatal("NewServer accepted empty address")
	}
	if _, err := NewClient("http://example/ws", DefaultClientOptions(network.HandlerFuncs{})); err == nil {
		t.Fatal("NewClient accepted HTTP URL")
	}
	if _, err := NewDialer("", DefaultDialOptions(network.HandlerFuncs{})); err == nil {
		t.Fatal("NewDialer accepted empty URL")
	}
	client := &Client{options: DefaultClientOptions(network.HandlerFuncs{})}
	client.options.Reconnect.Jitter = 0
	if got := client.retryDelay(1); got != 200*time.Millisecond {
		t.Fatalf("attempt 1 delay=%v", got)
	}
	if got := client.retryDelay(10); got != 5*time.Second {
		t.Fatalf("bounded delay=%v", got)
	}
}
