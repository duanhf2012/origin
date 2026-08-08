package log

import "github.com/duanhf2012/origin/v3/errs"

// OutputStatus 是一个日志输出端的不可变运行状态。
type OutputStatus struct {
	// Available 表示启动配置已经为该输出端建立底层资源。
	Available bool
	// Enabled 表示当前是否接收新日志；暂停不会释放底层资源。
	Enabled bool
	// Level 是当前最低日志级别。
	Level Level
	// ConfigLevel 是 Reset 将恢复的启动配置级别。
	ConfigLevel Level
}

// Status 是 Console 与 File 两个输出端的一次状态快照。
type Status struct {
	Console OutputStatus
	File    OutputStatus
}

// Controller 是可选的日志输出运行时控制边界。
//
// Handler 只需要继续实现固定写入契约；需要支持包级运行时控制时再额外实现本接口。
// 所有方法都必须支持并发调用，且不能重建 Runtime 或日志队列。
type Controller interface {
	SetConsoleLevel(level Level) error
	ResetConsoleLevel() error
	SetFileLevel(level Level) error
	ResetFileLevel() error
	SetConsoleEnabled(enabled bool) error
	SetFileEnabled(enabled bool) error
	Status() Status
}

// SetConsoleLevel 修改当前进程默认 Application 的控制台最低级别。
func SetConsoleLevel(level Level) error {
	controller, err := currentController()
	if err != nil {
		return err
	}
	return controller.SetConsoleLevel(level)
}

// ResetConsoleLevel 恢复控制台启动配置中的最低级别。
func ResetConsoleLevel() error {
	controller, err := currentController()
	if err != nil {
		return err
	}
	return controller.ResetConsoleLevel()
}

// SetFileLevel 修改当前进程默认 Application 的文件最低级别。
func SetFileLevel(level Level) error {
	controller, err := currentController()
	if err != nil {
		return err
	}
	return controller.SetFileLevel(level)
}

// ResetFileLevel 恢复文件输出启动配置中的最低级别。
func ResetFileLevel() error {
	controller, err := currentController()
	if err != nil {
		return err
	}
	return controller.ResetFileLevel()
}

// SetConsoleEnabled 暂停或恢复当前进程默认 Application 的控制台输出。
func SetConsoleEnabled(enabled bool) error {
	controller, err := currentController()
	if err != nil {
		return err
	}
	return controller.SetConsoleEnabled(enabled)
}

// SetFileEnabled 暂停或恢复当前进程默认 Application 的文件输出。
func SetFileEnabled(enabled bool) error {
	controller, err := currentController()
	if err != nil {
		return err
	}
	return controller.SetFileEnabled(enabled)
}

// CurrentStatus 返回当前进程默认 Application 的日志输出状态。
func CurrentStatus() (Status, error) {
	state := processDefault.Load()
	if state == nil || state.owner == nil {
		return Status{}, errs.ErrLogClosed
	}
	return state.owner.OutputStatus()
}

// currentController 定位当前默认 Runtime 的可选控制面，并统一生命周期和不支持错误。
func currentController() (Controller, error) {
	state := processDefault.Load()
	if state == nil || state.owner == nil || state.owner.state.Load() != runtimeRunning {
		return nil, errs.ErrLogClosed
	}
	if state.owner.controller == nil {
		return nil, errs.ErrLogControlUnsupported
	}
	return state.owner.controller, nil
}
