package main

import (
	"context"
	"strconv"

	"github.com/duanhf2012/origin/v3/examples/_support/tutorialrpc"
	"github.com/duanhf2012/origin/v3/service"
)

// PlayerService 是本示例的普通业务 Service。它无需 //origin:rpc 标记，也不会生成
// 任何业务侧适配文件；Node 会按模板名 PlayerService 自动关联生成契约。
type PlayerService struct{ service.Service }

// 编译期断言用于尽早发现业务实现漏掉或写错 RPC 方法。
var _ tutorialrpc.PlayerService = (*PlayerService)(nil)

// GetPlayer 返回稳定字符串，让示例只关注 RPC 调用外观。
func (*PlayerService) GetPlayer(_ context.Context, playerID int64) (string, error) {
	return "player-" + strconv.FormatInt(playerID, 10), nil
}

// Refresh 展示无业务返回值的方法；本示例暂不处理缓存刷新逻辑。
func (*PlayerService) Refresh(context.Context, int64) {}
