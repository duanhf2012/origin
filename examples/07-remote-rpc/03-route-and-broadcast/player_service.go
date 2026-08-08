package main

import (
	"context"
	"strconv"

	"github.com/duanhf2012/origin/v3/examples/_support/tutorialrpc"
	"github.com/duanhf2012/origin/v3/service"
)

// PlayerService 是多个 Player Node 共用的业务模板。
type PlayerService struct{ service.Service }

// 编译期断言不会产生运行时反射或分配。
var _ tutorialrpc.PlayerService = (*PlayerService)(nil)

// GetPlayer 返回当前实例处理的玩家结果。
func (*PlayerService) GetPlayer(_ context.Context, playerID int64) (string, error) {
	return "player-" + strconv.FormatInt(playerID, 10), nil
}

// Refresh 让路由通知和广播在日志中可观察。
func (target *PlayerService) Refresh(_ context.Context, version int64) {
	target.Logger().Info("player cache refreshed: " + strconv.FormatInt(version, 10))
}
