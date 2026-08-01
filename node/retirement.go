package node

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/duanhf2012/origin/v3/errs"
	"github.com/duanhf2012/origin/v3/service"
)

type batchDiscoveryPublicationKey struct{}

// Retire 按 Service 启动顺序的严格逆序退休当前 Node 的全部 Service。
func (node *Node) Retire(ctx context.Context) error {
	return node.changeServices(ctx, true)
}

// Resume 按 Service 启动顺序恢复当前 Node 的全部 Retired Service。
func (node *Node) Resume(ctx context.Context) error {
	return node.changeServices(ctx, false)
}

func (node *Node) changeServices(ctx context.Context, retire bool) error {
	if node == nil || ctx == nil {
		return errs.ErrInvalidArgument
	}
	if node.State() != StateReady {
		return nodeStateControlError(node.State())
	}
	batchCtx := context.WithValue(ctx, batchDiscoveryPublicationKey{}, struct{}{})
	var result error
	if retire {
		for index := len(node.services) - 1; index >= 0; index-- {
			entry := node.services[index]
			if err := entry.instance.Retire(batchCtx); err != nil {
				result = errors.Join(result, fmt.Errorf("Service %q Retire: %w", entry.name, err))
			}
		}
	} else {
		for _, entry := range node.services {
			if err := entry.instance.Resume(batchCtx); err != nil {
				result = errors.Join(result, fmt.Errorf("Service %q Resume: %w", entry.name, err))
			}
		}
	}
	return errors.Join(result, node.requestDiscoveryPublication(ctx))
}

func nodeStateControlError(state State) error {
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

// TransitionServiceState 实现 service 的可选 Retire/Resume 状态线性化适配面。
func (runtime *serviceRuntime) TransitionServiceState(
	from service.State,
	to service.State,
) (time.Time, bool, error) {
	if runtime == nil || runtime.node == nil || runtime.entry == nil {
		return time.Time{}, false, errs.ErrInvalidArgument
	}
	if runtime.node.State() != StateReady {
		return time.Time{}, false, nodeStateControlError(runtime.node.State())
	}
	for {
		if state := runtime.node.State(); state != StateReady {
			return time.Time{}, false, nodeStateControlError(state)
		}
		currentSnapshot := runtime.entry.state.Load()
		if currentSnapshot == nil {
			return time.Time{}, false, errs.ErrServiceNotReady
		}
		current := currentSnapshot.State
		if current == to {
			if state := runtime.node.State(); state != StateReady {
				return time.Time{}, false, nodeStateControlError(state)
			}
			return currentSnapshot.EnteredAt, false, nil
		}
		if current != from {
			switch current {
			case service.StateStopping:
				return time.Time{}, false, errs.ErrServiceStopping
			case service.StateStopped:
				return time.Time{}, false, errs.ErrServiceStopped
			case service.StateFailed:
				return time.Time{}, false, errs.ErrServiceFailed
			}
			return time.Time{}, false, errs.NewMessage(
				errs.CodeInvalidArgument,
				fmt.Sprintf("Service %q 不能从 %s 转换为 %s", runtime.entry.name, current, to),
			)
		}
		changedAt := time.Now()
		next := &serviceStateSnapshot{State: to, EnteredAt: changedAt}
		if !runtime.entry.state.CompareAndSwap(currentSnapshot, next) {
			continue
		}
		if state := runtime.node.State(); state != StateReady {
			// Node Stop 已取得优先权；直接关闭本地准入，后续 Stop 路径会完成 Scheduler 清理。
			runtime.entry.state.CompareAndSwap(next, &serviceStateSnapshot{
				State:     service.StateStopping,
				EnteredAt: time.Now(),
			})
			return time.Time{}, false, nodeStateControlError(state)
		}
		return changedAt, true, nil
	}
}

// ReserveServiceStatePublication 在 Service 释放执行槽前预留发布代次。
func (runtime *serviceRuntime) ReserveServiceStatePublication(
	ctx context.Context,
) (uint64, error) {
	if runtime == nil || runtime.node == nil {
		return 0, errs.ErrInvalidArgument
	}
	if runtime.DeferServiceStatePublication(ctx) {
		return 0, nil
	}
	if runtime.node.discoveryPublication == nil {
		return 0, nil
	}
	return runtime.node.discoveryPublication.enqueue()
}

// AwaitServiceStatePublication 等待预留代次或更新代次获得 ACK。
func (runtime *serviceRuntime) AwaitServiceStatePublication(
	ctx context.Context,
	generation uint64,
) error {
	if runtime == nil || runtime.node == nil {
		return errs.ErrInvalidArgument
	}
	if generation == 0 || runtime.node.discoveryPublication == nil {
		return nil
	}
	return runtime.node.discoveryPublication.wait(ctx, generation)
}

func (*serviceRuntime) DeferServiceStatePublication(ctx context.Context) bool {
	if ctx == nil {
		return false
	}
	_, deferred := ctx.Value(batchDiscoveryPublicationKey{}).(struct{})
	return deferred
}
