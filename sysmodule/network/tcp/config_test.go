package tcp

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

// TestDefaultConfigsConvertToValidOptions 验证 Server 和托管 Client 的默认配置无需补字段即可使用，
// 并检查 Client 的单连接配置不会携带没有意义的 Server 总预算。
func TestDefaultConfigsConvertToValidOptions(t *testing.T) {
	handler := network.HandlerFuncs{}
	serverConfig := DefaultServerConfig()
	serverOptions, err := serverConfig.Options(handler)
	if err != nil {
		t.Fatalf("ServerConfig.Options() error = %v", err)
	}
	if serverOptions.Frame.ByteOrder != network.BigEndian ||
		serverOptions.Network.MaxSessions != network.DefaultMaxSessions {
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
	if clientOptions.Dial.DialTimeout != 10*time.Second || clientOptions.Dial.Network.MaxSessions != 1 ||
		clientOptions.Dial.Network.ReceivePendingTotalSize != clientOptions.Dial.Network.ReceivePendingSize ||
		clientOptions.Dial.Network.SendQueueTotalSize != clientOptions.Dial.Network.SendQueueSize {
		t.Fatalf("client dial options = %+v", clientOptions.Dial)
	}
}

// TestConfigStrictDecodeAndConvert 覆盖带单位时间、容量、Little Endian 和重连字段从 YAML
// 严格解码到运行期 Options 的完整冷路径。
func TestConfigStrictDecodeAndConvert(t *testing.T) {
	directory := t.TempDir()
	if err := os.WriteFile(filepath.Join(directory, "tcp.yaml"), []byte(`
address: "127.0.0.1:29090"
dial_timeout: 3s
frame: {length_field_size: 2, byte_order: little}
keep_alive: 45s
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
	if configured.Address != "127.0.0.1:29090" || options.Dial.Frame.ByteOrder != network.LittleEndian ||
		options.Dial.Frame.LengthFieldSize != 2 || options.Dial.DialTimeout != 3*time.Second ||
		options.Dial.Network.MaxMessageSize != 32*1024 || options.Dial.Network.ReadIdleTimeout != 2*time.Minute {
		t.Fatalf("dial options = %+v", options.Dial)
	}
	if !options.Reconnect.Enabled || options.Reconnect.MaxAttempts != 6 ||
		options.Reconnect.InitialDelay != 100*time.Millisecond || options.Reconnect.Jitter != 0.1 {
		t.Fatalf("reconnect options = %+v", options.Reconnect)
	}
}

// TestConfigRejectsInvalidValues 验证配置字符串和运行边界最终复用 TCP Options 的统一校验。
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

	client := DefaultClientConfig()
	client.Address = ""
	if _, err := client.Options(handler); err == nil {
		t.Fatal("empty client address unexpectedly accepted")
	}

	client = DefaultClientConfig()
	client.Frame.ByteOrder = "native"
	if _, err := client.Options(handler); !errs.IsCode(err, errs.CodeInvalidConfig) {
		t.Fatalf("client byte order error = %v", err)
	}

	client = DefaultClientConfig()
	client.DialTimeout = 0
	if _, err := client.Options(handler); !errs.IsCode(err, errs.CodeInvalidConfig) {
		t.Fatalf("zero client dial timeout error = %v", err)
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

	// 配置反向映射必须保留 Little Endian，用于默认值生成和文档工具读取。
	if got := frameConfig(FrameOptions{LengthFieldSize: 2, ByteOrder: network.LittleEndian}); got.ByteOrder != FrameByteOrderLittle || got.LengthFieldSize != 2 {
		t.Fatalf("little frame config = %+v", got)
	}
}
