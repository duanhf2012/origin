package tcp

import (
	"github.com/duanhf2012/origin/v3/internal/tcpnet"
	"github.com/duanhf2012/origin/v3/sysmodule/network"
	"github.com/duanhf2012/origin/v3/sysmodule/network/internal/core"
)

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
