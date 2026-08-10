package network

import (
	"fmt"
	"reflect"
	"time"

	"github.com/duanhf2012/origin/v3/errs"
	"github.com/duanhf2012/origin/v3/internal/bufferpool"
)

const (
	// DefaultMaxSessions 是 Server 首轮容量验证使用的默认活动 Session 上限。
	DefaultMaxSessions = 4096
	// MaxEndpointSessions 防止错误配置建立无法验证的超大连接登记。
	MaxEndpointSessions = 65536
	// DefaultMaxMessageSize 与内部 Buffer Pool 最大池化档位一致。
	DefaultMaxMessageSize = 64 * 1024
	// DefaultReceivePendingMessages 限制单 Session 可占用的 Service 根任务数。
	DefaultReceivePendingMessages = 64
	// DefaultReceivePendingSize 是单 Session 等待处理 Buffer 的保留容量。
	DefaultReceivePendingSize int64 = 256 * 1024
	// DefaultReceivePendingTotalSize 是当前 Module 全部等待处理 Buffer 的保留容量。
	DefaultReceivePendingTotalSize int64 = 64 * 1024 * 1024
	// DefaultSendQueueMessages 是单 Session 等待发送消息数上限。
	DefaultSendQueueMessages = 256
	// DefaultSendQueueSize 是单 Session 排队 Payload 的保留容量。
	DefaultSendQueueSize int64 = 256 * 1024
	// DefaultSendQueueTotalSize 是当前 Module 排队和正在写出 Payload 的保留容量。
	DefaultSendQueueTotalSize int64 = 128 * 1024 * 1024
	// DefaultWriteTimeout 防止底层 Writer 永久阻塞。
	DefaultWriteTimeout = 15 * time.Second
	// DefaultSlowClientTimeout 限制发送队列连续高水位时间。
	DefaultSlowClientTimeout = 10 * time.Second
)

// EndpointOptions 定义三个传输能够保证完全相同语义的容量和回调。
//
// Options 在 Module OnInit 前验证并冻结。零值不是有效配置；调用方应从
// DefaultEndpointOptions 开始只修改需要的字段。
type EndpointOptions struct {
	// Handler 接收当前端点全部 Session 的业务事件。
	Handler Handler
	// MaxSessions 限制当前端点同时活动的 Session 数；Client/Dialer 必须设置为 1。
	MaxSessions int
	// MaxMessageSize 同时限制入站和出站完整逻辑消息长度。
	MaxMessageSize int
	// ReceivePendingMessages 限制每 Session 已提交但尚未返回的消息 Task 数。
	ReceivePendingMessages int
	// ReceivePendingSize 限制每 Session 待处理 Buffer 的保留容量。
	ReceivePendingSize int64
	// ReceivePendingTotalSize 限制当前端点全部待处理 Buffer 的保留容量。
	ReceivePendingTotalSize int64
	// SendQueueMessages 限制每 Session 等待发送的完整消息数。
	SendQueueMessages int
	// SendQueueSize 限制每 Session 排队 Payload 的保留容量。
	SendQueueSize int64
	// SendQueueTotalSize 限制当前端点排队和正在写出 Payload 的保留容量。
	SendQueueTotalSize int64
	// ReadIdleTimeout 是读取一条完整消息的空闲上限；零表示关闭。
	ReadIdleTimeout time.Duration
	// WriteTimeout 是写出一条完整消息的强制上限。
	WriteTimeout time.Duration
	// SlowClientTimeout 是发送队列连续高水位的最长时间。
	SlowClientTimeout time.Duration
}

// DefaultEndpointOptions 返回完整、安全且有界的首轮默认配置。
func DefaultEndpointOptions(handler Handler) EndpointOptions {
	return EndpointOptions{
		Handler:                 handler,
		MaxSessions:             DefaultMaxSessions,
		MaxMessageSize:          DefaultMaxMessageSize,
		ReceivePendingMessages:  DefaultReceivePendingMessages,
		ReceivePendingSize:      DefaultReceivePendingSize,
		ReceivePendingTotalSize: DefaultReceivePendingTotalSize,
		SendQueueMessages:       DefaultSendQueueMessages,
		SendQueueSize:           DefaultSendQueueSize,
		SendQueueTotalSize:      DefaultSendQueueTotalSize,
		ReadIdleTimeout:         0,
		WriteTimeout:            DefaultWriteTimeout,
		SlowClientTimeout:       DefaultSlowClientTimeout,
	}
}

// Validate 校验 EndpointOptions 能否形成完整、有界且内部一致的运行策略。
func (options EndpointOptions) Validate() error {
	// Handler 是事件和 Buffer 生命周期的终点，typed nil 同样不能接受。
	if options.Handler == nil || isNilHandler(options.Handler) {
		return invalidConfig("network.handler 不能为空")
	}
	if options.MaxSessions <= 0 || options.MaxSessions > MaxEndpointSessions {
		return invalidConfig(fmt.Sprintf(
			"network.max_sessions 必须在 1 到 %d 之间",
			MaxEndpointSessions,
		))
	}
	if options.MaxMessageSize <= 0 {
		return invalidConfig("network.max_message_size 必须大于零")
	}

	// 入站同时需要消息数、单 Session 字节和端点总字节三层明确上限。
	if options.ReceivePendingMessages <= 0 {
		return invalidConfig("network.receive_pending_messages 必须大于零")
	}
	retainedMessageSize := int64(bufferpool.RetainedCapacity(options.MaxMessageSize))
	if options.ReceivePendingSize < retainedMessageSize {
		return invalidConfig("network.receive_pending_size 不能小于 max_message_size 的 Buffer 保留容量")
	}
	if options.ReceivePendingTotalSize < options.ReceivePendingSize {
		return invalidConfig("network.receive_pending_total_size 不能小于 receive_pending_size")
	}

	// 出站配置同样拒绝“消息合法但永远无法进入队列”的组合。
	if options.SendQueueMessages <= 0 {
		return invalidConfig("network.send_queue_messages 必须大于零")
	}
	if options.SendQueueSize < retainedMessageSize {
		return invalidConfig("network.send_queue_size 不能小于 max_message_size 的 Buffer 保留容量")
	}
	if options.SendQueueTotalSize < options.SendQueueSize {
		return invalidConfig("network.send_queue_total_size 不能小于 send_queue_size")
	}

	// 读空闲允许显式关闭；Writer 和慢连接裁决必须始终有正数时间边界。
	if options.ReadIdleTimeout < 0 {
		return invalidConfig("network.read_idle_timeout 不能为负数")
	}
	if options.WriteTimeout <= 0 {
		return invalidConfig("network.write_timeout 必须大于零")
	}
	if options.SlowClientTimeout <= 0 {
		return invalidConfig("network.slow_client_timeout 必须大于零")
	}
	return nil
}

// isNilHandler 检测接口中保存的 typed nil，避免首次回调才发生 panic。
func isNilHandler(handler Handler) bool {
	value := reflect.ValueOf(handler)
	switch value.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return value.IsNil()
	default:
		return false
	}
}

// invalidConfig 统一网络公共配置的稳定错误码和公开说明。
func invalidConfig(message string) error {
	return errs.NewMessage(errs.CodeInvalidConfig, message)
}
