package service

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"runtime/debug"
	"sync"
	"time"

	"github.com/duanhf2012/origin/v3/errs"
	originlog "github.com/duanhf2012/origin/v3/log"
)

const (
	// MaxModulesPerService 是单个 Service 可登记的 Module 硬上限。
	MaxModulesPerService = 4096
	// MaxModuleDepth 是包含根 Module 在内的最大嵌套深度。
	MaxModuleDepth = 64
)

// IModule 是可嵌入 Service 的业务模块生命周期契约。
//
// 业务 Module 通常匿名嵌入 Module，只覆盖需要的生命周期方法。私有 baseModule
// 方法确保任意外部类型不能绕过 Origin 的归属和资源 scope。
type IModule interface {
	OnInit() error
	OnStart(context.Context) error
	OnStop(context.Context) error

	baseModule() *Module
}

// Module 为业务模块提供默认生命周期以及所属 Service 的统一能力委托。
//
// Module 必须按值匿名嵌入业务结构体，实例只能加入一个 Service，绑定后不得复制。
type Module struct {
	bindMu sync.Mutex
	owner  *Service
	target IService
	entry  *moduleEntry

	// scopeMu 仅保护可由并发 Timer 回调完成的 Module 长期资源登记。
	scopeMu        sync.Mutex
	timers         map[TimerID]*moduleTimerRegistration
	eventListeners []*eventListener
}

type moduleEntry struct {
	target       IModule
	base         *Module
	parent       *moduleEntry
	depth        int
	initializing bool
	initialized  bool
	started      bool
	stopped      bool
}

type moduleTimerRegistration struct {
	id        TimerID
	completed bool
}

// OnInit 是无需初始化逻辑时的默认实现。
func (module *Module) OnInit() error { return nil }

// OnStart 是无需启动逻辑时的默认实现。
func (module *Module) OnStart(context.Context) error { return nil }

// OnStop 是无需停止逻辑时的默认实现。
func (module *Module) OnStop(context.Context) error { return nil }

// Service 返回当前 Module 唯一所属的业务 Service。
func (module *Module) Service() IService {
	if module == nil {
		return nil
	}
	// 绑定在 Module.OnInit 进入前完成，之后 target 终身只读；热路径无需互斥。
	return module.target
}

// AddModule 在当前 Module 的 OnInit 调用栈中同步登记并初始化一个子 Module。
func (module *Module) AddModule(child IModule) error {
	if module == nil {
		return errs.ErrInvalidArgument
	}
	module.bindMu.Lock()
	owner := module.owner
	entry := module.entry
	module.bindMu.Unlock()
	if owner == nil || entry == nil {
		return errs.ErrInvalidArgument
	}
	return owner.addModule(entry, child)
}

// AddModule 在 Service.OnInit 中同步登记并初始化一个根 Module。
func (service *Service) AddModule(module IModule) error {
	if service == nil {
		return errs.ErrInvalidArgument
	}
	return service.addModule(nil, module)
}

func (service *Service) addModule(parent *moduleEntry, target IModule) error {
	if target == nil || isNilModule(target) {
		return errs.ErrInvalidArgument
	}
	base := target.baseModule()
	if base == nil {
		return errs.ErrInvalidArgument
	}

	// 先锁待绑定 Module，再锁所属 Service。不同 Service 竞争同一 Module 时只会有一个
	// 调用提交 owner，且不会形成 Service -> Module 的反向锁顺序。
	base.bindMu.Lock()
	if base.owner != nil {
		base.bindMu.Unlock()
		return errs.NewMessage(errs.CodeInvalidArgument, "Module 已经绑定到 Service")
	}
	service.bindMu.Lock()
	if !service.moduleInitActive || service.moduleSealed || service.moduleTarget == nil {
		service.bindMu.Unlock()
		base.bindMu.Unlock()
		return errs.NewMessage(errs.CodeInvalidArgument, "Module 只能在 OnInit 中登记")
	}
	depth := 1
	if parent != nil {
		if !parent.initializing || parent.base == nil || parent.base.owner != service {
			service.bindMu.Unlock()
			base.bindMu.Unlock()
			return errs.NewMessage(errs.CodeInvalidArgument, "子 Module 只能在父 Module.OnInit 中登记")
		}
		depth = parent.depth + 1
	}
	if len(service.modules) >= MaxModulesPerService {
		service.bindMu.Unlock()
		base.bindMu.Unlock()
		return errs.NewMessage(errs.CodeInvalidArgument, "Service Module 数量超过 4096")
	}
	if depth > MaxModuleDepth {
		service.bindMu.Unlock()
		base.bindMu.Unlock()
		return errs.NewMessage(errs.CodeInvalidArgument, "Module 嵌套深度超过 64")
	}
	entry := &moduleEntry{
		target:       target,
		base:         base,
		parent:       parent,
		depth:        depth,
		initializing: true,
	}
	base.owner = service
	base.target = service.moduleTarget
	base.entry = entry
	service.modules = append(service.modules, entry)
	service.bindMu.Unlock()
	// 业务 OnInit 可以再次调用当前 Module.AddModule；发布完整绑定后必须先释放该锁。
	base.bindMu.Unlock()

	err := callModuleLifecycle(service, target, "on_init", target.OnInit)
	service.bindMu.Lock()
	entry.initializing = false
	entry.initialized = err == nil
	if err != nil {
		service.moduleInitErr = errors.Join(service.moduleInitErr, err)
	}
	service.bindMu.Unlock()
	return err
}

