package node

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/duanhf2012/origin/v3/errs"
	"github.com/duanhf2012/origin/v3/service"
)

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
	var result error
	if retire {
		for index := len(node.services) - 1; index >= 0; index-- {
			entry := node.services[index]
			if err := entry.instance.Retire(ctx); err != nil {
				result = errors.Join(result, fmt.Errorf("Service %q Retire: %w", entry.name, err))
			}
		}
		return result
	}
	for _, entry := range node.services {
		if err := entry.instance.Resume(ctx); err != nil {
			result = errors.Join(result, fmt.Errorf("Service %q Resume: %w", entry.name, err))
		}
	}
	return result
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

// PublishServiceState 等待当前或更新发现发布代次获得 ACK。
func (runtime *serviceRuntime) PublishServiceState(ctx context.Context) error {
	if runtime == nil || runtime.node == nil {
		return errs.ErrInvalidArgument
	}
	if runtime.node.discoveryPublication == nil {
		return nil
	}
	return runtime.node.discoveryPublication.request(ctx)
}
