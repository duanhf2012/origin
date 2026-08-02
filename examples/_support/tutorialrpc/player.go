// Package tutorialrpc 提供 RPC 教程共用的合约与实现 Service。
package tutorialrpc

import (
	"context"
	"strconv"

	"github.com/duanhf2012/origin/v3/service"
)

// PlayerRPC 是由 origingen 生成强类型客户端和 Dispatcher 的业务合约。
//
//origin:rpc
type PlayerRPC interface {
	// GetPlayer 展示带返回值和业务错误的请求/响应方法。
	GetPlayer(context.Context, int64) (string, error)
	// Refresh 展示没有返回值、可由 Notify 或 Broadcast 调用的方法。
	Refresh(context.Context, int64)
}

// PlayerService 实现 PlayerRPC；默认 ServiceName 由 PlayerRPC 约定为 PlayerService。
type PlayerService struct{ service.Service }

// GetPlayer 返回稳定字符串，便于教程只关注 RPC 路径而不依赖数据库。
func (target *PlayerService) GetPlayer(_ context.Context, playerID int64) (string, error) {
	return "player-" + strconv.FormatInt(playerID, 10), nil
}

// Refresh 记录版本号，展示 Notify 和 Broadcast 已到达目标 Service。
func (target *PlayerService) Refresh(_ context.Context, version int64) {
	target.Logger().Info("player cache refreshed: " + strconv.FormatInt(version, 10))
}
