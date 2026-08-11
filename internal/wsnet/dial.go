package wsnet

import (
	"context"
	"errors"
	"strings"

	gorillaws "github.com/gorilla/websocket"
)

// Dial 执行一次 WebSocket 握手；成功返回后 Conn 已接管连接生命周期。
func Dial(
	ctx context.Context,
	url string,
	options DialOptions,
	handler Handler,
) (*Conn, error) {
	if ctx == nil {
		return nil, invalidArgument("wsnet: Dial Context 不能为空")
	}
	if strings.TrimSpace(url) == "" {
		return nil, invalidArgument("wsnet: Dial URL 不能为空")
	}
	if handler == nil {
		return nil, invalidArgument("wsnet: Dial Handler 不能为空")
	}
	if options.HandshakeTimeout <= 0 {
		return nil, invalidConfig("wsnet: HandshakeTimeout 必须大于零")
	}
	if err := validateConnectionOptions(options.Connection); err != nil {
		return nil, err
	}

	dialer := gorillaws.Dialer{
		HandshakeTimeout: options.HandshakeTimeout,
		Subprotocols:     append([]string(nil), options.Subprotocols...),
		TLSClientConfig:  cloneTLSConfig(options.TLSConfig),
	}
	raw, response, err := dialer.DialContext(ctx, url, cloneHeader(options.Header))
	if response != nil && response.Body != nil {
		_ = response.Body.Close()
	}
	if err != nil {
		if ctx.Err() != nil {
			return nil, contextError(ctx.Err())
		}
		if errors.Is(err, gorillaws.ErrBadHandshake) {
			return nil, transportUnavailable(err)
		}
		return nil, normalizeIOError(err)
	}
	conn := newConn(raw, options.Connection, handler, nil)
	conn.start()
	return conn, nil
}
