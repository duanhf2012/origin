package natsnet

import (
	"context"
	"errors"
	"fmt"
	"runtime/debug"
	"strings"

	"github.com/duanhf2012/origin/v3/errs"
	"github.com/nats-io/nats.go"
)

// invalidArgument 创建 NATS 公共调用参数错误。
func invalidArgument(message string) error {
	// 参数错误属于调用方可以立即修复的问题，保留不含凭据的简短说明。
	return errs.NewMessage(errs.CodeInvalidArgument, message)
}

// invalidConfig 创建 NATS 启动配置错误。
func invalidConfig(message string) error {
	// 配置在创建网络资源前统一失败，不让半初始化连接进入运行状态。
	return errs.NewMessage(errs.CodeInvalidConfig, message)
}

// mapError 把官方客户端和 Context 错误转换为 Origin 稳定错误码。
func mapError(err error) error {
	// nil 是唯一成功结果，不创建包装对象。
	if err == nil {
		return nil
	}

	// 已有 Origin 错误保持原分类和错误链。
	var coder errs.Coder
	if errors.As(err, &coder) {
		return err
	}

	// Context 取消和 Deadline 必须优先于一般 Transport 分类。
	switch {
	case errors.Is(err, context.Canceled):
		return errs.Wrap(errs.CodeCanceled, err)
	case errors.Is(err, context.DeadlineExceeded),
		errors.Is(err, nats.ErrTimeout),
		errors.Is(err, nats.ErrDrainTimeout):
		return errs.Wrap(errs.CodeDeadlineExceeded, err)
	}

	// 已关闭和 Drain 中都表示当前本地 Transport 不再接受新工作。
	switch {
	case errors.Is(err, nats.ErrConnectionClosed),
		errors.Is(err, nats.ErrConnectionDraining),
		errors.Is(err, nats.ErrBadSubscription):
		return errs.Wrap(errs.CodeTransportClosed, err)
	}

	// 重连缓冲和慢消费者 Pending 溢出统一映射为有界过载。
	switch {
	case errors.Is(err, nats.ErrReconnectBufExceeded),
		errors.Is(err, nats.ErrSlowConsumer):
		return errs.Wrap(errs.CodeTransportOverloaded, err)
	}

	// 本地或 Server 报告的 payload 上限使用统一消息过大错误。
	if errors.Is(err, nats.ErrMaxPayload) {
		return errs.Wrap(errs.CodeTransportMessageTooLarge, err)
	}

	// Subject、Queue 和 Context 形状错误属于调用参数问题。
	switch {
	case errors.Is(err, nats.ErrBadSubject),
		errors.Is(err, nats.ErrBadQueueName),
		errors.Is(err, nats.ErrInvalidArg),
		errors.Is(err, nats.ErrInvalidContext),
		errors.Is(err, nats.ErrNoDeadlineContext):
		return errs.Wrap(errs.CodeInvalidArgument, err)
	}

	// 明确的 NATS 协议解析失败与普通网络不可用分开报告。
	switch {
	case errors.Is(err, nats.ErrNoInfoReceived),
		errors.Is(err, nats.ErrJsonParse),
		errors.Is(err, nats.ErrBadHeaderMsg):
		return errs.Wrap(errs.CodeTransportProtocol, err)
	}

	// 其余认证、TLS、无 Server、断线和 socket 错误都表示当前传输不可用。
	return errs.Wrap(errs.CodeTransportUnavailable, err)
}

// panicError 把 Handler 或 EventHandler panic 转换为带现场堆栈的内部错误。
func panicError(scope string, value any) error {
	// debug.Stack 必须在 recover 所在 defer 中调用，才能保留真实 panic 现场。
	cause := fmt.Errorf("%s panic: %v\n%s", scope, value, debug.Stack())
	return errs.Wrap(errs.CodeInternal, cause)
}

// redactedError 保留原始错误链，同时只输出已经脱敏的文本。
type redactedError struct {
	cause error
	text  string
}

// Error 返回不会暴露密码、Token 或完整认证 URL 的错误文本。
func (err *redactedError) Error() string {
	return err.text
}

// Unwrap 允许 errors.Is/As 继续识别官方客户端的原始错误。
func (err *redactedError) Unwrap() error {
	return err.cause
}

// redactCause 把配置中可能出现的秘密从错误字符串中移除。
func redactCause(err error, options Options) error {
	// 没有错误时无需建立脱敏包装。
	if err == nil {
		return nil
	}
	text := err.Error()

	// URL 先整体替换为移除 UserInfo 和 Query 后的地址。
	for _, rawURL := range options.URLs {
		text = strings.ReplaceAll(text, rawURL, safeURL(rawURL))
	}
	// 独立秘密也可能由底层错误单独输出，逐项替换为固定标记。
	secrets := []string{
		options.Auth.Password,
		options.Auth.Token,
	}
	for _, secret := range secrets {
		if secret != "" {
			text = strings.ReplaceAll(text, secret, "[REDACTED]")
		}
	}
	if text == err.Error() {
		// 文本未包含任何敏感配置时保留原对象，减少正常错误路径分配。
		return err
	}
	return &redactedError{cause: err, text: text}
}
