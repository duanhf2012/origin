package tcp

import (
	"context"

	"github.com/duanhf2012/origin/v3/internal/tcpnet"
	"github.com/duanhf2012/origin/v3/sysmodule/network"
	"github.com/duanhf2012/origin/v3/sysmodule/network/internal/core"
)

// dialConnection 对每次建连同时施加生命周期 Context 和配置上限。
//
// Context 已经包含更早 Deadline 时 context.WithTimeout 会自然选择更早者；取消函数只释放
// 本次拨号的 Timer，不影响成功后由 Conn 自己持有的读写生命周期。
func dialConnection(
	ctx context.Context,
	address string,
	runtime *core.Runtime,
	options DialOptions,
	handler tcpnet.Handler,
) (*tcpnet.Conn, error) {
	dialContext, cancel := context.WithTimeout(ctx, options.DialTimeout)
	defer cancel()
	return tcpnet.Dial(
		dialContext,
		address,
		connectionOptions(runtime, options),
		handler,
	)
}

func connectionOptions(
	runtime *core.Runtime,
	options DialOptions,
) tcpnet.ConnectionOptions {
	return tcpnet.ConnectionOptions{
		Pool:   runtime.Pool(),
		Logger: runtime.Logger(),
		Frame: tcpnet.FrameOptions{
			LengthFieldSize: options.Frame.LengthFieldSize,
			ByteOrder:       byteOrder(options.Frame.ByteOrder),
		},
		MaxMessageSize:    options.Network.MaxMessageSize,
		SendQueueFrames:   options.Network.SendQueueMessages,
		SendQueueBytes:    options.Network.SendQueueSize,
		SendBudget:        runtime.SendBudget(),
		ReadTimeout:       options.Network.ReadIdleTimeout,
		WriteTimeout:      options.Network.WriteTimeout,
		SlowClientTimeout: options.Network.SlowClientTimeout,
		KeepAlive:         options.KeepAlive,
	}
}

func byteOrder(order network.ByteOrder) tcpnet.ByteOrder {
	if order == network.LittleEndian {
		return tcpnet.LittleEndian
	}
	return tcpnet.BigEndian
}
