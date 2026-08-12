package kafkamodule

import (
	"errors"
	"fmt"
	"strings"

	"github.com/duanhf2012/origin/v3/errs"
)

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
	// ErrClosed 表示已经接受的消息因 Producer 关闭且未获得 Broker 结果而失败。
	ErrClosed = errors.New("kafkamodule: closed before delivery")
)

// BatchFailure 描述批量输入中一个失败项，不包含消息 Payload。
type BatchFailure struct {
	// Index 是失败项在调用方输入切片中的位置。
	Index int
	// Topic 是失败项的目标 Topic；输入校验失败时也可能为空。
	Topic string
	// Partition 是 Broker 返回的分区；尚未投递时通常为 -1。
	Partition int32
	// Err 是该项未被接受或投递失败的原因。
	Err error
}

// BatchError 汇总批量操作的部分接受数量和逐项错误。
type BatchError struct {
	// Accepted 是本次调用中已经被 Producer 接受并取得所有权的消息数。
	Accepted int
	// Failures 按输入位置记录未被接受或最终发送失败的项。
	Failures []BatchFailure
}

// Error 返回不包含 Key、Value 和 Header Value 的脱敏摘要。
func (batch *BatchError) Error() string {
	if batch == nil {
		return "kafkamodule: batch failed"
	}
	parts := make([]string, 0, len(batch.Failures))
	for _, failure := range batch.Failures {
		parts = append(parts, fmt.Sprintf("index=%d topic=%q partition=%d: %v", failure.Index, failure.Topic, failure.Partition, failure.Err))
	}
	return fmt.Sprintf("kafkamodule: batch accepted=%d failures=[%s]", batch.Accepted, strings.Join(parts, "; "))
}

// Unwrap 返回全部逐项错误，使 errors.Is/As 可以检查原始原因。
func (batch *BatchError) Unwrap() []error {
	if batch == nil {
		return nil
	}
	result := make([]error, 0, len(batch.Failures))
	for _, failure := range batch.Failures {
		if failure.Err != nil {
			result = append(result, failure.Err)
		}
	}
	return result
}

func invalidConfig(message string) error {
	return errs.NewMessage(errs.CodeInvalidConfig, message)
}

func invalidArgument(message string) error {
	return errs.NewMessage(errs.CodeInvalidArgument, message)
}
