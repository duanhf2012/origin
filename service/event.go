package service

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"runtime/debug"
	"sync/atomic"

	"github.com/duanhf2012/origin/v3/errs"
	originlog "github.com/duanhf2012/origin/v3/log"
)

const (
	// MaxEventIDsPerService 是单个 Service 可订阅的不同事件 ID 硬上限。
	MaxEventIDsPerService = 4096
	// MaxEventListenersPerService 是单个 Service 全部事件监听器的硬上限。
	MaxEventListenersPerService = 65536
	// MaxSynchronousEventDepth 限制同步事件嵌套，防止错误递归耗尽调用栈。
	MaxSynchronousEventDepth = 64
)

// EventID 是 Service 本地事件的稳定非零标识。
type EventID uint32

// Event 是本地事件 payload 的最小契约。
//
// 同一个 Service 中，同一 EventID 第一次成功通知后绑定该 payload 的具体 Go 类型。
type Event interface {
	EventID() EventID
}

// EventHandler 是按订阅顺序在所属 Service 执行语义中调用的本地监听器。
type EventHandler func(context.Context, Event) error

// EventStats 是当前 Service 本地事件的无锁累计统计。
type EventStats struct {
	SyncNotifiedTotal   uint64
	AsyncNotifiedTotal  uint64
	HandlerFailureTotal uint64
}

type eventPayloadType struct {
	value reflect.Type
}

type eventSlot struct {
	id        EventID
	payload   atomic.Pointer[eventPayloadType]
	listeners []*eventListener
}

type eventListener struct {
	handler EventHandler
	active  atomic.Bool
}

// SubscribeEvent 在 Service.OnInit 或 Module.OnInit 中登记一个监听器。
func (service *Service) SubscribeEvent(eventID EventID, handler EventHandler) error {
	return service.subscribeEvent(eventID, handler, nil)
}

func (service *Service) subscribeEvent(
	eventID EventID,
	handler EventHandler,
	module *Module,
) error {
	if service == nil || eventID == 0 || handler == nil {
		return errs.ErrInvalidArgument
	}
	service.bindMu.Lock()
	if !service.moduleInitActive || service.moduleSealed || service.State() != StateInitializing {
		service.bindMu.Unlock()
		return errs.NewMessage(errs.CodeInvalidArgument, "事件只能在 OnInit 中订阅")
	}
	slot := service.events[eventID]
	if slot == nil {
		if len(service.events) >= MaxEventIDsPerService {
			service.bindMu.Unlock()
			return errs.NewMessage(errs.CodeInvalidArgument, "Service 事件 ID 数量超过 4096")
		}
		if service.events == nil {
			service.events = make(map[EventID]*eventSlot)
		}
		slot = &eventSlot{id: eventID}
		service.events[eventID] = slot
	}
	if service.eventListenerCount >= MaxEventListenersPerService {
		service.bindMu.Unlock()
		return errs.NewMessage(errs.CodeInvalidArgument, "Service 事件监听器数量超过 65536")
	}
	listener := &eventListener{handler: handler}
	listener.active.Store(true)
	slot.listeners = append(slot.listeners, listener)
	service.eventListenerCount++
	if module != nil {
		module.scopeMu.Lock()
		module.eventListeners = append(module.eventListeners, listener)
		module.scopeMu.Unlock()
	}
	service.bindMu.Unlock()
	return nil
}

// NotifyEventSync 在当前所属 Service Task 中同步通知全部监听器。
//
// 监听器可以嵌套同步通知或调用 Await；Await 期间允许其他 Service Task
// 执行，恢复后仍按注册顺序继续。错误和 panic 会聚合后返回且不跳过后续监听器。
func (service *Service) NotifyEventSync(ctx context.Context, event Event) error {
	if service == nil || ctx == nil {
		return errs.ErrInvalidArgument
	}
	eventID, payloadType, err := inspectEvent(event)
	if err != nil {
		return err
	}
	scheduler := service.scheduler.Load()
	if scheduler == nil {
		return errs.ErrServiceNotReady
	}
	task, err := scheduler.enterSynchronousEvent(ctx)
	if err != nil {
		return err
	}
	defer scheduler.leaveSynchronousEvent(task)

	slot := service.events[eventID]
	if slot == nil {
		service.eventSyncTotal.Add(1)
		return nil
	}
	if err := slot.bindPayload(payloadType); err != nil {
		return err
	}
	result, failures := service.notifyEventHandlers(ctx, slot, event)
	service.eventSyncTotal.Add(1)
	service.eventFailureTotal.Add(uint64(failures))
	return result
}

