package main

import (
	"context"
	"time"

	"github.com/duanhf2012/origin/v3/application"
	"github.com/duanhf2012/origin/v3/discovery/provider"
	"github.com/duanhf2012/origin/v3/service"
)

var app = application.New()

type GatewayService struct{ service.Service }

func (target *GatewayService) OnStart(ctx context.Context) error {
	if err := target.AwaitNodeService(ctx, "player-1", "PlayerService"); err != nil {
		return err
	}
	instance, ok := target.FindDiscoveredService("player-1", "PlayerService")
	if !ok {
		return nil
	}
	target.Logger().Info("discovery target is ready: " + instance.NodeID)
	return nil
}

// staticProvider publishes one remote service so the example remains runnable without external infrastructure.
type staticProvider struct{ host provider.Host }

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

func (*staticProvider) Publish(context.Context, provider.Node) error { return nil }
func (*staticProvider) Withdraw(context.Context) error               { return nil }
func (*staticProvider) Close(context.Context) error                  { return nil }

func init() {
	app.Setup(&GatewayService{})
	if err := app.RegisterDiscoveryProvider("await-demo", func(ctx provider.Context) (provider.Provider, error) {
		return &staticProvider{host: ctx.Host}, nil
	}); err != nil {
		panic(err)
	}
}

func main() { app.Start() }
