// 本示例重点展示如何在业务 Service 中注册并处理服务发现监听器。
package main

import (
	"context"
	"fmt"
	"time"

	"github.com/duanhf2012/origin/v3/application"
	"github.com/duanhf2012/origin/v3/discovery"
	"github.com/duanhf2012/origin/v3/discovery/provider"
	"github.com/duanhf2012/origin/v3/errs"
	"github.com/duanhf2012/origin/v3/service"
)

// app 是当前示例唯一的 Application。
var app = application.New()

// DiscoveryWatcherService 是教程中真正注册监听器的业务 Service。
//
// 监听器放在示例自身，而不是隐藏在公共辅助包中，便于直接复制到业务项目。
type DiscoveryWatcherService struct{ service.Service }

// OnInit 在 Service 初始化阶段把 Service 自身注册为发现事件监听器。
func (target *DiscoveryWatcherService) OnInit() error {
	// 监听器归属于当前 Service；框架会在进入 OnStop 前自动移除它。
	_, err := target.AddDiscoveryListener(target)
	return err
}

// OnDiscovered 处理远端 Service 首次出现或新会话重新出现。
func (target *DiscoveryWatcherService) OnDiscovered(_ context.Context, event discovery.Event) {
	target.Logger().Info(fmt.Sprintf(
		"discovered node=%s services=%v", event.NodeID, event.Services,
	))
}

// OnStateChanged 处理远端 Service 的 Running/Retired 状态变化。
func (target *DiscoveryWatcherService) OnStateChanged(_ context.Context, event discovery.Event) {
	target.Logger().Info(fmt.Sprintf(
		"state changed node=%s services=%v", event.NodeID, event.Services,
	))
}

// OnLost 处理已经发现的远端 Service 从权威快照中消失。
func (target *DiscoveryWatcherService) OnLost(_ context.Context, event discovery.Event) {
	target.Logger().Info(fmt.Sprintf(
		"lost node=%s services=%v", event.NodeID, event.Services,
	))
}

// disappearingProvider 先发布一个权威远端快照，再提交空快照，演示 Lost 是立即状态事实。
type disappearingProvider struct {
	host   provider.Host
	cancel context.CancelFunc
	done   chan struct{}
}

func (target *disappearingProvider) Start(context.Context) error {
	// Provider 必须先设置 TTL，Host 才接受权威快照。
	if err := target.host.SetTTL(3 * time.Second); err != nil {
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
	// Provider 自己管理异步 goroutine，并在 Close 中取消、等待它退出。
	runCtx, cancel := context.WithCancel(context.Background())
	target.cancel = cancel
	target.done = make(chan struct{})
	go func() {
		defer close(target.done)
		timer := time.NewTimer(500 * time.Millisecond)
		defer timer.Stop()
		select {
		case <-runCtx.Done():
			return
		case <-timer.C:
		}
		// 空权威快照立即产生 Lost；不吞掉 Host 拒绝或关闭错误。
		if err := target.host.ReplaceSnapshot(provider.Snapshot{}); err != nil {
			target.host.Report(provider.Report{
				State:     provider.StateRecovering,
				ErrorCode: errs.CodeOf(err),
			})
			return
		}
		target.host.Report(provider.Report{
			State:      provider.StateRecovering,
			Reconnects: 1,
		})
	}()
	return nil
}

// Publish 和 Withdraw 满足完整 Provider SPI；示例不发布真实本地地址。
func (*disappearingProvider) Publish(context.Context, provider.Node) error { return nil }
func (*disappearingProvider) Withdraw(context.Context) error               { return nil }

func (target *disappearingProvider) Close(ctx context.Context) error {
	if target.cancel != nil {
		target.cancel()
	}
	if target.done == nil {
		return nil
	}
	select {
	case <-target.done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// init 登记监听 Service 和名为 demo 的 Provider Factory。
func init() {
	app.Setup(&DiscoveryWatcherService{})
	if err := app.RegisterDiscoveryProvider("demo", func(ctx provider.Context) (provider.Provider, error) {
		return &disappearingProvider{host: ctx.Host}, nil
	}); err != nil {
		panic(err)
	}
}

// main 启动 Application。
func main() { app.Start() }
