// Package websocket 提供由 Origin Service 托管的 WebSocket Server、Client 和单次 Dialer。
package websocket

import (
	"context"
	"crypto/tls"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/duanhf2012/origin/v3/errs"
	"github.com/duanhf2012/origin/v3/sysmodule/network"
)

const (
	defaultPath             = "/ws"
	defaultHandshakeTimeout = 10 * time.Second
	defaultPingInterval     = 30 * time.Second
	defaultPongTimeout      = 60 * time.Second
)

// MessageType 固化 WebSocket 连接收发的 Data Message 类型。
type MessageType uint8

const (
	// BinaryMessage 适合 Raw、Protobuf 和其他二进制协议，也是默认值。
	BinaryMessage MessageType = iota + 1
	// TextMessage 适合浏览器直接收发 JSON；发送内容必须是有效 UTF-8。
	TextMessage
)

// ServerOptions 配置 WebSocket Server 的公共网络语义和 Upgrade 专属参数。
type ServerOptions struct {
	// Network 保存 Handler、容量、超时和背压等三个传输真正共有的语义。
	Network network.EndpointOptions
	// Path 是执行 WebSocket Upgrade 的 HTTP 路由，必须以斜杠开头。
	Path string
	// MessageType 固定当前端点收发的 Binary 或 Text Data Message 类型。
	MessageType MessageType
	// HandshakeTimeout 是读取 HTTP Upgrade 请求头和完成握手的最长时间。
	HandshakeTimeout time.Duration
	// PingInterval 是发送 WebSocket 协议 Ping 控制帧的间隔；与 PongTimeout 同为零时关闭。
	PingInterval time.Duration
	// PongTimeout 是等待协议 Pong 的上限，启用时必须大于 PingInterval。
	PongTimeout time.Duration
	// CheckOrigin 决定 Upgrade 是否接受请求 Origin；nil 使用 Gorilla 的安全同源策略。
	CheckOrigin func(*http.Request) bool
	// Subprotocols 按顺序列出服务端允许协商的应用子协议。
	Subprotocols []string
	// ResponseHeader 是 Upgrade 成功响应附带的非保留 Header。
	ResponseHeader http.Header
	// TLSConfig 非 nil 时启用 WSS；构造器会克隆配置，且服务端必须提供证书。
	TLSConfig *tls.Config
}

// DefaultServerOptions 返回安全同源、Binary Message 和有界心跳的默认配置。
func DefaultServerOptions(handler network.Handler) ServerOptions {
	return ServerOptions{
		Network:          network.DefaultEndpointOptions(handler),
		Path:             defaultPath,
		MessageType:      BinaryMessage,
		HandshakeTimeout: defaultHandshakeTimeout,
		PingInterval:     defaultPingInterval,
		PongTimeout:      defaultPongTimeout,
	}
}

// DialOptions 配置一次 WebSocket 拨号及其连接语义。
type DialOptions struct {
	// Network 保存单 Session 的 Handler、容量、超时和背压语义；MaxSessions 必须为 1。
	Network network.EndpointOptions
	// MessageType 固定当前连接收发的 Binary 或 Text Data Message 类型。
	MessageType MessageType
	// HandshakeTimeout 限制 DNS、TCP、TLS 和 HTTP Upgrade 的整体握手时间。
	HandshakeTimeout time.Duration
	// PingInterval 是发送 WebSocket 协议 Ping 控制帧的间隔；与 PongTimeout 同为零时关闭。
	PingInterval time.Duration
	// PongTimeout 是等待协议 Pong 的上限，启用时必须大于 PingInterval。
	PongTimeout time.Duration
	// Header 是 Upgrade 请求附带的非保留 Header，可用于项目自己的鉴权信息。
	Header http.Header
	// Subprotocols 按顺序列出客户端提议的应用子协议。
	Subprotocols []string
	// TLSConfig 配置 wss 客户端；ws URL 不允许同时设置该字段。
	TLSConfig *tls.Config
}

// DefaultDialOptions 返回单 Session、Binary Message 的拨号默认配置。
func DefaultDialOptions(handler network.Handler) DialOptions {
	server := DefaultServerOptions(handler)
	server.Network.MaxSessions = 1
	return DialOptions{
		Network:          server.Network,
		MessageType:      server.MessageType,
		HandshakeTimeout: server.HandshakeTimeout,
		PingInterval:     server.PingInterval,
		PongTimeout:      server.PongTimeout,
	}
}

