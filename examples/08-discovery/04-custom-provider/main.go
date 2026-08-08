// 本示例展示业务如何用一个集中 Factory 替换服务发现 Provider。
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

// consulLikeConfig 是自定义 Provider 独占的最小配置结构。
type consulLikeConfig struct {
	Address string `json:"address"`
}

// consulLikeProvider 只展示 Provider SPI；它不连接真实 Consul。
type consulLikeProvider struct {
	host   provider.Host
	config consulLikeConfig
}

func (target *consulLikeProvider) Start(context.Context) error {
	// TTL 和空权威快照必须在报告 Ready 前提交。
	if err := target.host.SetTTL(5 * time.Second); err != nil {
		return err
	}
	if err := target.host.ReplaceSnapshot(provider.Snapshot{}); err != nil {
		return err
	}
	target.host.Report(provider.Report{State: provider.StateReady})
	return nil
}

// Publish/Withdraw/Close 是真实 Consul 适配器需要实现的生命周期边界。
func (*consulLikeProvider) Publish(context.Context, provider.Node) error { return nil }
func (*consulLikeProvider) Withdraw(context.Context) error               { return nil }
func (*consulLikeProvider) Close(context.Context) error                  { return nil }

// AppService 证明业务 Service 不需要导入任何 Consul 客户端类型。
type AppService struct{ service.Service }

func (target *AppService) OnStart(context.Context) error {
	target.Logger().Info("custom Provider is ready; inspect main.go for the small SPI surface")
	return nil
}

// init 先登记业务 Service，再以稳定名称注册唯一 Provider Factory。
func init() {
	app.Setup(&AppService{})
	if err := app.RegisterDiscoveryProvider("consul", func(ctx provider.Context) (provider.Provider, error) {
		// Factory 统一解析和校验 Provider 配置，避免配置逻辑散落到业务层。
		var config consulLikeConfig
		if err := ctx.Config.Decode(&config); err != nil {
			return nil, err
		}
		if config.Address == "" {
			return nil, fmt.Errorf("consul address is required")
		}
		return &consulLikeProvider{host: ctx.Host, config: config}, nil
	}); err != nil {
		panic(err)
	}
}

// main 启动 Application。
func main() { app.Start() }