// NotifyEventAsync 把一次完整通知作为一个普通 Ready item 提交。
//
// 方法只保存调用方传入的 Event 接口值，不复制 payload；提交成功后生产者不得再修改它。
func (service *Service) NotifyEventAsync(event Event) error {
	if service == nil {
		return errs.ErrInvalidArgument
	}
	eventID, payloadType, err := inspectEvent(event)
	if err != nil {
		return err
	}
	if err := service.acceptanceError(); err != nil {
		return err
	}
	slot := service.events[eventID]
	if slot != nil {
		if err := slot.bindPayload(payloadType); err != nil {
			return err
		}
	}
	scheduler := service.scheduler.Load()
	if scheduler == nil {
		return errs.ErrServiceNotReady
	}
	if err := scheduler.dispatchEvent(service, slot, event); err != nil {
		return err
	}
	service.eventAsyncTotal.Add(1)
	return nil
}

// EventStats 返回当前 Service 的事件累计统计。
func (service *Service) EventStats() EventStats {
	if service == nil {
		return EventStats{}
	}
	return EventStats{
		SyncNotifiedTotal:   service.eventSyncTotal.Load(),
		AsyncNotifiedTotal:  service.eventAsyncTotal.Load(),
		HandlerFailureTotal: service.eventFailureTotal.Load(),
	}
}

func (service *Service) executeAsyncEvent(
	ctx context.Context,
	slot *eventSlot,
	event Event,
) {
	if slot == nil {
		return
	}
	result, failures := service.notifyEventHandlers(ctx, slot, event)
	if failures == 0 {
		return
	}
	service.eventFailureTotal.Add(uint64(failures))
	service.Logger().Error(
		"service async event listeners failed",
		originlog.Uint32("event_id", uint32(slot.id)),
		originlog.Int("listener_failures", failures),
		originlog.Err(result),
	)
}

func (service *Service) notifyEventHandlers(
	ctx context.Context,
	slot *eventSlot,
	event Event,
) (error, int) {
	var result error
	failures := 0
	for _, listener := range slot.listeners {
		if !listener.active.Load() {
			continue
		}
		if err := callEventHandler(listener.handler, ctx, event); err != nil {
			failures++
			result = errors.Join(result, err)
		}
	}
	return result, failures
}

func callEventHandler(
	handler EventHandler,
	ctx context.Context,
	event Event,
) (result error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			result = fmt.Errorf(
				"事件监听器 panic: %v\n%s",
				recovered,
				debug.Stack(),
			)
		}
	}()
	return handler(ctx, event)
}

func inspectEvent(event Event) (eventID EventID, payloadType reflect.Type, result error) {
	if event == nil || isNilEvent(event) {
		return 0, nil, errs.ErrInvalidArgument
	}
	defer func() {
		if recovered := recover(); recovered != nil {
			eventID = 0
			payloadType = nil
			result = errs.NewMessage(errs.CodeInvalidArgument, "EventID 方法发生 panic")
		}
	}()
	eventID = event.EventID()
	if eventID == 0 {
		return 0, nil, errs.ErrInvalidArgument
	}
	return eventID, reflect.TypeOf(event), nil
}

func isNilEvent(event Event) bool {
	value := reflect.ValueOf(event)
	switch value.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return value.IsNil()
	default:
		return false
	}
}

func (slot *eventSlot) bindPayload(payloadType reflect.Type) error {
	bound := slot.payload.Load()
	if bound == nil {
		candidate := &eventPayloadType{value: payloadType}
		if slot.payload.CompareAndSwap(nil, candidate) {
			return nil
		}
		bound = slot.payload.Load()
	}
	if bound.value != payloadType {
		return errs.NewMessage(errs.CodeInvalidArgument, "同一 EventID 使用了不同 payload 类型")
	}
	return nil
}