// ReconnectOptions 配置 Client 的有界指数退避。
type ReconnectOptions struct {
	// Enabled 控制初始连接失败或活动连接关闭后是否自动重试。
	Enabled bool
	// MaxAttempts 是每轮连续失败允许执行的最大重试次数。
	MaxAttempts int
	// InitialDelay 是第一次重试前的等待时间。
	InitialDelay time.Duration
	// MaxDelay 是指数退避单次等待时间的上限。
	MaxDelay time.Duration
	// Jitter 是退避随机抖动比例，范围为 0 到 1。
	Jitter float64
}

// ClientOptions 配置托管 WebSocket Client。
type ClientOptions struct {
	// Dial 配置每次 WebSocket 握手和连接建立后的 Session 语义。
	Dial DialOptions
	// Reconnect 配置 Client 唯一重连 Worker 的有界退避。
	Reconnect ReconnectOptions
	// StateChange 在所属 Service 串行上下文中接收不可变状态快照；nil 表示不通知。
	StateChange func(context.Context, network.ClientStateSnapshot)
}

// DefaultClientOptions 返回默认不自动重连的托管 Client 配置。
func DefaultClientOptions(handler network.Handler) ClientOptions {
	return ClientOptions{
		Dial: DefaultDialOptions(handler),
		Reconnect: ReconnectOptions{
			Enabled:      false,
			MaxAttempts:  10,
			InitialDelay: 200 * time.Millisecond,
			MaxDelay:     5 * time.Second,
			Jitter:       0.2,
		},
	}
}

func validateAddress(address string) error {
	if strings.TrimSpace(address) == "" {
		return errs.NewMessage(errs.CodeInvalidArgument, "websocket: 监听地址不能为空")
	}
	return nil
}

func validateURL(rawURL string, tlsConfig *tls.Config) error {
	parsed, err := url.Parse(rawURL)
	if err != nil || parsed.Host == "" || (parsed.Scheme != "ws" && parsed.Scheme != "wss") {
		return errs.NewMessage(errs.CodeInvalidArgument, "websocket: URL 必须使用 ws:// 或 wss://")
	}
	if parsed.User != nil {
		return errs.NewMessage(errs.CodeInvalidArgument, "websocket: URL 不能包含用户凭证")
	}
	if parsed.Scheme == "ws" && tlsConfig != nil {
		return errs.NewMessage(errs.CodeInvalidConfig, "websocket: ws:// 不能配置 TLSConfig")
	}
	return nil
}

func validateServerOptions(options ServerOptions) error {
	if err := options.Network.Validate(); err != nil {
		return err
	}
	if options.Path == "" || options.Path[0] != '/' || strings.ContainsAny(options.Path, "?#") {
		return errs.NewMessage(errs.CodeInvalidConfig, "websocket.path 必须是以 / 开头且不含查询或片段的路径")
	}
	if err := validateTransportOptions(
		options.MessageType,
		options.HandshakeTimeout,
		options.PingInterval,
		options.PongTimeout,
	); err != nil {
		return err
	}
	if err := validateSubprotocols(options.Subprotocols); err != nil {
		return err
	}
	if options.TLSConfig != nil && len(options.TLSConfig.Certificates) == 0 &&
		options.TLSConfig.GetCertificate == nil && options.TLSConfig.GetConfigForClient == nil {
		return errs.NewMessage(errs.CodeInvalidConfig, "websocket.tls_config 缺少服务端证书")
	}
	for _, header := range []string{
		"Connection", "Upgrade", "Sec-Websocket-Accept",
		"Sec-Websocket-Extensions", "Sec-Websocket-Protocol",
	} {
		if hasHeader(options.ResponseHeader, header) {
			return errs.NewMessage(errs.CodeInvalidConfig, fmt.Sprintf(
				"websocket.response_header 不能设置保留字段 %s",
				header,
			))
		}
	}
	return nil
}

