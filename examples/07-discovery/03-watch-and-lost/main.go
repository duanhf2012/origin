// 本示例用可控内存 Provider 展示 discovered 后立即产生 Lost 的状态顺序。
package main

import (
	"context"
	"time"

	"github.com/duanhf2012/origin/v3/application"
	"github.com/duanhf2012/origin/v3/discovery/provider"
	"github.com/duanhf2012/origin/v3/examples/_support/tutorialwatcher"
)

// app 是当前示例唯一的 Application。
var app = application.New()

// disappearingProvider 先发布一个权威远端快照，再提交空快照，演示 Lost 是立即状态事实。
type disappearingProvider struct{ host provider.Host }

func (target *disappearingProvider) Start(context.Context) error {
	// Provider 必须先设置 TTL，Host 才接受权威快照。
	if err := target.host.SetTTL(time.Second); err != nil {
		return err
	}
	// 首个快照发布一个 Running 的远端 PlayerService。
	if err := target.host.ReplaceSnapshot(provider.Snapshot{Nodes: []provider.Node{{
		NodeID: "player-1", SessionID: 1, Transport: provider.TransportNone,
		Services: []provider.Service{{ServiceName: "PlayerService", State: provider.ServiceStateRunning}},
	}}}); err != nil {
		return err
	}
	target.host.Report(provider.Report{State: provider.StateReady})
	// Provider 自己管理网络/重连 goroutine；空快照会立即产生 Lost。
	go func() {
		time.Sleep(500 * time.Millisecond)
		_ = target.host.ReplaceSnapshot(provider.Snapshot{})
		target.host.Report(provider.Report{State: provider.StateRecovering, Reconnects: 1})
	}()
	return nil
}

// 这三个空实现满足完整 Provider SPI；示例不发布真实本地地址。
func (*disappearingProvider) Publish(context.Context, provider.Node) error { return nil }
func (*disappearingProvider) Withdraw(context.Context) error               { return nil }
func (*disappearingProvider) Close(context.Context) error                  { return nil }

// init 登记监听 Service 和名为 demo 的 Provider Factory。
func init() {
	app.Setup(&tutorialwatcher.Service{})
	if err := app.RegisterDiscoveryProvider("demo", func(ctx provider.Context) (provider.Provider, error) {
		return &disappearingProvider{host: ctx.Host}, nil
	}); err != nil {
		panic(err)
	}
}

// main 启动 Application。
func main() { app.Start() }
