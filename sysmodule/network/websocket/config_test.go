package websocket

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	originconfig "github.com/duanhf2012/origin/v3/config"
	"github.com/duanhf2012/origin/v3/errs"
	"github.com/duanhf2012/origin/v3/sysmodule/network"
)

// TestDefaultConfigsConvertToValidOptions 验证三类 WebSocket 默认配置可以直接使用，
// 且 Client/Dialer 的固定单连接语义不会暴露冗余总预算。
func TestDefaultConfigsConvertToValidOptions(t *testing.T) {
	handler := network.HandlerFuncs{}
	serverConfig := DefaultServerConfig()
	serverOptions, err := serverConfig.Options(handler)
	if err != nil {
		t.Fatalf("ServerConfig.Options() error = %v", err)
	}
	if serverOptions.Path != "/ws" || serverOptions.MessageType != BinaryMessage ||
		serverOptions.Network.MaxSessions != network.DefaultMaxSessions {
		t.Fatalf("server options = %+v", serverOptions)
	}

	dialerConfig := DefaultDialerConfig()
	dialOptions, err := dialerConfig.Options(handler)
	if err != nil {
		t.Fatalf("DialerConfig.Options() error = %v", err)
	}
	if dialOptions.Network.MaxSessions != 1 ||
		dialOptions.Network.ReceivePendingTotalSize != dialOptions.Network.ReceivePendingSize ||
		dialOptions.Network.SendQueueTotalSize != dialOptions.Network.SendQueueSize {
		t.Fatalf("single-session options = %+v", dialOptions.Network)
	}

	clientOptions, err := DefaultClientConfig().Options(handler)
	if err != nil {
		t.Fatalf("ClientConfig.Options() error = %v", err)
	}
	if clientOptions.Reconnect.Enabled || clientOptions.Reconnect.MaxAttempts != 10 {
		t.Fatalf("client reconnect = %+v", clientOptions.Reconnect)
	}
}

// TestConfigStrictDecodeAndConvert 覆盖 Text Message、协议心跳、子协议、容量和重连字段从
// YAML 严格解码到运行期 Options 的完整冷路径。
func TestConfigStrictDecodeAndConvert(t *testing.T) {
	directory := t.TempDir()
	if err := os.WriteFile(filepath.Join(directory, "websocket.yaml"), []byte(`
url: "wss://example.test/game"
message_type: text
handshake_timeout: 4s
ping_interval: 20s
pong_timeout: 45s
subprotocols: [origin-v1, origin-v2]
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
	if configured.URL != "wss://example.test/game" || options.Dial.MessageType != TextMessage ||
		options.Dial.HandshakeTimeout != 4*time.Second || options.Dial.Network.MaxMessageSize != 32*1024 ||
		options.Dial.Network.ReadIdleTimeout != 2*time.Minute {
		t.Fatalf("dial options = %+v", options.Dial)
	}
	if len(options.Dial.Subprotocols) != 2 || !options.Reconnect.Enabled ||
		options.Reconnect.InitialDelay != 100*time.Millisecond || options.Reconnect.Jitter != 0.1 {
		t.Fatalf("client options = %+v", options)
	}

	// Options 必须取得自己的 Slice，调用方后续修改 Config 不能改变已经生成的运行配置。
	configured.Subprotocols[0] = "changed"
	if options.Dial.Subprotocols[0] != "origin-v1" {
		t.Fatal("Options 与 Config 共享 Subprotocols 所有权")
	}
}

// TestConfigRejectsInvalidValues 验证消息类型、URL、心跳和容量最终复用 WebSocket Options 校验。
func TestConfigRejectsInvalidValues(t *testing.T) {
	handler := network.HandlerFuncs{}

	server := DefaultServerConfig()
	server.Address = ""
	if _, err := server.Options(handler); err == nil {
		t.Fatal("empty server address unexpectedly accepted")
	}

	server = DefaultServerConfig()
	server.MessageType = "json"
	if _, err := server.Options(handler); !errs.IsCode(err, errs.CodeInvalidConfig) {
		t.Fatalf("invalid message type error = %v", err)
	}

	dialer := DefaultDialerConfig()
	dialer.MessageType = "json"
	if _, err := dialer.Options(handler); !errs.IsCode(err, errs.CodeInvalidConfig) {
		t.Fatalf("dial message type error = %v", err)
	}

	dialer = DefaultDialerConfig()
	dialer.URL = "http://example.test/ws"
	if _, err := dialer.Options(handler); err == nil {
		t.Fatal("HTTP URL unexpectedly accepted")
	}

	dialer = DefaultDialerConfig()
	dialer.PongTimeout = dialer.PingInterval
	if _, err := dialer.Options(handler); !errs.IsCode(err, errs.CodeInvalidConfig) {
		t.Fatalf("invalid heartbeat error = %v", err)
	}

	server = DefaultServerConfig()
	server.ReceivePendingSize = originconfig.ByteSize(1)
	if _, err := server.Options(handler); !errs.IsCode(err, errs.CodeInvalidConfig) {
		t.Fatalf("invalid capacity error = %v", err)
	}

	client := DefaultClientConfig()
	client.Reconnect.Jitter = -0.1
	if _, err := client.Options(handler); !errs.IsCode(err, errs.CodeInvalidConfig) {
		t.Fatalf("invalid reconnect error = %v", err)
	}

	// 配置反向映射必须保留 Text 类型，用于默认值生成和文档工具读取。
	if got := configMessageType(TextMessage); got != ConfigMessageTypeText {
		t.Fatalf("text config message type = %q", got)
	}
}
