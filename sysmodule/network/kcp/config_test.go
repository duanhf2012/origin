package kcp

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	originconfig "github.com/duanhf2012/origin/v3/config"
	"github.com/duanhf2012/origin/v3/errs"
	"github.com/duanhf2012/origin/v3/sysmodule/network"
)

func TestDefaultConfigsConvertToValidOptions(t *testing.T) {
	handler := network.HandlerFuncs{}
	serverConfig := DefaultServerConfig()
	serverOptions, err := serverConfig.Options(handler)
	if err != nil {
		t.Fatalf("ServerConfig.Options() error = %v", err)
	}
	if serverOptions.Frame.ByteOrder != network.BigEndian || serverOptions.MTU != 1400 ||
		serverOptions.SendWindow != 1024 || serverOptions.ReceiveWindow != 1024 ||
		serverOptions.Network.ReadIdleTimeout != 60*time.Second {
		t.Fatalf("server options = %+v", serverOptions)
	}

	clientConfig := DefaultClientConfig()
	clientOptions, err := clientConfig.Options(handler)
	if err != nil {
		t.Fatalf("ClientConfig.Options() error = %v", err)
	}
	if clientOptions.Reconnect.Enabled || clientOptions.Reconnect.MaxAttempts != 10 {
		t.Fatalf("client reconnect = %+v", clientOptions.Reconnect)
	}
	// Client 只有一个 Session，未公开的端点总预算应收敛为对应的单 Session 容量。
	expected := DefaultClientOptions(handler)
	expected.Dial.Network.ReceivePendingTotalSize = expected.Dial.Network.ReceivePendingSize
	expected.Dial.Network.SendQueueTotalSize = expected.Dial.Network.SendQueueSize
	if !reflect.DeepEqual(clientOptions, expected) {
		t.Fatalf("client options = %+v, want %+v", clientOptions, expected)
	}
	if clientOptions.Dial.Network.MaxSessions != 1 ||
		clientOptions.Dial.Network.ReceivePendingTotalSize != clientOptions.Dial.Network.ReceivePendingSize ||
		clientOptions.Dial.Network.SendQueueTotalSize != clientOptions.Dial.Network.SendQueueSize {
		t.Fatalf("client dial options = %+v", clientOptions.Dial)
	}
}

func TestConfigStrictDecodeAndConvert(t *testing.T) {
	directory := t.TempDir()
	if err := os.WriteFile(filepath.Join(directory, "kcp.yaml"), []byte(`
address: "127.0.0.1:29092"
frame: {length_field_size: 2, byte_order: little}
mtu: 1300
send_window: 512
receive_window: 768
no_delay:
  enabled: true
  interval: 20ms
  fast_resend: 3
  disable_congestion_control: false
ack_no_delay: true
write_delay: true
fec: {data_shards: 4, parity_shards: 2}
dscp: 46
socket_read_buffer: 1M
socket_write_buffer: 2M
max_message_size: 32KB
receive_pending_messages: 8
receive_pending_size: 64KB
send_queue_messages: 16
send_queue_size: 96KB
read_idle_timeout: 2m
write_timeout: 7s
slow_client_timeout: 4s
reconnect:
  enabled: true
  max_attempts: 6
  initial_delay: 100ms
  max_delay: 2s
  jitter: 0.1
`), 0o600); err != nil {
		t.Fatal(err)
	}
	snapshot, err := originconfig.LoadSnapshot(directory)
	if err != nil {
		t.Fatal(err)
	}
	configured := DefaultClientConfig()
	if err := snapshot.Root().DecodeStrict(&configured); err != nil {
		t.Fatalf("DecodeStrict() error = %v", err)
	}
	options, err := configured.Options(network.HandlerFuncs{})
	if err != nil {
		t.Fatalf("ClientConfig.Options() error = %v", err)
	}
	if configured.Address != "127.0.0.1:29092" ||
		options.Dial.Frame.ByteOrder != network.LittleEndian ||
		options.Dial.Frame.LengthFieldSize != 2 || options.Dial.MTU != 1300 ||
		options.Dial.SendWindow != 512 || options.Dial.ReceiveWindow != 768 ||
		options.Dial.NoDelay.Interval != 20*time.Millisecond || options.Dial.NoDelay.FastResend != 3 ||
		options.Dial.NoDelay.DisableCongestionControl || !options.Dial.ACKNoDelay || !options.Dial.WriteDelay ||
		options.Dial.FEC != (FECOptions{DataShards: 4, ParityShards: 2}) || options.Dial.DSCP != 46 ||
		options.Dial.SocketReadBuffer != 1024*1024 || options.Dial.SocketWriteBuffer != 2*1024*1024 ||
		options.Dial.Network.MaxMessageSize != 32*1024 ||
		options.Dial.Network.ReadIdleTimeout != 2*time.Minute {
		t.Fatalf("dial options = %+v", options.Dial)
	}
	if !options.Reconnect.Enabled || options.Reconnect.MaxAttempts != 6 ||
		options.Reconnect.InitialDelay != 100*time.Millisecond || options.Reconnect.Jitter != 0.1 {
		t.Fatalf("reconnect options = %+v", options.Reconnect)
	}
}

