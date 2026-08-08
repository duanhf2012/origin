package main

import (
	"context"
	"strconv"

	"github.com/duanhf2012/origin/v3/examples/_support/tutorialrpc"
	"github.com/duanhf2012/origin/v3/service"
)

// PlayerService 是运行在 player-1 Node 上的业务实现。
type PlayerService struct{ service.Service }

// 编译期断言保证静态 Dispatcher 能安全接收该实现。
var _ tutorialrpc.PlayerService = (*PlayerService)(nil)

// GetPlayer 返回远端 TCP 调用结果。
func (*PlayerService) GetPlayer(_ context.Context, playerID int64) (string, error) {
	return "player-" + strconv.FormatInt(playerID, 10), nil
}

// Refresh 提供契约要求的通知方法。
func (target *PlayerService) Refresh(_ context.Context, version int64) {
	target.Logger().Info("player cache refreshed: " + strconv.FormatInt(version, 10))
}
