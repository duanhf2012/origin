package kcp

import (
	"github.com/duanhf2012/origin/v3/internal/kcpnet"
	"github.com/duanhf2012/origin/v3/internal/lengthframe"
	"github.com/duanhf2012/origin/v3/sysmodule/network"
	"github.com/duanhf2012/origin/v3/sysmodule/network/internal/core"
)

func connectionOptions(
	runtime *core.Runtime,
	options DialOptions,
) kcpnet.ConnectionOptions {
	return kcpnet.ConnectionOptions{
		Pool:   runtime.Pool(),
		Logger: runtime.Logger(),
		Frame: kcpnet.FrameOptions{
			LengthFieldSize: options.Frame.LengthFieldSize,
			ByteOrder:       byteOrder(options.Frame.ByteOrder),
		},
		Protocol: kcpnet.ProtocolOptions{
			MTU:           options.MTU,
			SendWindow:    options.SendWindow,
			ReceiveWindow: options.ReceiveWindow,
			NoDelay: kcpnet.NoDelayOptions{
				Enabled:                  options.NoDelay.Enabled,
				Interval:                 options.NoDelay.Interval,
				FastResend:               options.NoDelay.FastResend,
				DisableCongestionControl: options.NoDelay.DisableCongestionControl,
			},
			ACKNoDelay: options.ACKNoDelay,
			WriteDelay: options.WriteDelay,
		},
		MaxMessageSize:    options.Network.MaxMessageSize,
		SendQueueMessages: options.Network.SendQueueMessages,
		SendQueueBytes:    options.Network.SendQueueSize,
		SendBudget:        runtime.SendBudget(),
		ReadTimeout:       options.Network.ReadIdleTimeout,
		WriteTimeout:      options.Network.WriteTimeout,
		SlowClientTimeout: options.Network.SlowClientTimeout,
	}
}

func internalDialOptions(runtime *core.Runtime, options DialOptions) kcpnet.DialOptions {
	return kcpnet.DialOptions{
		BlockCrypt: options.BlockCrypt,
		FEC: kcpnet.FECOptions{
			DataShards:   options.FEC.DataShards,
			ParityShards: options.FEC.ParityShards,
		},
		DSCP:              options.DSCP,
		SocketReadBuffer:  options.SocketReadBuffer,
		SocketWriteBuffer: options.SocketWriteBuffer,
		Connection:        connectionOptions(runtime, options),
	}
}

func byteOrder(order network.ByteOrder) lengthframe.ByteOrder {
	if order == network.LittleEndian {
		return lengthframe.LittleEndian
	}
	return lengthframe.BigEndian
}
