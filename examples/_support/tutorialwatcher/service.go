// Package tutorialwatcher contains a small Service used by discovery examples.
package tutorialwatcher

import (
	"context"
	"fmt"

	"github.com/duanhf2012/origin/v3/discovery"
	"github.com/duanhf2012/origin/v3/service"
)

type Service struct{ service.Service }

func (target *Service) OnInit() error {
	_, err := target.AddDiscoveryListener(listener{owner: target})
	return err
}

type listener struct{ owner *Service }

func (target listener) OnDiscovered(_ context.Context, event discovery.Event) {
	target.owner.Logger().Info(fmt.Sprintf("discovered node=%s services=%v", event.NodeID, event.Services))
}

func (target listener) OnStateChanged(_ context.Context, event discovery.Event) {
	target.owner.Logger().Info(fmt.Sprintf("state changed node=%s services=%v", event.NodeID, event.Services))
}

func (target listener) OnLost(_ context.Context, event discovery.Event) {
	target.owner.Logger().Info(fmt.Sprintf("lost node=%s services=%v", event.NodeID, event.Services))
}
