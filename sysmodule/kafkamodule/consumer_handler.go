package kafkamodule

import (
	"context"
	"time"

	"github.com/IBM/sarama"
	"github.com/duanhf2012/origin/v3/errs"
	"github.com/duanhf2012/origin/v3/service"
)

type managedGroupHandler struct {
	owner        service.IService
	config       ConsumerConfig
	handler      Handler
	batchHandler BatchHandler
	consumer     *Consumer
	ready        chan struct{}
}

func newManagedGroupHandler(owner service.IService, current ConsumerConfig, handler Handler, batchHandler BatchHandler, consumer *Consumer) *managedGroupHandler {
	return &managedGroupHandler{owner: owner, config: current, handler: handler, batchHandler: batchHandler, consumer: consumer, ready: make(chan struct{}, 1)}
}

func (handler *managedGroupHandler) Setup(sarama.ConsumerGroupSession) error {
	if handler.consumer != nil {
		handler.consumer.rebalances.Add(1)
	}
	select {
	case handler.ready <- struct{}{}:
	default:
	}
	return nil
}

func (handler *managedGroupHandler) Cleanup(sarama.ConsumerGroupSession) error { return nil }

func (handler *managedGroupHandler) ConsumeClaim(session sarama.ConsumerGroupSession, claim sarama.ConsumerGroupClaim) error {
	if session == nil || claim == nil || handler.owner == nil || handler.consumer == nil {
		return ErrInvalidArgument
	}
	// Sarama 的 Pause 状态只作用于当前已经创建的 Partition Consumer。Claim 在 Setup 后才创建，
	// 因此必须在每个 Claim 入口重放 Module 保存的暂停意图，覆盖启动与 Rebalance 窗口。
	handler.consumer.applyDesiredPause()
	if handler.batchHandler != nil {
		return handler.consumeBatch(session, claim)
	}
	for {
		select {
		case <-session.Context().Done():
			return nil
		case raw, open := <-claim.Messages():
			if !open {
				return nil
			}
			if raw == nil {
				continue
			}
			handler.consumer.received.Add(1)
			message := fromSaramaConsumerMessage(raw, claim.HighWaterMarkOffset())
			err := handler.dispatch(session.Context(), func(ctx context.Context) error { return handler.invokeSingle(ctx, message) })
			if err != nil {
				if session.Context().Err() != nil {
					return nil
				}
				handler.consumer.failed.Add(1)
				handler.consumer.stopWithError(err)
				return err
			}
			if session.Context().Err() != nil {
				return nil
			}
			session.MarkMessage(raw, "")
			handler.consumer.handled.Add(1)
		}
	}
}

func (handler *managedGroupHandler) consumeBatch(session sarama.ConsumerGroupSession, claim sarama.ConsumerGroupClaim) error {
	type batchItem struct {
		raw     *sarama.ConsumerMessage
		message *Message
	}
	items := make([]batchItem, 0, handler.config.Batch.MaxMessages)
	var bytes int64
	var timer *time.Timer
	var timerChannel <-chan time.Time
	stopTimer := func() {
		if timer == nil {
			return
		}
		if !timer.Stop() {
			select {
			case <-timer.C:
			default:
			}
		}
		timer = nil
		timerChannel = nil
	}
	defer stopTimer()
	flush := func() error {
		if len(items) == 0 {
			return nil
		}
		messages := make([]*Message, len(items))
		for index := range items {
			messages[index] = items[index].message
		}
		batch := Batch{Topic: claim.Topic(), Partition: claim.Partition(), Messages: messages}
		err := handler.dispatch(session.Context(), func(ctx context.Context) error { return handler.invokeBatch(ctx, batch) })
		if err != nil {
			if session.Context().Err() != nil {
				return nil
			}
			handler.consumer.failed.Add(uint64(len(items)))
			handler.consumer.stopWithError(err)
			return err
		}
		if session.Context().Err() != nil {
			return nil
		}
		for _, item := range items {
			session.MarkMessage(item.raw, "")
		}
		handler.consumer.handled.Add(uint64(len(items)))
		handler.consumer.batches.Add(1)
		items = items[:0]
		bytes = 0
		stopTimer()
		return nil
	}

	for {
		select {
		case <-session.Context().Done():
			return nil
		case <-timerChannel:
			if err := flush(); err != nil {
				return err
			}
		case raw, open := <-claim.Messages():
			if !open {
				if session.Context().Err() != nil {
					return nil
				}
				return flush()
			}
			if raw == nil {
				continue
			}
			messageSize := int64(len(raw.Key)) + int64(len(raw.Value))
			for _, header := range raw.Headers {
				if header != nil {
					messageSize += int64(len(header.Key)) + int64(len(header.Value))
				}
			}
			if len(items) > 0 && (len(items) >= handler.config.Batch.MaxMessages || bytes > handler.config.Batch.MaxSize.Bytes()-messageSize) {
				if err := flush(); err != nil {
					return err
				}
			}
			handler.consumer.received.Add(1)
			items = append(items, batchItem{raw: raw, message: fromSaramaConsumerMessage(raw, claim.HighWaterMarkOffset())})
			bytes += messageSize
			if len(items) == 1 {
				timer = time.NewTimer(handler.config.Batch.MaxWait.Duration())
				timerChannel = timer.C
			}
			if len(items) >= handler.config.Batch.MaxMessages || bytes >= handler.config.Batch.MaxSize.Bytes() {
				if err := flush(); err != nil {
					return err
				}
			}
		}
	}
}

