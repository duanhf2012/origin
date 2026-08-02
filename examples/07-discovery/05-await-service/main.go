// 本示例展示等待、精确查询和列表查询三个服务发现 API。
package main

import (
	"context"
	"fmt"
	"time"

	"github.com/duanhf2012/origin/v3/application"
	"github.com/duanhf2012/origin/v3/discovery/provider"
	"github.com/duanhf2012/origin/v3/service"
)

// app 是当前示例唯一的 Application。
var app = application.New()

// GatewayService 在启动阶段等待远端 PlayerService 进入发现目录。
type GatewayService struct{ service.Service }

// OnStart 使用生命周期 Context，使停止或超时可以取消等待。
func (target *GatewayService) OnStart(ctx context.Context) error {
	// AwaitService 等待任意远端 Node 上出现指定 Service。
	if err := target.AwaitService(ctx, "PlayerService"); err != nil {
		return err
	}
	// AwaitNodeService 进一步要求具体 NodeID 和 ServiceName 同时匹配。
	if err := target.AwaitNodeService(ctx, "player-1", "PlayerService"); err != nil {
		return err
	}

	// FindDiscoveredService 用于已知 NodeID 的精确只读查询。
	instance, ok := target.FindDiscoveredService("player-1", "PlayerService")
	if !ok {
		return fmt.Errorf("player-1:PlayerService disappeared after await")
	}
	// ListDiscoveredServices 返回当前可见的全部同名远端实例快照。
	instances := target.ListDiscoveredServices("PlayerService")
	target.Logger().Info(fmt.Sprintf(
		"discovery target is ready: node=%s candidates=%d",
		instance.NodeID,
		len(instances),
	))
	return nil
}

// staticProvider 发布一个远端服务，使示例无需外部基础设施也能直接运行。
type staticProvider struct{ host provider.Host }

// Start 先设置 TTL，再提交权威快照，最后报告 Provider Ready。
func (target *staticProvider) Start(context.Context) error {
	if err := target.host.SetTTL(5 * time.Second); err != nil {
		return err
	}
	if err := target.host.ReplaceSnapshot(provider.Snapshot{Nodes: []provider.Node{{
		NodeID: "player-1", SessionID: 1, Transport: provider.TransportNone,
		Services: []provider.Service{{ServiceName: "PlayerService", State: provider.ServiceStateRunning}},
	}}}); err != nil {
		return err
	}
	target.host.Report(provider.Report{State: provider.StateReady})
	return nil
}

// Publish、Withdraw、Close 是 Provider SPI 的本地发布和生命周期入口。
func (*staticProvider) Publish(context.Context, provider.Node) error { return nil }
func (*staticProvider) Withdraw(context.Context) error               { return nil }
func (*staticProvider) Close(context.Context) error                  { return nil }

// init 同时登记业务 Service 和名为 await-demo 的自定义 Provider Factory。
func init() {
	app.Setup(&GatewayService{})
	if err := app.RegisterDiscoveryProvider("await-demo", func(ctx provider.Context) (provider.Provider, error) {
		return &staticProvider{host: ctx.Host}, nil
	}); err != nil {
		panic(err)
	}
}

// main 启动 Application。
func main() { app.Start() }
