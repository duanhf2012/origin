package kafkamodule

import "github.com/duanhf2012/origin/v3/errs"

var (
	// ErrInvalidConfig 表示 Kafka 配置或 Sarama Hook 结果违反了必需约束。
	ErrInvalidConfig = errs.ErrInvalidConfig
	// ErrInvalidArgument 表示调用参数无效。
	ErrInvalidArgument = errs.ErrInvalidArgument
	// ErrNotSetup 表示 Module 尚未完成 Setup。
	ErrNotSetup = errs.NewMessage(errs.CodeInvalidConfig, "kafkamodule 尚未完成配置")
	// ErrAlreadySetup 表示同一个 Module 被重复 Setup。
	ErrAlreadySetup = errs.NewMessage(errs.CodeInvalidArgument, "kafkamodule 只能配置一次")
	// ErrNotRunning 表示 Module 尚未启动、启动失败、正在停止或已停止。
	ErrNotRunning = errs.NewMessage(errs.CodeServiceNotReady, "kafkamodule 尚未运行")
)

func invalidConfig(message string) error {
	return errs.NewMessage(errs.CodeInvalidConfig, message)
}

func invalidArgument(message string) error {
	return errs.NewMessage(errs.CodeInvalidArgument, message)
}
