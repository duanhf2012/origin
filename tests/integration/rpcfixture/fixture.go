// Package rpcfixture 提供 M11 代码生成和同 Node 调用的真实集成夹具。
package rpcfixture

import (
	"context"

	"github.com/duanhf2012/origin/v3/errs"
	"github.com/duanhf2012/origin/v3/service"
	"google.golang.org/protobuf/types/known/structpb"
	"google.golang.org/protobuf/types/known/wrapperspb"
)

// OptionalScore 验证具名指针类型可以保留 nil 与非 nil 零值语义。
type OptionalScore *int32

// PlayerData 同时覆盖普通结构体、指针、Slice、Map、[]byte 和嵌套 Protobuf。
type PlayerData struct {
	ID       int64
	Name     string
	Score    OptionalScore
	Tags     []string
	Metadata map[string]*wrapperspb.StringValue
	Payload  []byte
}

//origin:rpc
type PlayerRPC interface {
	EchoName(
		ctx context.Context,
		value string,
	) string

	GetPlayer(
		ctx context.Context,
		playerID int64,
		seed PlayerData,
		options *structpb.Struct,
	) (PlayerData, *structpb.Struct, error)

	SavePlayer(ctx context.Context, player PlayerData) error

	PlayerOnline(ctx context.Context, playerID int64)
}

// PlayerService 是集成测试的目标 RPC Service。
type PlayerService struct {
	service.Service

	LastSaved   PlayerData
	OnlineID    int64
	GetCount    int
	ShouldFail  bool
	ShouldPanic bool
	Wait        <-chan struct{}
}

// EchoName 覆盖普通类型业务输出不声明 error 的契约分类。
func (target *PlayerService) EchoName(
	_ context.Context,
	value string,
) string {
	return value + "-echo"
}

// GetPlayer 返回一份独立结果并回显顶层 Protobuf。
func (target *PlayerService) GetPlayer(
	ctx context.Context,
	playerID int64,
	seed PlayerData,
	options *structpb.Struct,
) (PlayerData, *structpb.Struct, error) {
	target.GetCount++
	if target.ShouldPanic {
		panic("player rpc test panic")
	}
	if target.Wait != nil {
		select {
		case <-target.Wait:
		case <-ctx.Done():
			return PlayerData{}, nil, ctx.Err()
		}
	}
	if target.ShouldFail {
		return PlayerData{}, nil, errs.ErrInvalidArgument
	}
	seed.ID = playerID
	return seed, options, nil
}

// SavePlayer 保存解码后的普通结构体。
func (target *PlayerService) SavePlayer(
	_ context.Context,
	player PlayerData,
) error {
	target.LastSaved = player
	return nil
}

// PlayerOnline 接收完全无返回值的通知。
func (target *PlayerService) PlayerOnline(_ context.Context, playerID int64) {
	target.OnlineID = playerID
}

// CallerService 为生成客户端提供独立的 Service 执行上下文。
type CallerService struct {
	service.Service
}
