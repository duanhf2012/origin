package wsnet

import (
	"crypto/tls"
	"net/http"
	"time"

	"github.com/duanhf2012/origin/v3/internal/bufferpool"
	"github.com/duanhf2012/origin/v3/internal/bytebudget"
	originlog "github.com/duanhf2012/origin/v3/log"
)

const (
	// BinaryMessage 和 TextMessage 是允许作为业务逻辑消息的数据类型。
	BinaryMessage MessageType = iota + 1
	TextMessage
)

// MessageType 固化一条连接允许收发的 WebSocket Data Message 类型。
type MessageType uint8

// Handler 接收一条内部 WebSocket 连接的有序生命周期和完整消息。
type Handler interface {
	OnOpen(*Conn)
	OnMessage(*Conn, *bufferpool.Buffer) error
	OnClose(*Conn, error)
}

// WritableHandler 是发送队列跨越高低水位时的可选通知。
type WritableHandler interface {
	OnWritableChanged(*Conn, bool)
}

// ConnectionOptions 配置一条已经完成握手的 WebSocket 连接。
type ConnectionOptions struct {
	Pool              *bufferpool.Pool
	Logger            originlog.Logger
	MessageType       MessageType
	MaxMessageSize    int
	SendQueueMessages int
	SendQueueBytes    int64
	SendBudget        *bytebudget.Budget
	ReadTimeout       time.Duration
	WriteTimeout      time.Duration
	SlowClientTimeout time.Duration
	PingInterval      time.Duration
	PongTimeout       time.Duration
}

// ListenOptions 配置 HTTP Upgrade Listener 及其连接准入。
type ListenOptions struct {
	MaxConnections   int
	Path             string
	HandshakeTimeout time.Duration
	CheckOrigin      func(*http.Request) bool
	Subprotocols     []string
	ResponseHeader   http.Header
	TLSConfig        *tls.Config
	Connection       ConnectionOptions
}

// DialOptions 配置一次客户端 WebSocket 握手。
type DialOptions struct {
	HandshakeTimeout time.Duration
	Header           http.Header
	Subprotocols     []string
	TLSConfig        *tls.Config
	Connection       ConnectionOptions
}

func validateConnectionOptions(options ConnectionOptions) error {
	if options.Pool == nil || options.SendBudget == nil {
		return invalidConfig("wsnet: Pool 和 SendBudget 不能为空")
	}
	if options.MessageType != BinaryMessage && options.MessageType != TextMessage {
		return invalidConfig("wsnet: MessageType 无效")
	}
	if options.MaxMessageSize <= 0 || options.SendQueueMessages <= 0 ||
		options.SendQueueBytes < int64(bufferpool.RetainedCapacity(options.MaxMessageSize)) {
		return invalidConfig("wsnet: 消息或发送队列容量无效")
	}
	if options.ReadTimeout < 0 || options.WriteTimeout <= 0 ||
		options.SlowClientTimeout <= 0 {
		return invalidConfig("wsnet: 读写或慢连接超时无效")
	}
	if options.PingInterval < 0 || options.PongTimeout < 0 ||
		(options.PingInterval == 0) != (options.PongTimeout == 0) ||
		(options.PingInterval > 0 && options.PongTimeout <= options.PingInterval) {
		return invalidConfig("wsnet: Ping/Pong 配置无效")
	}
	return nil
}

func cloneHeader(header http.Header) http.Header {
	if header == nil {
		return nil
	}
	return header.Clone()
}

func cloneTLSConfig(config *tls.Config) *tls.Config {
	if config == nil {
		return nil
	}
	return config.Clone()
}
