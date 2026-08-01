package service

import (
	"context"
	"errors"
	"time"

	"github.com/duanhf2012/origin/v3/errs"
)

// ServiceStateChangedEventID 是框架内建状态变化事件唯一保留的 EventID。
const ServiceStateChangedEventID EventID = EventID(^uint32(0))

// ServiceStateChanged 在 Retire/Resume 本地状态提交后异步通知所属 Service。
type ServiceStateChanged struct {
	Previous  State
	Current   State
	ChangedAt time.Time
}

// EventID 实现 Event。
func (ServiceStateChanged) EventID() EventID { return ServiceStateChangedEventID }

// retirementRuntime 是 Node 提供的可选状态线性化和发现发布适配面。
type retirementRuntime interface {
	TransitionServiceState(from State, to State) (time.Time, bool, error)
	PublishServiceState(context.Context) error
}

// Retire 把 Running Service 转换为 Retired，并等待对应发现快照发布确认。
func (service *Service) Retire(ctx context.Context) error {
	return service.changeRunningState(ctx, StateRunning, StateRetired)
}

// Resume 把 Retired Service 恢复为 Running，并等待对应发现快照发布确认。
func (service *Service) Resume(ctx context.Context) error {
	return service.changeRunningState(ctx, StateRetired, StateRunning)
}

func (service *Service) changeRunningState(
	ctx context.Context,
	from State,
	to State,
) error {
	if service == nil || ctx == nil || service.runtime == nil {
		return errs.ErrInvalidArgument
	}
	runtime, ok := service.runtime.(retirementRuntime)
	if !ok {
		return errs.NewMessage(errs.CodeInvalidArgument, "Service Runtime 不支持 Retire/Resume")
	}
	scheduler := service.scheduler.Load()
	if scheduler == nil {
		return errs.ErrServiceNotReady
	}
	if scheduler.synchronousEventActive(ctx) {
		// Retire/Resume 内部必须 Await 发布确认；同步事件禁止在状态已提交后才发现无法 Await。
		return errs.ErrInvalidArgument
	}
	return scheduler.executeControl(ctx, func(taskCtx context.Context) error {
		if err := taskCtx.Err(); err != nil {
			return errs.Wrap(errs.CodeOf(err), err)
		}
		changedAt, changed, err := runtime.TransitionServiceState(from, to)
		if err != nil || !changed {
			return err
		}
		eventErr := service.NotifyEventAsync(ServiceStateChanged{
			Previous:  from,
			Current:   to,
			ChangedAt: changedAt,
		})
		publishErr := service.Await(taskCtx, runtime.PublishServiceState)
		// 本地状态已经提交；事件队列或远端发布失败都只作为结果返回，绝不回滚。
		return errors.Join(eventErr, publishErr)
	})
}
