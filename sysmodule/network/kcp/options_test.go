package kcp

import (
	"errors"
	"testing"
	"time"

	kcplib "github.com/xtaci/kcp-go/v5"

	"github.com/duanhf2012/origin/v3/errs"
	"github.com/duanhf2012/origin/v3/sysmodule/network"
)

func TestDefaultOptionsValidate(t *testing.T) {
	handler := network.HandlerFuncs{}
	server := DefaultServerOptions(handler)
	if err := validateServerOptions(server); err != nil {
		t.Fatal(err)
	}
	if server.Network.ReadIdleTimeout != defaultReadIdleTimeout || server.MTU != 1400 ||
		server.SendWindow != 1024 || server.ReceiveWindow != 1024 ||
		!server.NoDelay.Enabled || server.NoDelay.Interval != 10*time.Millisecond ||
		server.NoDelay.FastResend != 2 || !server.NoDelay.DisableCongestionControl {
		t.Fatalf("server defaults=%+v", server)
	}
	dial := DefaultDialOptions(handler)
	if err := validateDialOptions(dial); err != nil {
		t.Fatal(err)
	}
	client := DefaultClientOptions(handler)
	if err := validateClientOptions(client); err != nil {
		t.Fatal(err)
	}
}

func TestOptionsValidationRejectsEveryBoundary(t *testing.T) {
	handler := network.HandlerFuncs{}
	tests := []struct {
		name   string
		mutate func(*ServerOptions)
	}{
		{name: "read idle disabled", mutate: func(o *ServerOptions) { o.Network.ReadIdleTimeout = 0 }},
		{name: "frame width", mutate: func(o *ServerOptions) { o.Frame.LengthFieldSize = 3 }},
		{name: "frame order", mutate: func(o *ServerOptions) { o.Frame.ByteOrder = 0 }},
		{name: "frame capacity", mutate: func(o *ServerOptions) { o.Frame.LengthFieldSize = 1; o.Network.MaxMessageSize = 256 }},
		{name: "mtu low", mutate: func(o *ServerOptions) { o.MTU = 49 }},
		{name: "mtu high", mutate: func(o *ServerOptions) { o.MTU = 1501 }},
		{name: "send window", mutate: func(o *ServerOptions) { o.SendWindow = 0 }},
		{name: "receive window overflow", mutate: func(o *ServerOptions) { o.ReceiveWindow = 65536 }},
		{name: "interval low", mutate: func(o *ServerOptions) { o.NoDelay.Interval = 9 * time.Millisecond }},
		{name: "interval precision", mutate: func(o *ServerOptions) { o.NoDelay.Interval = 10*time.Millisecond + time.Nanosecond }},
		{name: "fast resend", mutate: func(o *ServerOptions) { o.NoDelay.FastResend = -1 }},
		{name: "fec half", mutate: func(o *ServerOptions) { o.FEC.DataShards = 4 }},
		{name: "fec total", mutate: func(o *ServerOptions) { o.FEC = FECOptions{DataShards: 200, ParityShards: 57} }},
		{name: "dscp", mutate: func(o *ServerOptions) { o.DSCP = 64 }},
		{name: "read buffer", mutate: func(o *ServerOptions) { o.SocketReadBuffer = -1 }},
		{name: "write buffer", mutate: func(o *ServerOptions) { o.SocketWriteBuffer = -1 }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			options := DefaultServerOptions(handler)
			test.mutate(&options)
			if err := validateServerOptions(options); !errors.Is(err, errs.ErrInvalidConfig) {
				t.Fatalf("error=%v", err)
			}
		})
	}
}

func TestEncryptionAndFECReduceSafeMTU(t *testing.T) {
	block, err := kcplib.NewAESBlockCrypt([]byte("origin-kcp-key16"))
	if err != nil {
		t.Fatal(err)
	}
	options := DefaultServerOptions(network.HandlerFuncs{})
	options.BlockCrypt = block
	options.MTU = 1481
	if err := validateServerOptions(options); !errors.Is(err, errs.ErrInvalidConfig) {
		t.Fatalf("encrypted mtu error=%v", err)
	}
	options.MTU = 1472
	options.FEC = FECOptions{DataShards: 4, ParityShards: 2}
	if err := validateServerOptions(options); err != nil {
		t.Fatalf("encrypted+fec safe mtu error=%v", err)
	}
	options.MTU++
	if err := validateServerOptions(options); !errors.Is(err, errs.ErrInvalidConfig) {
		t.Fatalf("encrypted+fec overflow error=%v", err)
	}
}

func TestDialAndReconnectValidation(t *testing.T) {
	handler := network.HandlerFuncs{}
	dial := DefaultDialOptions(handler)
	dial.Network.MaxSessions = 2
	if err := validateDialOptions(dial); !errors.Is(err, errs.ErrInvalidConfig) {
		t.Fatalf("dial sessions error=%v", err)
	}
	client := DefaultClientOptions(handler)
	client.Reconnect.Jitter = 1.1
	if err := validateClientOptions(client); !errors.Is(err, errs.ErrInvalidConfig) {
		t.Fatalf("jitter error=%v", err)
	}
}
