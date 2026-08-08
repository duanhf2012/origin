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
	ReserveServiceStatePublication(context.Context) (uint64, error)
	AwaitServiceStatePublication(context.Context, uint64) error
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
	return scheduler.executeControl(ctx, func(taskCtx context.Context) error {
		if err := taskCtx.Err(); err != nil {
			return errs.Wrap(errs.CodeOf(err), err)
		}
		changedAt, changed, err := runtime.TransitionServiceState(from, to)
		if err != nil {
			return err
		}
		var eventErr error
		if changed {
			eventErr = service.NotifyEventAsync(ServiceStateChanged{
				Previous:  from,
				Current:   to,
				ChangedAt: changedAt,
			})
		}
		generation, reserveErr := runtime.ReserveServiceStatePublication(taskCtx)
		if reserveErr != nil {
			return errors.Join(eventErr, reserveErr)
		}
		if !changed && generation == 0 {
			return nil
		}
		publishErr := service.Await(taskCtx, func(waitCtx context.Context) error {
			return runtime.AwaitServiceStatePublication(waitCtx, generation)
		})
		// 本地状态已经提交；事件队列或远端发布失败都只作为结果返回，绝不回滚。
		return errors.Join(eventErr, publishErr)
	})
}
