// Package tutorialrpc 只声明 RPC 教程共用的公开合约。
package tutorialrpc

import "context"

// 进入本目录后执行 go generate，即可更新本合约的 player_service.rpc.gen.go。
//go:generate go run github.com/duanhf2012/origin/v3/cmd/origingen rpc .

// PlayerService 描述业务 PlayerService 对其他 Service 公开的 RPC 能力。
//
// 契约与实现使用相同的领域名称，但位于不同包：本接口属于共享 RPC 合约包，具体实现
// 位于各业务示例目录。这样既符合 Go 不使用 I 前缀的习惯，也不会把生成声明和业务
// 逻辑混在同一个源文件中。
//
//origin:rpc
type PlayerService interface {
	// GetPlayer 展示带返回值和业务错误的请求/响应方法。
	GetPlayer(context.Context, int64) (string, error)
	// Refresh 展示没有返回值、可由 Notify 或 Broadcast 调用的方法。
	Refresh(context.Context, int64)
}