func isNilModule(target IModule) bool {
	value := reflect.ValueOf(target)
	switch value.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return value.IsNil()
	default:
		return false
	}
}

// GetConfig 委托所属 Service 读取根配置的显式路径。
func (module *Module) GetConfig(path string, destination any) error {
	owner := module.ownerService()
	if owner == nil {
		return errs.ErrInvalidArgument
	}
	return owner.GetConfig(path, destination)
}

// GetServiceConfig 委托所属 Service 读取有效业务配置的显式相对路径。
func (module *Module) GetServiceConfig(path string, destination any) error {
	owner := module.ownerService()
	if owner == nil {
		return errs.ErrInvalidArgument
	}
	return owner.GetServiceConfig(path, destination)
}

// ParseServiceConfig 委托所属 Service 宽松解析完整有效业务配置。
func (module *Module) ParseServiceConfig(destination any) error {
	owner := module.ownerService()
	if owner == nil {
		return errs.ErrInvalidArgument
	}
	return owner.ParseServiceConfig(destination)
}

// DispatchAsync 把根任务投递到所属 Service。
func (module *Module) DispatchAsync(fn func(context.Context)) error {
	owner := module.ownerService()
	if owner == nil {
		return errs.ErrInvalidArgument
	}
	return owner.DispatchAsync(fn)
}

// SubscribeEvent 在当前 Module.OnInit 中登记归属于该 Module 的监听器。
func (module *Module) SubscribeEvent(eventID EventID, handler EventHandler) error {
	owner := module.ownerService()
	if owner == nil {
		return errs.ErrInvalidArgument
	}
	return owner.subscribeEvent(eventID, handler, module)
}

// NotifyEventSync 委托所属 Service 同步通知本地事件。
func (module *Module) NotifyEventSync(ctx context.Context, event Event) error {
	owner := module.ownerService()
	if owner == nil {
		return errs.ErrInvalidArgument
	}
	return owner.NotifyEventSync(ctx, event)
}

// NotifyEventAsync 委托所属 Service 异步通知本地事件。
func (module *Module) NotifyEventAsync(event Event) error {
	owner := module.ownerService()
	if owner == nil {
		return errs.ErrInvalidArgument
	}
	return owner.NotifyEventAsync(event)
}

// EventStats 返回所属 Service 的事件统计。
func (module *Module) EventStats() EventStats {
	owner := module.ownerService()
	if owner == nil {
		return EventStats{}
	}
	return owner.EventStats()
}

// Retire 委托所属 Service 进入 Retired 并等待发现发布确认。
func (module *Module) Retire(ctx context.Context) error {
	owner := module.ownerService()
	if owner == nil {
		return errs.ErrInvalidArgument
	}
	return owner.Retire(ctx)
}

// Resume 委托所属 Service 恢复 Running 并等待发现发布确认。
func (module *Module) Resume(ctx context.Context) error {
	owner := module.ownerService()
	if owner == nil {
		return errs.ErrInvalidArgument
	}
	return owner.Resume(ctx)
}

// Await 在所属 Service 的当前 Task 中协作式等待。
func (module *Module) Await(ctx context.Context, fn func(context.Context) error) error {
	owner := module.ownerService()
	if owner == nil {
		return errs.ErrInvalidArgument
	}
	return owner.Await(ctx, fn)
}

// SetDefaultAwaitTimeout 在初始化期设置所属 Service 的默认 Await 超时。
func (module *Module) SetDefaultAwaitTimeout(timeout time.Duration) error {
	owner := module.ownerService()
	if owner == nil {
		return errs.ErrInvalidArgument
	}
	return owner.SetDefaultAwaitTimeout(timeout)
}

// ExecutionStats 返回所属 Service 的调度统计。
func (module *Module) ExecutionStats() ExecutionStats {
	owner := module.ownerService()
	if owner == nil {
		return ExecutionStats{}
	}
	return owner.ExecutionStats()
}

// GoSafe 委托所属 Service 启动带 panic 边界的业务 goroutine。
func (module *Module) GoSafe(fn func()) error {
	owner := module.ownerService()
	if owner == nil {
		return errs.ErrInvalidArgument
	}
	return owner.GoSafe(fn)
}

// RunSafe 在当前 goroutine 使用所属 Service 的 panic 边界执行 Job。
func (module *Module) RunSafe(fn func()) error {
	owner := module.ownerService()
	if owner == nil {
		return errs.ErrInvalidArgument
	}
	return owner.RunSafe(fn)
}

