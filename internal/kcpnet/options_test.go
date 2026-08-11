package kcpnet

import (
	"context"
	"errors"
	"testing"
	"time"

	kcplib "github.com/xtaci/kcp-go/v5"

	"github.com/duanhf2012/origin/v3/errs"
	"github.com/duanhf2012/origin/v3/internal/lengthframe"
)

func TestValidateConnectionOptions(t *testing.T) {
	_, valid := testConnectionOptions(t, 1024, lengthframe.BigEndian)
	tests := []struct {
		name   string
		mutate func(*ConnectionOptions)
	}{
		{name: "nil pool", mutate: func(o *ConnectionOptions) { o.Pool = nil }},
		{name: "nil budget", mutate: func(o *ConnectionOptions) { o.SendBudget = nil }},
		{name: "frame size", mutate: func(o *ConnectionOptions) { o.Frame.LengthFieldSize = 3 }},
		{name: "frame order", mutate: func(o *ConnectionOptions) { o.Frame.ByteOrder = 0 }},
		{name: "message size", mutate: func(o *ConnectionOptions) { o.MaxMessageSize = 0 }},
		{name: "frame capacity", mutate: func(o *ConnectionOptions) { o.Frame.LengthFieldSize = 1; o.MaxMessageSize = 256 }},
		{name: "queue messages", mutate: func(o *ConnectionOptions) { o.SendQueueMessages = 0 }},
		{name: "queue bytes", mutate: func(o *ConnectionOptions) { o.SendQueueBytes = 1 }},
		{name: "queue budget", mutate: func(o *ConnectionOptions) { o.SendQueueBytes = 2 * 1024 * 1024 }},
		{name: "read timeout", mutate: func(o *ConnectionOptions) { o.ReadTimeout = 0 }},
		{name: "write timeout", mutate: func(o *ConnectionOptions) { o.WriteTimeout = 0 }},
		{name: "slow timeout", mutate: func(o *ConnectionOptions) { o.SlowClientTimeout = 0 }},
		{name: "mtu low", mutate: func(o *ConnectionOptions) { o.Protocol.MTU = 49 }},
		{name: "mtu high", mutate: func(o *ConnectionOptions) { o.Protocol.MTU = 1501 }},
		{name: "send window", mutate: func(o *ConnectionOptions) { o.Protocol.SendWindow = 0 }},
		{name: "receive window", mutate: func(o *ConnectionOptions) { o.Protocol.ReceiveWindow = 65536 }},
		{name: "interval", mutate: func(o *ConnectionOptions) { o.Protocol.NoDelay.Interval = 9 * time.Millisecond }},
		{name: "interval precision", mutate: func(o *ConnectionOptions) { o.Protocol.NoDelay.Interval += time.Nanosecond }},
		{name: "fast resend", mutate: func(o *ConnectionOptions) { o.Protocol.NoDelay.FastResend = -1 }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			options := valid
			test.mutate(&options)
			if err := validateConnectionOptions(options); !errors.Is(err, errs.ErrInvalidConfig) {
				t.Fatalf("error=%v", err)
			}
		})
	}
}

func TestValidateWireOptions(t *testing.T) {
	block, err := kcplib.NewAESBlockCrypt([]byte("origin-kcp-key16"))
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name        string
		block       kcplib.BlockCrypt
		fec         FECOptions
		dscp        int
		readBuffer  int
		writeBuffer int
		mtu         int
	}{
		{name: "fec half", fec: FECOptions{DataShards: 1}, mtu: 1400},
		{name: "fec negative", fec: FECOptions{DataShards: -1, ParityShards: -1}, mtu: 1400},
		{name: "fec too many", fec: FECOptions{DataShards: 200, ParityShards: 57}, mtu: 1400},
		{name: "dscp low", dscp: -1, mtu: 1400},
		{name: "dscp high", dscp: 64, mtu: 1400},
		{name: "read buffer", readBuffer: -1, mtu: 1400},
		{name: "write buffer", writeBuffer: -1, mtu: 1400},
		{name: "encrypted mtu", block: block, mtu: 1481},
		{name: "fec mtu", fec: FECOptions{DataShards: 4, ParityShards: 2}, mtu: 1493},
		{name: "encrypted fec mtu", block: block, fec: FECOptions{DataShards: 4, ParityShards: 2}, mtu: 1473},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if err := validateWireOptions(
				test.block, test.fec, test.dscp,
				test.readBuffer, test.writeBuffer, test.mtu,
			); !errors.Is(err, errs.ErrInvalidConfig) {
				t.Fatalf("error=%v", err)
			}
		})
	}
	if err := validateWireOptions(
		block, FECOptions{DataShards: 4, ParityShards: 2}, 63, 1024, 1024, 1472,
	); err != nil {
		t.Fatalf("valid boundary error=%v", err)
	}
}

func TestPublicArgumentValidation(t *testing.T) {
	_, connection := testConnectionOptions(t, 64, lengthframe.BigEndian)
	if _, err := Listen("", ListenOptions{MaxConnections: 1, Connection: connection}, newRecordingHandler()); !errors.Is(err, errs.ErrInvalidArgument) {
		t.Fatalf("empty listen address error=%v", err)
	}
	if _, err := Listen("127.0.0.1:0", ListenOptions{MaxConnections: 1, Connection: connection}, nil); !errors.Is(err, errs.ErrInvalidArgument) {
		t.Fatalf("nil listen handler error=%v", err)
	}
	if _, err := Dial(nil, "127.0.0.1:1", DialOptions{Connection: connection}, newRecordingHandler()); !errors.Is(err, errs.ErrInvalidArgument) {
		t.Fatalf("nil dial context error=%v", err)
	}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := Dial(canceled, "127.0.0.1:1", DialOptions{Connection: connection}, newRecordingHandler()); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled dial error=%v", err)
	}
}
