package main

import (
	"context"
	"time"

	"github.com/duanhf2012/origin/v3/application"
	"github.com/duanhf2012/origin/v3/discovery/provider"
	"github.com/duanhf2012/origin/v3/examples/_support/tutorialwatcher"
)

var app = application.New()

// disappearingProvider 先发布一个权威远端快照，再提交空快照，演示 Lost 是立即状态事实。
type disappearingProvider struct{ host provider.Host }

func (target *disappearingProvider) Start(context.Context) error {
	if err := target.host.SetTTL(time.Second); err != nil {
		return err
	}
	if err := target.host.ReplaceSnapshot(provider.Snapshot{Nodes: []provider.Node{{
		NodeID: "player-1", SessionID: 1, Transport: provider.TransportNone,
		Services: []provider.Service{{ServiceName: "PlayerService", State: provider.ServiceStateRunning}},
	}}}); err != nil {
		return err
	}
	target.host.Report(provider.Report{State: provider.StateReady})
	go func() {
		time.Sleep(500 * time.Millisecond)
		_ = target.host.ReplaceSnapshot(provider.Snapshot{})
		target.host.Report(provider.Report{State: provider.StateRecovering, Reconnects: 1})
	}()
	return nil
}

func (*disappearingProvider) Publish(context.Context, provider.Node) error { return nil }
func (*disappearingProvider) Withdraw(context.Context) error               { return nil }
func (*disappearingProvider) Close(context.Context) error                  { return nil }

func init() {
	app.Setup(&tutorialwatcher.Service{})
	if err := app.RegisterDiscoveryProvider("demo", func(ctx provider.Context) (provider.Provider, error) {
		return &disappearingProvider{host: ctx.Host}, nil
	}); err != nil {
		panic(err)
	}
}

func main() { app.Start() }
