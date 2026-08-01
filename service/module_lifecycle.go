package service

import (
	"context"
	"errors"

	"github.com/duanhf2012/origin/v3/errs"
)

// BeginModuleInitialization 在 Node 调用 Service.OnInit 前开放同步 Module 注册栈。
func BeginModuleInitialization(target IService) error {
	if target == nil || isNilService(target) {
		return errs.ErrInvalidArgument
	}
	base := target.baseService()
	if base == nil || base.runtime == nil || base.State() != StateInitializing {
		return errs.ErrInvalidArgument
	}
	base.bindMu.Lock()
	defer base.bindMu.Unlock()
	if base.moduleInitActive || base.moduleSealed || base.moduleTarget != nil {
		return errs.ErrInvalidArgument
	}
	base.moduleTarget = target
	base.moduleInitActive = true
	return nil
}

// CompleteModuleInitialization 关闭 Module 注册栈，并返回即使 Service 忽略也不能丢失的
// Module 初始化错误。serviceSucceeded 为 false 时立即释放所有已登记 scope，且不执行 OnStop。
func CompleteModuleInitialization(target IService, serviceSucceeded bool) error {
	if target == nil || isNilService(target) {
		return errs.ErrInvalidArgument
	}
	base := target.baseService()
	if base == nil {
		return errs.ErrInvalidArgument
	}
	base.bindMu.Lock()
	if !base.moduleInitActive {
		base.bindMu.Unlock()
		return errs.ErrInvalidArgument
	}
	base.moduleInitActive = false
	base.moduleSealed = true
	moduleErr := base.moduleInitErr
	modules := base.modules
	base.bindMu.Unlock()
	if !serviceSucceeded || moduleErr != nil {
		for index := len(modules) - 1; index >= 0; index-- {
			modules[index].base.cleanupScope()
		}
	}
	return moduleErr
}

// StartWithModules 依次启动全部 Module，成功后才进入 Service.OnStart。
func StartWithModules(ctx context.Context, target IService) error {
	if ctx == nil || target == nil || isNilService(target) {
		return errs.ErrInvalidArgument
	}
	base := target.baseService()
	if base == nil {
		return errs.ErrInvalidArgument
	}
	base.bindMu.Lock()
	if !base.moduleSealed || base.moduleInitErr != nil {
		base.bindMu.Unlock()
		return errors.Join(errs.ErrInvalidArgument, base.moduleInitErr)
	}
	modules := base.modules
	base.bindMu.Unlock()

	for _, entry := range modules {
		if !entry.initialized {
			continue
		}
		entry.started = true
		if err := callModuleLifecycle(base, entry.target, "on_start", func() error {
			return entry.target.OnStart(ctx)
		}); err != nil {
			return err
		}
	}
	base.bindMu.Lock()
	base.serviceStartEntered = true
	base.bindMu.Unlock()
	return target.OnStart(ctx)
}

// StopWithModules 先停止已经进入 OnStart 的 Service，再按严格逆序停止已启动 Module。
// 每个回调的 error 或 panic 都被聚合，后续资源清理始终继续。
func StopWithModules(ctx context.Context, target IService) error {
	if ctx == nil || target == nil || isNilService(target) {
		return errs.ErrInvalidArgument
	}
	base := target.baseService()
	if base == nil {
		return errs.ErrInvalidArgument
	}
	base.bindMu.Lock()
	serviceEntered := base.serviceStartEntered
	base.serviceStartEntered = false
	modules := base.modules
	base.bindMu.Unlock()

	var result error
	if serviceEntered {
		result = errors.Join(result, callModuleLifecycle(base, nil, "service_on_stop", func() error {
			return target.OnStop(ctx)
		}))
	}
	for index := len(modules) - 1; index >= 0; index-- {
		entry := modules[index]
		if entry.started && !entry.stopped {
			entry.stopped = true
			result = errors.Join(result, callModuleLifecycle(base, entry.target, "on_stop", func() error {
				return entry.target.OnStop(ctx)
			}))
		}
		entry.base.cleanupScope()
	}
	return result
}
