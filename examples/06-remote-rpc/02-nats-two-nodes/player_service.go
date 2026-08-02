package main

import (
	"context"
	"strconv"

	"github.com/duanhf2012/origin/v3/examples/_support/tutorialrpc"
	"github.com/duanhf2012/origin/v3/service"
)

// PlayerService 是运行在 player-1 Node 上的业务实现；切换 NATS 不改变其代码。
type PlayerService struct{ service.Service }

// 编译期断言保证业务方法与共享契约保持一致。
var _ tutorialrpc.PlayerService = (*PlayerService)(nil)

// GetPlayer 返回远端 NATS 调用结果。
func (*PlayerService) GetPlayer(_ context.Context, playerID int64) (string, error) {
	return "player-" + strconv.FormatInt(playerID, 10), nil
}

// Refresh 提供契约要求的通知方法。
func (target *PlayerService) Refresh(_ context.Context, version int64) {
	target.Logger().Info("player cache refreshed: " + strconv.FormatInt(version, 10))
}
