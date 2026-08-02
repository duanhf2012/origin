package main

import (
	"context"
	"strconv"

	"github.com/duanhf2012/origin/v3/examples/_support/tutorialrpc"
	"github.com/duanhf2012/origin/v3/service"
)

// PlayerService 是用于演示 Retired 候选规则的本地业务实现。
type PlayerService struct{ service.Service }

// 编译期断言保证业务实现完整满足生成契约。
var _ tutorialrpc.PlayerService = (*PlayerService)(nil)

// GetPlayer 在显式包含 Retired 后仍可被调用。
func (*PlayerService) GetPlayer(_ context.Context, playerID int64) (string, error) {
	return "player-" + strconv.FormatInt(playerID, 10), nil
}

// Refresh 提供契约要求的通知方法。
func (*PlayerService) Refresh(context.Context, int64) {}
