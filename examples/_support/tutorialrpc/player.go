// Package tutorialrpc contains the small generated contract shared by the remote RPC tutorials.
package tutorialrpc

import (
	"context"
	"strconv"

	"github.com/duanhf2012/origin/v3/service"
)

//origin:rpc
type PlayerRPC interface {
	GetPlayer(context.Context, int64) (string, error)
	Refresh(context.Context, int64)
}

type PlayerService struct{ service.Service }

func (target *PlayerService) GetPlayer(_ context.Context, playerID int64) (string, error) {
	return "player-" + strconv.FormatInt(playerID, 10), nil
}

func (target *PlayerService) Refresh(_ context.Context, version int64) {
	target.Logger().Info("player cache refreshed: " + strconv.FormatInt(version, 10))
}