func (handler *managedGroupHandler) dispatch(sessionCtx context.Context, invoke func(context.Context) error) error {
	result := make(chan error, 1)
	err := handler.owner.DispatchAsync(func(taskCtx context.Context) {
		if sessionCtx.Err() != nil {
			result <- sessionCtx.Err()
			return
		}
		merged := &consumerTaskContext{execution: taskCtx, session: sessionCtx}
		result <- invoke(merged)
	})
	if err != nil {
		handler.consumer.dispatchRejected.Add(1)
		return err
	}
	select {
	case err := <-result:
		return err
	case <-sessionCtx.Done():
		return sessionCtx.Err()
	}
}

func (handler *managedGroupHandler) invokeSingle(ctx context.Context, message *Message) error {
	return handler.invokeWithRetry(ctx, func() (err error) {
		defer func() {
			if recover() != nil {
				err = errs.NewMessage(errs.CodeInternal, "kafkamodule Consumer Handler panic")
			}
		}()
		return handler.handler(ctx, message)
	})
}

func (handler *managedGroupHandler) invokeBatch(ctx context.Context, batch Batch) error {
	return handler.invokeWithRetry(ctx, func() (err error) {
		defer func() {
			if recover() != nil {
				err = errs.NewMessage(errs.CodeInternal, "kafkamodule Consumer BatchHandler panic")
			}
		}()
		return handler.batchHandler(ctx, batch)
	})
}

func (handler *managedGroupHandler) invokeWithRetry(ctx context.Context, invoke func() error) error {
	var err error
	for attempt := 0; attempt <= handler.config.HandlerRetryMax; attempt++ {
		if err = invoke(); err == nil {
			return nil
		}
		if attempt == handler.config.HandlerRetryMax {
			return err
		}
		waitErr := handler.owner.Await(ctx, func(waitCtx context.Context) error {
			timer := time.NewTimer(handler.config.HandlerRetryBackoff.Duration())
			defer timer.Stop()
			select {
			case <-timer.C:
				return nil
			case <-waitCtx.Done():
				return waitCtx.Err()
			}
		})
		if waitErr != nil {
			return waitErr
		}
	}
	return err
}

type consumerTaskContext struct {
	execution context.Context
	session   context.Context
}

func (ctx *consumerTaskContext) Deadline() (time.Time, bool) { return ctx.session.Deadline() }
func (ctx *consumerTaskContext) Done() <-chan struct{}       { return ctx.session.Done() }
func (ctx *consumerTaskContext) Err() error                  { return ctx.session.Err() }
func (ctx *consumerTaskContext) Value(key any) any {
	if value := ctx.execution.Value(key); value != nil {
		return value
	}
	return ctx.session.Value(key)
}

func fromSaramaConsumerMessage(raw *sarama.ConsumerMessage, highWatermark int64) *Message {
	message := &Message{Topic: raw.Topic, Partition: raw.Partition, Offset: raw.Offset, Key: raw.Key, Value: raw.Value, Timestamp: raw.Timestamp, HighWatermark: highWatermark}
	if len(raw.Headers) > 0 {
		message.Headers = make([]Header, 0, len(raw.Headers))
		for _, header := range raw.Headers {
			if header != nil {
				message.Headers = append(message.Headers, Header{Key: string(header.Key), Value: header.Value})
			}
		}
	}
	return message
}
