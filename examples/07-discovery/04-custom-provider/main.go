package main

import (
	"context"
	"fmt"
	"time"

	"github.com/duanhf2012/origin/v3/application"
	"github.com/duanhf2012/origin/v3/discovery/provider"
	"github.com/duanhf2012/origin/v3/service"
)

var app = application.New()

type consulLikeConfig struct {
	Address string `json:"address"`
}

// consulLikeProvider 只展示 Provider SPI；它不连接真实 Consul。
type consulLikeProvider struct {
	host   provider.Host
	config consulLikeConfig
}

func (target *consulLikeProvider) Start(context.Context) error {
	if err := target.host.SetTTL(5 * time.Second); err != nil {
		return err
	}
	if err := target.host.ReplaceSnapshot(provider.Snapshot{}); err != nil {
		return err
	}
	target.host.Report(provider.Report{State: provider.StateReady})
	return nil
}

func (*consulLikeProvider) Publish(context.Context, provider.Node) error { return nil }
func (*consulLikeProvider) Withdraw(context.Context) error               { return nil }
func (*consulLikeProvider) Close(context.Context) error                  { return nil }

type AppService struct{ service.Service }

func (target *AppService) OnStart(context.Context) error {
	target.Logger().Info("custom Provider is ready; inspect main.go for the small SPI surface")
	return nil
}

func init() {
	app.Setup(&AppService{})
	if err := app.RegisterDiscoveryProvider("consul", func(ctx provider.Context) (provider.Provider, error) {
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

func main() { app.Start() }
