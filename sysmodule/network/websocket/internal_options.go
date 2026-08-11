package websocket

import (
	"github.com/duanhf2012/origin/v3/internal/wsnet"
	"github.com/duanhf2012/origin/v3/sysmodule/network/internal/core"
)

func connectionOptions(runtime *core.Runtime, options DialOptions) wsnet.ConnectionOptions {
	messageType := wsnet.BinaryMessage
	if options.MessageType == TextMessage {
		messageType = wsnet.TextMessage
	}
	return wsnet.ConnectionOptions{
		Pool:              runtime.Pool(),
		Logger:            runtime.Logger(),
		MessageType:       messageType,
		MaxMessageSize:    options.Network.MaxMessageSize,
		SendQueueMessages: options.Network.SendQueueMessages,
		SendQueueBytes:    options.Network.SendQueueSize,
		SendBudget:        runtime.SendBudget(),
		ReadTimeout:       options.Network.ReadIdleTimeout,
		WriteTimeout:      options.Network.WriteTimeout,
		SlowClientTimeout: options.Network.SlowClientTimeout,
		PingInterval:      options.PingInterval,
		PongTimeout:       options.PongTimeout,
	}
}

func internalDialOptions(runtime *core.Runtime, options DialOptions) wsnet.DialOptions {
	return wsnet.DialOptions{
		HandshakeTimeout: options.HandshakeTimeout,
		Header:           options.Header,
		Subprotocols:     options.Subprotocols,
		TLSConfig:        options.TLSConfig,
		Connection:       connectionOptions(runtime, options),
	}
}
