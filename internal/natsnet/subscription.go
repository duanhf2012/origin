package natsnet

import (
	"context"
	"sync"
	"sync/atomic"

	"github.com/duanhf2012/origin/v3/errs"
	originlog "github.com/duanhf2012/origin/v3/log"
	"github.com/nats-io/nats.go"
)

const (
	// subscriptionActive 表示订阅仍然接受新投递。
	subscriptionActive uint32 = iota
	// subscriptionDraining 表示订阅已经停止新投递并等待 Pending 回调完成。
	subscriptionDraining
	// subscriptionClosed 表示订阅已经终止。
	subscriptionClosed
)

// Subscription 包装一条普通异步订阅或 Queue Group 订阅。
//
// Subscription 由所属 Conn 最终持有，也可以先于 Conn 单独 Close 或 Drain。MessageHandler
// 在官方客户端回调 goroutine 中执行，包装层不再创建每消息 goroutine。
type Subscription struct {
	conn    *Conn
	raw     *nats.Subscription
	subject string
	queue   string
	handler MessageHandler

	state atomic.Uint32

	// finishOnce 统一取消登记并完成所有 Drain 等待者。
	finishOnce sync.Once
	done       chan struct{}

	// resultMu 保护 Drain 首个终止结果；nil 表示正常排空。
	resultMu  sync.Mutex
	resultSet bool
	result    error

	// overloadLogged 保证同一订阅的持续慢消费者只记录一次日志。
	overloadLogged atomic.Bool
}

// newSubscription 创建已经完成官方订阅和 Pending 设置的包装对象。
func newSubscription(
	conn *Conn,
	raw *nats.Subscription,
	subject string,
	queue string,
	handler MessageHandler,
) *Subscription {
	// 字段在登记到 Conn 前全部确定，后续回调只读取不可变值。
	subscription := &Subscription{
		conn:    conn,
		raw:     raw,
		subject: subject,
		queue:   queue,
		handler: handler,
		done:    make(chan struct{}),
	}
	subscription.state.Store(subscriptionActive)
	return subscription
}

// startMonitor 启动一条由 Subscription 明确拥有的关闭状态观察 goroutine。
func (subscription *Subscription) startMonitor() {
	// nats.go 会在 Unsubscribe、Drain 或 Connection Close 后关闭状态 Channel；观察者
	// 只等待 SubscriptionClosed，随后 finish 并退出，不形成 fire-and-forget 资源。
	changes := subscription.raw.StatusChanged(nats.SubscriptionClosed)
	go func() {
		for status := range changes {
			if status == nats.SubscriptionClosed {
				subscription.finish()
				return
			}
		}
		// Channel 关闭本身也意味着官方 Subscription 已经结束。
		subscription.finish()
	}()
}

// Subject 返回创建订阅时保存的 Subject。
func (subscription *Subscription) Subject() string {
	// Subject 是不可变字符串，可以无锁直接返回。
	return subscription.subject
}

// Queue 返回 Queue Group；普通订阅返回空字符串。
func (subscription *Subscription) Queue() string {
	// Queue 是不可变字符串，可以无锁直接返回。
	return subscription.queue
}

// Stats 返回当前 Pending 消息数和累计丢弃数。
func (subscription *Subscription) Stats() SubscriptionStats {
	// 官方 API 在订阅关闭后返回 ErrBadSubscription；统计快照使用零值而不向日志制造错误。
	pendingMessages, _, pendingErr := subscription.raw.Pending()
	if pendingErr != nil {
		pendingMessages = 0
	}
	dropped, droppedErr := subscription.raw.Dropped()
	if droppedErr != nil {
		dropped = 0
	}
	return SubscriptionStats{
		PendingMessages: pendingMessages,
		DroppedMessages: dropped,
	}
}

