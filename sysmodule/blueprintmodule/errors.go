package blueprintmodule

import "github.com/duanhf2012/origin/v3/errs"

var (
	// ErrInvalidArgument 表示方法参数或调用环境无效。
	ErrInvalidArgument = errs.ErrInvalidArgument
	// ErrInvalidConfig 表示 Blueprint Module 配置或构造选项无效。
	ErrInvalidConfig = errs.ErrInvalidConfig
	// ErrNotSetup 表示 Module 尚未通过 New 或 Setup 冻结配置。
	ErrNotSetup = errs.NewMessage(errs.CodeInvalidConfig, "blueprintmodule 尚未完成配置")
	// ErrAlreadySetup 表示同一 Module 已经冻结配置，不能再次配置。
	ErrAlreadySetup = errs.NewMessage(errs.CodeInvalidArgument, "blueprintmodule 只能配置一次")
	// ErrNotRunning 表示 Module 未启动、启动失败、正在停止或已经停止。
	ErrNotRunning = errs.NewMessage(errs.CodeServiceNotReady, "blueprintmodule 尚未运行")
	// ErrInstanceClosed 表示 Instance 已经关闭，不能开始新的执行。
	ErrInstanceClosed = errs.NewMessage(errs.CodeInvalidArgument, "blueprintmodule Instance 已关闭")
	// ErrReloadInProgress 表示当前 Module 已有一项热加载事务正在执行。
	ErrReloadInProgress = errs.NewMessage(errs.CodeInvalidArgument, "blueprintmodule 热加载正在进行")
)

func invalidConfig(message string) error {
	return errs.NewMessage(errs.CodeInvalidConfig, message)
}

func invalidArgument(message string) error {
	return errs.NewMessage(errs.CodeInvalidArgument, message)
}
