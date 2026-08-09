package application

import (
	"context"
	"errors"
	"fmt"
	"runtime/debug"

	"github.com/duanhf2012/origin/v3/command"
	"github.com/duanhf2012/origin/v3/errs"
	"github.com/duanhf2012/origin/v3/node"
)

func (app *Application) handleControlRequest(
	lifecycleCtx context.Context,
	request command.ControlRequest,
) (result error) {
	if request == nil || request.Context() == nil {
		return errs.ErrInvalidArgument
	}
	defer func() {
		if value := recover(); value != nil {
			result = errs.Wrap(
				errs.CodeInternal,
				fmt.Errorf("Application 控制请求 panic: %v\n%s", value, debug.Stack()),
			)
		}
	}()
	controlCtx, cancel := context.WithCancel(request.Context())
	stopCancel := context.AfterFunc(lifecycleCtx, cancel)
	defer func() {
		stopCancel()
		cancel()
	}()
	switch request.Action() {
	case command.ControlActionRetire:
		return app.Retire(controlCtx)
	case command.ControlActionResume:
		return app.Resume(controlCtx)
	default:
		return errs.ErrInvalidArgument
	}
}

// Retire 按 Node 启动顺序的严格逆序退休全部 Service。
//
// 各 Node 的错误按 best-effort 聚合，前一个失败不会跳过后续 Node。
func (app *Application) Retire(ctx context.Context) error {
	return app.changeNodes(ctx, true)
}

// Resume 按 Node 启动顺序恢复全部 Retired Service。
func (app *Application) Resume(ctx context.Context) error {
	return app.changeNodes(ctx, false)
}

func (app *Application) changeNodes(ctx context.Context, retire bool) error {
	if app == nil || ctx == nil {
		return errs.ErrInvalidArgument
	}
	if app.State() != StateRunning {
		return applicationControlStateError(app.State())
	}
	app.mu.Lock()
	nodes := append([]*node.Node(nil), app.nodes...)
	app.mu.Unlock()
	var result error
	if retire {
		for index := len(nodes) - 1; index >= 0; index-- {
			current := nodes[index]
			if err := current.Retire(ctx); err != nil {
				result = errors.Join(result, fmt.Errorf("Node %q Retire: %w", current.ID(), err))
			}
		}
		return result
	}
	for _, current := range nodes {
		if err := current.Resume(ctx); err != nil {
			result = errors.Join(result, fmt.Errorf("Node %q Resume: %w", current.ID(), err))
		}
	}
	return result
}

func applicationControlStateError(state State) error {
	switch state {
	case StateStopping:
		return errs.ErrServiceStopping
	case StateStopped:
		return errs.ErrServiceStopped
	case StateFailed:
		return errs.ErrServiceFailed
	default:
		return errs.ErrServiceNotReady
	}
}