// Drain 停止新投递并等待已经 Pending 的 Handler 全部完成。
func (subscription *Subscription) Drain(ctx context.Context) error {
	// nil Context 无法形成等待退出条件，必须明确拒绝。
	if ctx == nil {
		return invalidArgument("natsnet: Subscription Drain Context 不能为空")
	}

	// 第一个 Drain 调用负责向官方客户端发起排空；重复调用只等待同一个 done。
	first := subscription.state.CompareAndSwap(subscriptionActive, subscriptionDraining)
	if first {
		if err := subscription.raw.Drain(); err != nil {
			mapped := mapError(redactCause(err, subscription.conn.options))
			subscription.setResult(mapped)
			subscription.Close()
			return mapped
		}
	} else if subscription.state.Load() == subscriptionClosed {
		return subscription.finalResult()
	}

	// 单订阅 Drain 使用与 Connection 操作相同的默认保底和更早调用方 Deadline。
	operationCtx, cancel := boundedContext(
		ctx,
		subscription.conn.options.DefaultOperationTimeout,
	)
	defer cancel()

	select {
	case <-subscription.done:
		return subscription.finalResult()
	case <-operationCtx.Done():
		mapped := mapError(operationCtx.Err())
		subscription.setResult(mapped)
		subscription.Close()
		return mapped
	}
}

// Close 幂等地立即注销订阅，不等待 Pending Handler。
func (subscription *Subscription) Close() {
	// 状态先切换为 Closed，使并发 Drain 和回调立即观察到终态。
	previous := subscription.state.Swap(subscriptionClosed)
	if previous == subscriptionClosed {
		return
	}
	if err := subscription.raw.Unsubscribe(); err != nil &&
		!natsSubscriptionAlreadyClosed(err) {
		mapped := mapError(redactCause(err, subscription.conn.options))
		subscription.conn.logger.Warn(
			"关闭 NATS Subscription 失败",
			originlog.String("subject", subscription.subject),
			originlog.String("queue", subscription.queue),
			originlog.Err(mapped),
		)
	}
	subscription.finish()
}

// deliver 验证消息边界并安全调用用户 Handler。
func (subscription *Subscription) deliver(raw *nats.Msg) {
	// Connection Drain 期间官方客户端仍会投递已经 Pending 的消息；只有立即关闭才跳过。
	if subscription.state.Load() == subscriptionClosed {
		return
	}
	if len(raw.Data) > subscription.conn.options.MaxMessageSize {
		subscription.conn.reportHandlerError(
			raw.Subject,
			errs.ErrTransportMessageTooLarge,
		)
		return
	}

	// Handler panic 只丢弃当前消息；debug.Stack 在 recover 现场生成并通过异步事件报告。
	defer func() {
		if value := recover(); value != nil {
			subscription.conn.reportHandlerError(
				raw.Subject,
				panicError("natsnet MessageHandler", value),
			)
		}
	}()
	subscription.handler(Message{
		Subject: raw.Subject,
		Data:    raw.Data,
	})
}

// finish 完成包装订阅终态并从所属 Connection 移除登记。
func (subscription *Subscription) finish() {
	// 官方状态观察、主动 Close 和 Connection Close 可能并发到达，只清理一次。
	subscription.finishOnce.Do(func() {
		subscription.state.Store(subscriptionClosed)
		subscription.conn.unregisterSubscription(subscription.raw)
		close(subscription.done)
	})
}

// markOverloadLogged 报告当前调用是否是第一次慢消费者告警。
func (subscription *Subscription) markOverloadLogged() bool {
	// CompareAndSwap 返回 true 表示当前调用取得唯一记录权。
	return subscription.overloadLogged.CompareAndSwap(false, true)
}

// setResult 只保存 Drain 的第一个有效结束原因。
func (subscription *Subscription) setResult(result error) {
	subscription.resultMu.Lock()
	if !subscription.resultSet {
		subscription.resultSet = true
		subscription.result = result
	}
	subscription.resultMu.Unlock()
}

// finalResult 返回 Subscription Drain 的稳定结果。
func (subscription *Subscription) finalResult() error {
	subscription.resultMu.Lock()
	defer subscription.resultMu.Unlock()

	// 没有错误被提交表示正常 Drain 或幂等重复 Drain。
	return subscription.result
}

// natsSubscriptionAlreadyClosed 报告关闭竞态中可以忽略的官方错误。
func natsSubscriptionAlreadyClosed(err error) bool {
	// Connection Close 会先使 raw Subscription 失效，再由包装层完成 Close。
	return err == nats.ErrBadSubscription ||
		err == nats.ErrConnectionClosed ||
		err == nats.ErrConnectionDraining
}
