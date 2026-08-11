package kcpnet

import (
	"time"

	kcplib "github.com/xtaci/kcp-go/v5"
)

// configureSession 在 goroutine 启动前一次性应用已经校验的 KCP 参数。
func configureSession(raw *kcplib.UDPSession, options ProtocolOptions) error {
	// Origin 在 KCP 的可靠有序字节流上增加自己的长度帧，因此 Stream Mode 固定开启。
	// kcp-go 把该方法标为 Deprecated 只是因为其推荐 Message Mode；当前固定调用仍是库的
	// 唯一公开设置入口，升级依赖时必须通过帧互通测试复核。
	raw.SetStreamMode(true)
	if !raw.SetMtu(options.MTU) {
		return invalidConfig("kcpnet: KCP Session 拒绝 MTU")
	}
	raw.SetWindowSize(options.SendWindow, options.ReceiveWindow)
	nodelay := 0
	if options.NoDelay.Enabled {
		nodelay = 1
	}
	disableCongestion := 0
	if options.NoDelay.DisableCongestionControl {
		disableCongestion = 1
	}
	raw.SetNoDelay(
		nodelay,
		int(options.NoDelay.Interval/time.Millisecond),
		options.NoDelay.FastResend,
		disableCongestion,
	)
	raw.SetACKNoDelay(options.ACKNoDelay)
	raw.SetWriteDelay(options.WriteDelay)
	return nil
}
