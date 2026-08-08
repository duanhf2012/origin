package main

import (
	"context"
	"strconv"

	"github.com/duanhf2012/origin/v3/examples/_support/tutorialrpc"
	"github.com/duanhf2012/origin/v3/service"
)

// PlayerService 是当前示例目录内的业务实现，不需要生成业务侧代码。
type PlayerService struct{ service.Service }

// 该断言只做编译期校验，不参与运行时 RPC 分派。
var _ tutorialrpc.PlayerService = (*PlayerService)(nil)

// GetPlayer 为 Async 调用返回一个容易观察的结果。
func (*PlayerService) GetPlayer(_ context.Context, playerID int64) (string, error) {
	return "player-" + strconv.FormatInt(playerID, 10), nil
}

// Refresh 记录 Notify 是否到达目标 Service。
func (target *PlayerService) Refresh(_ context.Context, version int64) {
	target.Logger().Info("player cache refreshed: " + strconv.FormatInt(version, 10))
}
