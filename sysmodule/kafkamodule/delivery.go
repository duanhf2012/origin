package kafkamodule

import (
	"context"
	"sync"
)

// Delivery 表示受管 Producer 已接受的一条消息的最终 Broker 结果。
// Wait 的 Context 取消只停止当前调用方等待，不取消或撤回 Kafka 消息。
type Delivery struct {
	done   chan struct{}
	once   sync.Once
	mutex  sync.RWMutex
	result DeliveryResult
}

func newDelivery() *Delivery {
	return &Delivery{done: make(chan struct{})}
}

func (delivery *Delivery) complete(result DeliveryResult) {
	if delivery == nil {
		return
	}
	delivery.once.Do(func() {
		delivery.mutex.Lock()
		delivery.result = result
		delivery.mutex.Unlock()
		close(delivery.done)
	})
}

// Wait 等待 Delivery 完成并返回 Metadata 或最终错误；ctx 不能为空。
func (delivery *Delivery) Wait(ctx context.Context) (Metadata, error) {
	if delivery == nil || ctx == nil {
		return Metadata{}, invalidArgument("kafkamodule Delivery 和 Context 不能为空")
	}
	select {
	case <-delivery.done:
		result, _ := delivery.Result()
		return result.Metadata, result.Err
	case <-ctx.Done():
		return Metadata{}, ctx.Err()
	}
}

// Done 返回只读完成 Channel，便于 select；调用方不得关闭它。nil Delivery 返回 nil。
func (delivery *Delivery) Done() <-chan struct{} {
	if delivery == nil {
		return nil
	}
	return delivery.done
}

// Result 非阻塞读取不可变结果；尚未完成或 Delivery 为 nil 时返回 false。
func (delivery *Delivery) Result() (DeliveryResult, bool) {
	if delivery == nil {
		return DeliveryResult{}, false
	}
	select {
	case <-delivery.done:
		delivery.mutex.RLock()
		result := delivery.result
		delivery.mutex.RUnlock()
		return result, true
	default:
		return DeliveryResult{}, false
	}
}