// AfterFunc 创建归属于当前 Module 的一次性 Timer。
func (module *Module) AfterFunc(delay time.Duration, fn TimerFunc) TimerID {
	owner := module.ownerService()
	if owner == nil || fn == nil {
		return InvalidTimerID
	}
	registration := &moduleTimerRegistration{}
	id := owner.AfterFunc(delay, func(ctx context.Context, timerID TimerID) {
		module.completeTimer(registration, timerID)
		fn(ctx, timerID)
	})
	return module.registerTimer(registration, id)
}

// NewTicker 创建归属于当前 Module 的周期 Timer。
func (module *Module) NewTicker(interval time.Duration, fn TimerFunc) TimerID {
	owner := module.ownerService()
	if owner == nil {
		return InvalidTimerID
	}
	id := owner.NewTicker(interval, fn)
	return module.registerTimer(&moduleTimerRegistration{}, id)
}

// CronFunc 创建归属于当前 Module 的 Cron Timer。
func (module *Module) CronFunc(expression string, fn TimerFunc) (TimerID, error) {
	owner := module.ownerService()
	if owner == nil {
		return InvalidTimerID, errs.ErrInvalidArgument
	}
	id, err := owner.CronFunc(expression, fn)
	if err != nil {
		return InvalidTimerID, err
	}
	return module.registerTimer(&moduleTimerRegistration{}, id), nil
}

// PauseTimer 暂停所属 Service 中由 ID 指定的 Timer。
func (module *Module) PauseTimer(timerID TimerID) bool {
	owner := module.ownerService()
	return owner != nil && owner.PauseTimer(timerID)
}

// ResumeTimer 恢复所属 Service 中由 ID 指定的 Timer。
func (module *Module) ResumeTimer(timerID TimerID) bool {
	owner := module.ownerService()
	return owner != nil && owner.ResumeTimer(timerID)
}

// CancelTimer 取消 Timer；只有由当前 Module 创建的 Timer 会从当前 scope 移除。
func (module *Module) CancelTimer(timerID *TimerID) bool {
	if timerID == nil {
		return false
	}
	id := *timerID
	owner := module.ownerService()
	if owner == nil {
		*timerID = InvalidTimerID
		return false
	}
	result := owner.CancelTimer(timerID)
	module.scopeMu.Lock()
	delete(module.timers, id)
	module.scopeMu.Unlock()
	return result
}

// TimerStats 返回所属 Service 全部 Timer 的统计快照。
func (module *Module) TimerStats() TimerStats {
	owner := module.ownerService()
	if owner == nil {
		return TimerStats{}
	}
	return owner.TimerStats()
}

func (module *Module) registerTimer(
	registration *moduleTimerRegistration,
	timerID TimerID,
) TimerID {
	if timerID == InvalidTimerID {
		return InvalidTimerID
	}
	module.scopeMu.Lock()
	registration.id = timerID
	if !registration.completed {
		if module.timers == nil {
			module.timers = make(map[TimerID]*moduleTimerRegistration)
		}
		module.timers[timerID] = registration
	}
	module.scopeMu.Unlock()
	return timerID
}

func (module *Module) completeTimer(
	registration *moduleTimerRegistration,
	timerID TimerID,
) {
	module.scopeMu.Lock()
	registration.completed = true
	delete(module.timers, timerID)
	module.scopeMu.Unlock()
}

func (module *Module) cleanupScope() {
	owner := module.ownerService()
	module.scopeMu.Lock()
	identifiers := make([]TimerID, 0, len(module.timers))
	for timerID := range module.timers {
		identifiers = append(identifiers, timerID)
	}
	module.timers = nil
	listeners := module.eventListeners
	module.eventListeners = nil
	module.scopeMu.Unlock()
	for _, listener := range listeners {
		listener.active.Store(false)
	}
	if owner == nil {
		return
	}
	for _, timerID := range identifiers {
		current := timerID
		owner.CancelTimer(&current)
	}
}

func (module *Module) ownerService() *Service {
	if module == nil {
		return nil
	}
	// owner 与 target 一样只写一次，且在任何业务回调开始前发布。
	return module.owner
}

func (module *Module) baseModule() *Module { return module }

func callModuleLifecycle(
	owner *Service,
	target any,
	phase string,
	callback func() error,
) (result error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			stack := debug.Stack()
			moduleType := fmt.Sprintf("%T", target)
			owner.Logger().ErrorStack(
				"module lifecycle panic",
				originlog.String("module_type", moduleType),
				originlog.String("phase", phase),
				originlog.String("panic", fmt.Sprint(recovered)),
				originlog.String("panic_stack", string(stack)),
			)
			result = fmt.Errorf("Module %T %s panic: %v", target, phase, recovered)
		}
	}()
	return callback()
}

func callServiceLifecycle(
	owner *Service,
	target IService,
	phase string,
	callback func() error,
) (result error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			stack := debug.Stack()
			owner.Logger().ErrorStack(
				"service lifecycle panic",
				originlog.String("service_type", fmt.Sprintf("%T", target)),
				originlog.String("phase", phase),
				originlog.String("panic", fmt.Sprint(recovered)),
				originlog.String("panic_stack", string(stack)),
			)
			result = fmt.Errorf("Service %T %s panic: %v", target, phase, recovered)
		}
	}()
	return callback()
}
