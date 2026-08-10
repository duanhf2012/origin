package tcpnet

import (
	"context"
	"errors"
	"net"
	"strings"

	originlog "github.com/duanhf2012/origin/v3/log"
)

// Dial 使用 Context 对一个 TCP 地址执行单次连接尝试。
//
// Dial 不重试，返回成功后 Conn 已经启动读写循环；后续重连由 Node 连接管理器组合。
func Dial(
	ctx context.Context,
	address string,
	options ConnectionOptions,
	handler Handler,
) (*Conn, error) {
	// 所有参数和 Options 必须在创建 socket 前完成校验。
	if ctx == nil {
		return nil, invalidArgument("tcpnet: Dial Context 不能为空")
	}
	if strings.TrimSpace(address) == "" {
		return nil, invalidArgument("tcpnet: Dial 地址不能为空")
	}
	if handler == nil {
		return nil, invalidArgument("tcpnet: Dial Handler 不能为空")
	}
	if err := validateConnectionOptions(options); err != nil {
		return nil, err
	}

	// 显式关闭 Dialer 自己的默认 KeepAlive，连接成功后再统一应用 Origin Options。
	dialer := net.Dialer{KeepAlive: -1}
	raw, err := dialer.DialContext(ctx, "tcp", address)
	if err != nil {
		if ctx.Err() != nil {
			return nil, contextError(ctx.Err())
		}
		options.Logger.Error(
			"TCP Dial 失败",
			originlog.String("address", address),
			originlog.Err(err),
		)
		return nil, transportUnavailable(err)
	}

	// socket 参数任一设置失败都按部分初始化逆序关闭，不启动 goroutine。
	if err := configureTCP(raw, options); err != nil {
		_ = raw.Close()
		options.Logger.Error(
			"配置 TCP 连接失败",
			originlog.String("address", address),
			originlog.Err(err),
		)
		return nil, err
	}

	// 最后创建并启动连接对象，成功返回即表示生命周期已经由 Conn 接管。
	conn := newConn(raw, options, handler, nil)
	conn.start()
	return conn, nil
}

// configureTCP 把已经确认的低延迟和存活参数应用到真实 TCP socket。
func configureTCP(raw net.Conn, options ConnectionOptions) error {
	// tcp 网络的 Dial/Accept 正常都返回 *net.TCPConn；其他实现表示内部调用错误。
	tcpConn, ok := raw.(*net.TCPConn)
	if !ok {
		return transportUnavailable(errors.New("tcpnet: net.Conn 不是 TCPConn"))
	}

	// TCP_NODELAY 固定开启，避免小 RPC 等待 Nagle 合并。
	if err := tcpConn.SetNoDelay(true); err != nil {
		return transportUnavailable(err)
	}

	// KeepAlive=0 明确关闭；非零值同时开启并设置已确认周期。
	if options.KeepAlive == 0 {
		if err := tcpConn.SetKeepAlive(false); err != nil {
			return transportUnavailable(err)
		}
		return nil
	}
	if err := tcpConn.SetKeepAlive(true); err != nil {
		return transportUnavailable(err)
	}
	if err := tcpConn.SetKeepAlivePeriod(options.KeepAlive); err != nil {
		return transportUnavailable(err)
	}
	return nil
}
