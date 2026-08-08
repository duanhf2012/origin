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

// StartWithModules 先进入 Service.OnStart，再按静态树登记顺序依次启动全部 Module。
// Service 是 Module 使用调度、Timer、Await、配置和共享资源的生命周期父级，因此必须先启动；
// 任一回调返回错误后，调用方通过 StopWithModules 严格反序回滚已经进入过 OnStart 的对象。
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

	// 先记录 Service 已经进入 OnStart，确保回调即使返回错误，回滚也会调用一次
	// Service.OnStop 清理已经交给 Service 持有的资源。
	base.bindMu.Lock()
	base.serviceStartEntered = true
	base.bindMu.Unlock()
	if err := target.OnStart(ctx); err != nil {
		return err
	}

	// Service 启动成功后再按父先子后、同级添加顺序启动 Module。started 在调用前写入，
	// 使部分启动后返回错误或 panic 的 Module 也能在独立停止路径中得到一次 OnStop。
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
	return nil
}

// StopWithModules 先按严格逆序停止已启动 Module，再停止已经进入 OnStart 的 Service。
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
	// Module 按实际启动顺序严格反向释放，使子 Module、后登记 Module 先停止，并保证它们
	// 的 OnStop 仍可使用尚未关闭的 Service 级能力。单个回调失败不能跳过余下对象。
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

	// 全部 Module 都完成停止尝试和作用域清理后，Service 最后汇总结果并关闭共享资源。
	// 即使某个 Module 返回错误或 panic，Service.OnStop 仍必须获得一次执行机会。
	if serviceEntered {
		result = errors.Join(result, callServiceLifecycle(base, target, "on_stop", func() error {
			return target.OnStop(ctx)
		}))
	}
	return result
}
