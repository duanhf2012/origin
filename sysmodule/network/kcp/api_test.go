package kcp

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/duanhf2012/origin/v3/errs"
	"github.com/duanhf2012/origin/v3/sysmodule/network"
)

func TestNilAndUnstartedFacadeIsSafe(t *testing.T) {
	var server *Server
	if server.Addr() != nil || server.SessionCount() != 0 ||
		server.CloseSession(1, nil) || server.Stats() != (network.EndpointStats{}) {
		t.Fatal("nil Server facade 不安全")
	}
	if session, ok := server.Session(1); session != nil || ok {
		t.Fatal("nil Server.Session 不安全")
	}
	var client *Client
	if session, ok := client.Session(); session != nil || ok {
		t.Fatal("nil Client.Session 不安全")
	}
	if client.State().State != network.ClientStopped ||
		client.Stats() != (network.EndpointStats{}) {
		t.Fatal("nil Client facade 不安全")
	}
	if err := (&Server{}).OnStop(context.Background()); err != nil {
		t.Fatalf("unstarted Server.OnStop=%v", err)
	}
	if err := (&Client{}).OnStop(context.Background()); err != nil {
		t.Fatalf("unstarted Client.OnStop=%v", err)
	}
	var dialer *Dialer
	if _, err := dialer.Dial(context.Background(), nil); !errors.Is(err, errs.ErrInvalidArgument) {
		t.Fatalf("nil Dialer.Dial=%v", err)
	}
}

func TestConstructorsRejectInvalidInput(t *testing.T) {
	handler := network.HandlerFuncs{}
	if _, err := NewServer("", DefaultServerOptions(handler)); !errors.Is(err, errs.ErrInvalidArgument) {
		t.Fatalf("NewServer empty address=%v", err)
	}
	badServer := DefaultServerOptions(handler)
	badServer.MTU = 0
	if _, err := NewServer("127.0.0.1:0", badServer); !errors.Is(err, errs.ErrInvalidConfig) {
		t.Fatalf("NewServer invalid options=%v", err)
	}
	if _, err := NewClient("", DefaultClientOptions(handler)); !errors.Is(err, errs.ErrInvalidArgument) {
		t.Fatalf("NewClient empty address=%v", err)
	}
	badClient := DefaultClientOptions(handler)
	badClient.Reconnect.MaxAttempts = 0
	if _, err := NewClient("127.0.0.1:1", badClient); !errors.Is(err, errs.ErrInvalidConfig) {
		t.Fatalf("NewClient invalid options=%v", err)
	}
	if _, err := NewDialer("", DefaultDialOptions(handler)); !errors.Is(err, errs.ErrInvalidArgument) {
		t.Fatalf("NewDialer empty address=%v", err)
	}
	badDial := DefaultDialOptions(handler)
	badDial.Network.MaxSessions = 2
	if _, err := NewDialer("127.0.0.1:1", badDial); !errors.Is(err, errs.ErrInvalidConfig) {
		t.Fatalf("NewDialer invalid options=%v", err)
	}
}

func TestClientRetryDelayAndStateCallbackPanic(t *testing.T) {
	client := &Client{options: DefaultClientOptions(network.HandlerFuncs{})}
	client.options.Reconnect.InitialDelay = 100 * time.Millisecond
	client.options.Reconnect.MaxDelay = 250 * time.Millisecond
	client.options.Reconnect.Jitter = 0
	if got := client.retryDelay(1); got != 100*time.Millisecond {
		t.Fatalf("attempt 1 delay=%s", got)
	}
	if got := client.retryDelay(3); got != 250*time.Millisecond {
		t.Fatalf("attempt 3 delay=%s", got)
	}
	client.options.Reconnect.Jitter = 0.2
	if got := client.retryDelay(1); got < 80*time.Millisecond || got > 120*time.Millisecond {
		t.Fatalf("jitter delay=%s", got)
	}
	client.options.StateChange = func(context.Context, network.ClientStateSnapshot) {
		panic("expected state panic")
	}
	// 未绑定 Module 的 Logger 是安全 Nop；回调 panic 必须被隔离而不是逃逸到调用方。
	client.notifyState(context.Background(), network.ClientStateSnapshot{State: network.ClientConnected})
}