func TestConfigStrictDecodeRejectsUnknownField(t *testing.T) {
	directory := t.TempDir()
	if err := os.WriteFile(filepath.Join(directory, "kcp.yaml"), []byte(`
address: "127.0.0.1:29092"
dial_timeout: 3s
`), 0o600); err != nil {
		t.Fatal(err)
	}
	snapshot, err := originconfig.LoadSnapshot(directory)
	if err != nil {
		t.Fatal(err)
	}
	configured := DefaultClientConfig()
	if err := snapshot.Root().DecodeStrict(&configured); err == nil {
		t.Fatal("KCP 不存在 dial_timeout，严格解码不应接受该字段")
	}
}

func TestConfigRejectsInvalidValues(t *testing.T) {
	handler := network.HandlerFuncs{}

	server := DefaultServerConfig()
	server.Address = ""
	if _, err := server.Options(handler); err == nil {
		t.Fatal("empty server address unexpectedly accepted")
	}

	server = DefaultServerConfig()
	server.Frame.ByteOrder = "native"
	if _, err := server.Options(handler); !errs.IsCode(err, errs.CodeInvalidConfig) {
		t.Fatalf("invalid byte order error = %v", err)
	}

	server = DefaultServerConfig()
	server.MTU = 49
	if _, err := server.Options(handler); !errs.IsCode(err, errs.CodeInvalidConfig) {
		t.Fatalf("invalid mtu error = %v", err)
	}

	server = DefaultServerConfig()
	server.FEC = FECConfig{DataShards: 4}
	if _, err := server.Options(handler); !errs.IsCode(err, errs.CodeInvalidConfig) {
		t.Fatalf("invalid fec error = %v", err)
	}

	client := DefaultClientConfig()
	client.Address = ""
	if _, err := client.Options(handler); err == nil {
		t.Fatal("empty client address unexpectedly accepted")
	}

	client = DefaultClientConfig()
	client.SocketReadBuffer = originconfig.ByteSize(-1)
	if _, err := client.Options(handler); !errs.IsCode(err, errs.CodeInvalidConfig) {
		t.Fatalf("negative socket buffer error = %v", err)
	}

	client = DefaultClientConfig()
	client.Reconnect.Jitter = -0.1
	if _, err := client.Options(handler); !errs.IsCode(err, errs.CodeInvalidConfig) {
		t.Fatalf("invalid jitter error = %v", err)
	}

	server = DefaultServerConfig()
	server.Frame.LengthFieldSize = 1
	server.MaxMessageSize = originconfig.ByteSize(256)
	if _, err := server.Options(handler); !errs.IsCode(err, errs.CodeInvalidConfig) {
		t.Fatalf("frame capacity error = %v", err)
	}

	if got := kcpFrameConfig(FrameOptions{LengthFieldSize: 2, ByteOrder: network.LittleEndian}); got.ByteOrder != FrameByteOrderLittle || got.LengthFieldSize != 2 {
		t.Fatalf("little frame config = %+v", got)
	}
}