func validateDialOptions(rawURL string, options DialOptions) error {
	if err := validateURL(rawURL, options.TLSConfig); err != nil {
		return err
	}
	if options.Network.MaxSessions != 1 {
		return errs.NewMessage(errs.CodeInvalidConfig, "websocket Dial/Client 的 max_sessions 必须为 1")
	}
	if err := options.Network.Validate(); err != nil {
		return err
	}
	if err := validateTransportOptions(
		options.MessageType,
		options.HandshakeTimeout,
		options.PingInterval,
		options.PongTimeout,
	); err != nil {
		return err
	}
	if err := validateSubprotocols(options.Subprotocols); err != nil {
		return err
	}
	for _, header := range []string{
		"Connection", "Upgrade", "Sec-Websocket-Key", "Sec-Websocket-Version",
		"Sec-Websocket-Extensions", "Sec-Websocket-Protocol",
	} {
		if hasHeader(options.Header, header) {
			return errs.NewMessage(errs.CodeInvalidConfig, fmt.Sprintf(
				"websocket.header 不能设置保留字段 %s",
				header,
			))
		}
	}
	return nil
}

func hasHeader(headers http.Header, target string) bool {
	for header := range headers {
		if strings.EqualFold(header, target) {
			return true
		}
	}
	return false
}

func validateClientOptions(rawURL string, options ClientOptions) error {
	if err := validateDialOptions(rawURL, options.Dial); err != nil {
		return err
	}
	if options.Reconnect.MaxAttempts <= 0 || options.Reconnect.InitialDelay <= 0 ||
		options.Reconnect.MaxDelay < options.Reconnect.InitialDelay ||
		options.Reconnect.Jitter < 0 || options.Reconnect.Jitter > 1 {
		return errs.NewMessage(errs.CodeInvalidConfig, "websocket.reconnect 配置无效")
	}
	return nil
}

func validateTransportOptions(
	messageType MessageType,
	handshakeTimeout time.Duration,
	pingInterval time.Duration,
	pongTimeout time.Duration,
) error {
	if messageType != BinaryMessage && messageType != TextMessage {
		return errs.NewMessage(errs.CodeInvalidConfig, "websocket.message_type 无效")
	}
	if handshakeTimeout <= 0 {
		return errs.NewMessage(errs.CodeInvalidConfig, "websocket.handshake_timeout 必须大于零")
	}
	if pingInterval < 0 || pongTimeout < 0 ||
		(pingInterval == 0) != (pongTimeout == 0) ||
		(pingInterval > 0 && pongTimeout <= pingInterval) {
		return errs.NewMessage(errs.CodeInvalidConfig, "websocket.ping/pong 配置无效")
	}
	return nil
}

func validateSubprotocols(protocols []string) error {
	seen := make(map[string]struct{}, len(protocols))
	for _, protocol := range protocols {
		if !validSubprotocol(protocol) {
			return errs.NewMessage(errs.CodeInvalidConfig, "websocket.subprotocol 包含非法值")
		}
		if _, exists := seen[protocol]; exists {
			return errs.NewMessage(errs.CodeInvalidConfig, "websocket.subprotocol 不能重复")
		}
		seen[protocol] = struct{}{}
	}
	return nil
}

func validSubprotocol(protocol string) bool {
	if protocol == "" {
		return false
	}
	for index := 0; index < len(protocol); index++ {
		value := protocol[index]
		if (value >= 'a' && value <= 'z') || (value >= 'A' && value <= 'Z') ||
			(value >= '0' && value <= '9') || strings.ContainsRune("!#$%&'*+-.^_`|~", rune(value)) {
			continue
		}
		return false
	}
	return true
}

func freezeServerOptions(options ServerOptions) ServerOptions {
	options.Subprotocols = append([]string(nil), options.Subprotocols...)
	options.ResponseHeader = options.ResponseHeader.Clone()
	if options.TLSConfig != nil {
		options.TLSConfig = options.TLSConfig.Clone()
	}
	return options
}

func freezeDialOptions(options DialOptions) DialOptions {
	options.Subprotocols = append([]string(nil), options.Subprotocols...)
	options.Header = options.Header.Clone()
	if options.TLSConfig != nil {
		options.TLSConfig = options.TLSConfig.Clone()
	}
	return options
}
