package kafkamodule

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"

	"github.com/IBM/sarama"
	"github.com/duanhf2012/origin/v3/errs"
	"github.com/duanhf2012/origin/v3/service"
)

type producerState uint8

const (
	producerStateUnconfigured producerState = iota
	producerStateConfigured
	producerStateStarting
	producerStateRunning
	producerStateStopping
	producerStateStopped
)

type producerHolder struct {
	runtime        producerRuntime
	queue          *submitQueue
	submitDone     chan struct{}
	completionDone chan struct{}
}

// Producer 是一个逻辑 Kafka 集群的受管异步 Producer Module。
// 它使用一个 Sarama Client、一个 AsyncProducer 和一层消息数/字节双有界提交队列。
type Producer struct {
	service.Module
	mu             sync.Mutex
	state          producerState
	config         ProducerConfig
	options        []ProducerOption
	factory        producerRuntimeFactory
	running        atomic.Pointer[producerHolder]
	transitionDone chan struct{}
	transitionErr  error
	startCancel    context.CancelFunc
	accepted       atomic.Uint64
	succeeded      atomic.Uint64
	failed         atomic.Uint64
	overloaded     atomic.Uint64
	inFlight       atomic.Int64
}

// NewProducer 校验并冻结配置，返回可交给 Service.AddModule 的 Producer。
// NewProducer 不连接 Kafka；网络资源在 OnStart 中创建。
func NewProducer(config ProducerConfig, options ...ProducerOption) (*Producer, error) {
	producer := &Producer{}
	if err := producer.configure(config, options...); err != nil {
		return nil, err
	}
	return producer, nil
}

// Setup 在已绑定业务 Module 的 OnInit 中校验并冻结 Producer 配置，只允许成功一次。
func (producer *Producer) Setup(config ProducerConfig, options ...ProducerOption) error {
	if producer == nil || producer.Service() == nil {
		return ErrNotSetup
	}
	return producer.configure(config, options...)
}

func (producer *Producer) configure(input ProducerConfig, options ...ProducerOption) error {
	if producer == nil {
		return ErrInvalidArgument
	}
	producer.mu.Lock()
	defer producer.mu.Unlock()
	if producer.state != producerStateUnconfigured {
		return ErrAlreadySetup
	}
	current, err := normalizeProducerConfig(input)
	if err != nil {
		return err
	}
	selected := producerOptions{factory: newDriverProducerRuntime}
	for _, option := range options {
		if option == nil {
			return invalidConfig("kafkamodule ProducerOption 不能为空")
		}
		option.applyProducer(&selected)
	}
	if selected.factory == nil {
		return invalidConfig("kafkamodule Producer Runtime Factory 不能为空")
	}
	producer.config = current
	producer.options = append([]ProducerOption(nil), options...)
	producer.factory = selected.factory
	producer.state = producerStateConfigured
	return nil
}

// OnInit 验证 Producer 已通过 NewProducer 或 Setup 完成配置。
func (producer *Producer) OnInit() error {
	if producer == nil {
		return ErrInvalidArgument
	}
	producer.mu.Lock()
	defer producer.mu.Unlock()
	if producer.state != producerStateConfigured {
		return ErrNotSetup
	}
	return nil
}

// OnStart 创建唯一 Client 和 AsyncProducer，并启动提交与完成事件排空 goroutine。
func (producer *Producer) OnStart(ctx context.Context) error {
	if producer == nil || ctx == nil {
		return ErrInvalidArgument
	}
	producer.mu.Lock()
	if producer.state != producerStateConfigured {
		producer.mu.Unlock()
		return ErrNotSetup
	}
	producer.state = producerStateStarting
	startCtx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	producer.startCancel = cancel
	producer.transitionDone = done
	producer.transitionErr = nil
	current, factory := producer.config, producer.factory
	options := append([]ProducerOption(nil), producer.options...)
	producer.mu.Unlock()
	defer cancel()

	saramaConfig, err := BuildProducerSaramaConfig(current, options...)
	if err != nil {
		producer.failedStart(done)
		return err
	}
	runtime, err := factory(startCtx, current.Cluster.Brokers, saramaConfig)
	if err != nil {
		producer.failedStart(done)
		return err
	}
	if runtime == nil {
		producer.failedStart(done)
		return errors.New("kafkamodule: producer runtime factory returned nil")
	}
	if startErr := startCtx.Err(); startErr != nil {
		closeErr := closeUnpublishedProducerRuntime(runtime)
		producer.failedStart(done)
		return errors.Join(startErr, closeErr)
	}

	holder := &producerHolder{runtime: runtime, queue: newSubmitQueue(current.SubmitQueueMessages, current.SubmitQueueSize.Bytes()), submitDone: make(chan struct{}), completionDone: make(chan struct{})}
	producer.mu.Lock()
	if producer.state != producerStateStarting || producer.transitionDone != done {
		producer.mu.Unlock()
		closeErr := closeUnpublishedProducerRuntime(runtime)
		producer.failedStart(done)
		startErr := startCtx.Err()
		if startErr == nil {
			startErr = context.Canceled
		}
		return errors.Join(startErr, closeErr)
	}
	producer.running.Store(holder)
	producer.state = producerStateRunning
	producer.startCancel = nil
	producer.transitionDone = nil
	producer.transitionErr = nil
	close(done)
	producer.mu.Unlock()
	go producer.submitLoop(holder)
	go producer.completionLoop(holder)
	return nil
}

func (producer *Producer) failedStart(done chan struct{}) {
	producer.running.Store(nil)
	producer.mu.Lock()
	if producer.transitionDone == done {
		producer.state = producerStateStopped
		producer.startCancel = nil
		producer.transitionDone = nil
		producer.transitionErr = nil
		close(done)
	}
	producer.mu.Unlock()
}

func closeUnpublishedProducerRuntime(runtime producerRuntime) error {
	runtime.asyncClose()
	drainProducerRuntime(runtime)
	return runtime.closeClient()
}

func drainProducerRuntime(runtime producerRuntime) {
	successes, failures := runtime.successChannel(), runtime.errorChannel()
	for successes != nil || failures != nil {
		select {
		case _, open := <-successes:
			if !open {
				successes = nil
			}
		case _, open := <-failures:
			if !open {
				failures = nil
			}
		}
	}
}

func (producer *Producer) submitLoop(holder *producerHolder) {
	defer close(holder.submitDone)
	for envelope := range holder.queue.items {
		if envelope.admitted != nil {
			<-envelope.admitted
		}
		holder.runtime.inputChannel() <- toSaramaProducerMessage(envelope)
	}
	holder.runtime.asyncClose()
}

func (producer *Producer) completionLoop(holder *producerHolder) {
	defer close(holder.completionDone)
	successes, failures := holder.runtime.successChannel(), holder.runtime.errorChannel()
	for successes != nil || failures != nil {
		select {
		case message, open := <-successes:
			if !open {
				successes = nil
				continue
			}
			envelope, _ := message.Metadata.(*producerEnvelope)
			producer.completeEnvelope(holder, envelope, DeliveryResult{Metadata: Metadata{Topic: message.Topic, Partition: message.Partition, Offset: message.Offset, Timestamp: message.Timestamp}})
		case failure, open := <-failures:
			if !open {
				failures = nil
				continue
			}
			var envelope *producerEnvelope
			var metadata Metadata
			if failure != nil && failure.Msg != nil {
				envelope, _ = failure.Msg.Metadata.(*producerEnvelope)
				metadata = Metadata{Topic: failure.Msg.Topic, Partition: failure.Msg.Partition, Offset: failure.Msg.Offset, Timestamp: failure.Msg.Timestamp}
			}
			var err error
			if failure != nil {
				err = failure.Err
			}
			if err == nil {
				err = ErrClosed
			}
			producer.completeEnvelope(holder, envelope, DeliveryResult{Metadata: metadata, Err: err})
		}
	}
	holder.queue.close()
	holder.queue.failAll(ErrClosed, func(envelope *producerEnvelope, result DeliveryResult) {
		producer.completeEnvelope(holder, envelope, result)
	})
}

func (producer *Producer) completeEnvelope(holder *producerHolder, envelope *producerEnvelope, result DeliveryResult) {
	if envelope == nil {
		return
	}
	envelope.finish.Do(func() {
		envelope.delivery.complete(result)
		holder.queue.release(envelope)
		producer.inFlight.Add(-1)
		if result.Err == nil {
			producer.succeeded.Add(1)
		} else {
			producer.failed.Add(1)
		}
	})
}

func toSaramaProducerMessage(envelope *producerEnvelope) *sarama.ProducerMessage {
	current := envelope.encoded
	message := &sarama.ProducerMessage{Topic: current.topic, Timestamp: current.timestamp, Metadata: envelope}
	if current.key != nil {
		message.Key = sarama.ByteEncoder(current.key)
	}
	if current.value != nil {
		message.Value = sarama.ByteEncoder(current.value)
	}
	if len(current.headers) > 0 {
		message.Headers = make([]sarama.RecordHeader, len(current.headers))
		for index, header := range current.headers {
			message.Headers[index] = sarama.RecordHeader{Key: []byte(header.Key), Value: header.Value}
		}
	}
	return message
}

// OnStop 封闭新准入并排空全部已接受消息；ctx 到期只结束当前等待，内部清理继续。
func (producer *Producer) OnStop(ctx context.Context) error {
	if producer == nil || ctx == nil {
		return ErrInvalidArgument
	}
	producer.mu.Lock()
	if producer.state == producerStateUnconfigured || producer.state == producerStateConfigured || producer.state == producerStateStopped {
		producer.mu.Unlock()
		return nil
	}
	if producer.state == producerStateStarting {
		producer.state = producerStateStopping
		if producer.startCancel != nil {
			producer.startCancel()
		}
		done := producer.transitionDone
		producer.mu.Unlock()
		return producer.waitTransition(ctx, done)
	}
	if producer.state == producerStateStopping {
		done := producer.transitionDone
		producer.mu.Unlock()
		return producer.waitTransition(ctx, done)
	}
	if producer.state != producerStateRunning {
		producer.mu.Unlock()
		return ErrNotRunning
	}
	producer.state = producerStateStopping
	done := make(chan struct{})
	producer.transitionDone = done
	producer.transitionErr = nil
	holder := producer.running.Swap(nil)
	if holder != nil {
		holder.queue.close()
	}
	producer.mu.Unlock()
	go producer.finishStop(holder, done)
	return producer.waitTransition(ctx, done)
}

func (producer *Producer) finishStop(holder *producerHolder, done chan struct{}) {
	var err error
	if holder != nil {
		<-holder.submitDone
		<-holder.completionDone
		err = holder.runtime.closeClient()
	}
	producer.mu.Lock()
	if producer.transitionDone == done {
		producer.state = producerStateStopped
		producer.transitionErr = err
		close(done)
	}
	producer.mu.Unlock()
}

func (producer *Producer) waitTransition(ctx context.Context, done <-chan struct{}) error {
	if done == nil {
		return ErrNotRunning
	}
	select {
	case <-done:
		producer.mu.Lock()
		err := producer.transitionErr
		producer.mu.Unlock()
		return err
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (producer *Producer) submit(encoded *encodedMessage) (*Delivery, error) {
	if producer == nil {
		return nil, ErrNotRunning
	}
	holder := producer.running.Load()
	if holder == nil {
		return nil, ErrNotRunning
	}
	if encoded.payloadBytes > producer.config.MaxMessageSize.Bytes() {
		return nil, invalidArgument("kafkamodule 消息超过 max_message_size")
	}
	envelope := &producerEnvelope{encoded: encoded, delivery: newDelivery(), admitted: make(chan struct{})}
	if err := holder.queue.trySubmit(envelope); err != nil {
		if errors.Is(err, errs.ErrTransportOverloaded) {
			producer.overloaded.Add(1)
		}
		return nil, err
	}
	producer.accepted.Add(1)
	producer.inFlight.Add(1)
	close(envelope.admitted)
	return envelope.delivery, nil
}

// ProduceAsync 非阻塞接受一条 Raw 消息；Async 表示不等待 Broker，Raw Buffer 在 Delivery 前由 Module 借用。
func (producer *Producer) ProduceAsync(message ProducerMessage) (*Delivery, error) {
	encoded, err := encodeRaw(message)
	if err != nil {
		return nil, err
	}
	return producer.submit(encoded)
}

// ProduceSync 接受一条 Raw 消息并等待 Broker Delivery；ctx 取消不撤回消息。
func (producer *Producer) ProduceSync(ctx context.Context, message ProducerMessage) (Metadata, error) {
	if ctx == nil {
		return Metadata{}, ErrInvalidArgument
	}
	delivery, err := producer.ProduceAsync(message)
	if err != nil {
		return Metadata{}, err
	}
	return delivery.Wait(ctx)
}

// ProduceJSONAsync 在调用方 goroutine 使用 Sonic 编码稳定快照，再非阻塞提交。
func (producer *Producer) ProduceJSONAsync(message JSONMessage) (*Delivery, error) {
	encoded, err := encodeJSON(message)
	if err != nil {
		return nil, err
	}
	return producer.submit(encoded)
}

// ProduceJSONSync 编码 JSON、提交并等待 Broker Delivery。
func (producer *Producer) ProduceJSONSync(ctx context.Context, message JSONMessage) (Metadata, error) {
	if ctx == nil {
		return Metadata{}, ErrInvalidArgument
	}
	delivery, err := producer.ProduceJSONAsync(message)
	if err != nil {
		return Metadata{}, err
	}
	return delivery.Wait(ctx)
}

// ProducePBAsync 在调用方 goroutine 编码 Protobuf 稳定快照，再非阻塞提交。
func (producer *Producer) ProducePBAsync(message PBMessage) (*Delivery, error) {
	encoded, err := encodePB(message)
	if err != nil {
		return nil, err
	}
	return producer.submit(encoded)
}

// ProducePBSync 编码 Protobuf、提交并等待 Broker Delivery。
func (producer *Producer) ProducePBSync(ctx context.Context, message PBMessage) (Metadata, error) {
	if ctx == nil {
		return Metadata{}, ErrInvalidArgument
	}
	delivery, err := producer.ProducePBAsync(message)
	if err != nil {
		return Metadata{}, err
	}
	return delivery.Wait(ctx)
}

func batchAsync[T any](messages []T, submit func(T) (*Delivery, error), topic func(T) string) ([]*Delivery, error) {
	if len(messages) == 0 {
		return nil, ErrInvalidArgument
	}
	deliveries := make([]*Delivery, 0, len(messages))
	for index, message := range messages {
		delivery, err := submit(message)
		if err != nil {
			return deliveries, &BatchError{Accepted: len(deliveries), Failures: []BatchFailure{{Index: index, Topic: topic(message), Partition: -1, Err: err}}}
		}
		deliveries = append(deliveries, delivery)
	}
	return deliveries, nil
}

func batchSync[T any](ctx context.Context, messages []T, submit func(T) (*Delivery, error), topic func(T) string) ([]DeliveryResult, error) {
	if ctx == nil || len(messages) == 0 {
		return nil, ErrInvalidArgument
	}
	results := make([]DeliveryResult, len(messages))
	deliveries := make([]*Delivery, len(messages))
	accepted := 0
	failures := make([]BatchFailure, 0)
	for index, message := range messages {
		delivery, err := submit(message)
		if err != nil {
			results[index].Err = err
			failures = append(failures, BatchFailure{Index: index, Topic: topic(message), Partition: -1, Err: err})
			continue
		}
		deliveries[index] = delivery
		accepted++
	}
	for index, delivery := range deliveries {
		if delivery == nil {
			continue
		}
		metadata, err := delivery.Wait(ctx)
		results[index] = DeliveryResult{Metadata: metadata, Err: err}
		if err != nil {
			failures = append(failures, BatchFailure{Index: index, Topic: topic(messages[index]), Partition: metadata.Partition, Err: err})
		}
	}
	if len(failures) > 0 {
		return results, &BatchError{Accepted: accepted, Failures: failures}
	}
	return results, nil
}

// ProduceBatchAsync 逐条非阻塞提交 Raw 消息；失败时返回已接受前缀和 BatchError。
func (producer *Producer) ProduceBatchAsync(messages []ProducerMessage) ([]*Delivery, error) {
	return batchAsync(messages, producer.ProduceAsync, func(message ProducerMessage) string { return message.Topic })
}

// ProduceBatchSync 提交 Raw 批量并返回与输入等长的逐条结果；批量不具备事务原子性。
func (producer *Producer) ProduceBatchSync(ctx context.Context, messages []ProducerMessage) ([]DeliveryResult, error) {
	return batchSync(ctx, messages, producer.ProduceAsync, func(message ProducerMessage) string { return message.Topic })
}

// ProduceJSONBatchAsync 编码并逐条非阻塞提交 JSON 消息。
func (producer *Producer) ProduceJSONBatchAsync(messages []JSONMessage) ([]*Delivery, error) {
	return batchAsync(messages, producer.ProduceJSONAsync, func(message JSONMessage) string { return message.Topic })
}

// ProduceJSONBatchSync 编码、提交并等待 JSON 批量结果。
func (producer *Producer) ProduceJSONBatchSync(ctx context.Context, messages []JSONMessage) ([]DeliveryResult, error) {
	return batchSync(ctx, messages, producer.ProduceJSONAsync, func(message JSONMessage) string { return message.Topic })
}

// ProducePBBatchAsync 编码并逐条非阻塞提交 Protobuf 消息。
func (producer *Producer) ProducePBBatchAsync(messages []PBMessage) ([]*Delivery, error) {
	return batchAsync(messages, producer.ProducePBAsync, func(message PBMessage) string { return message.Topic })
}

// ProducePBBatchSync 编码、提交并等待 Protobuf 批量结果。
func (producer *Producer) ProducePBBatchSync(ctx context.Context, messages []PBMessage) ([]DeliveryResult, error) {
	return batchSync(ctx, messages, producer.ProducePBAsync, func(message PBMessage) string { return message.Topic })
}

// Stats 返回当前累计计数和在途数量的原子快照。
func (producer *Producer) Stats() ProducerStats {
	if producer == nil {
		return ProducerStats{}
	}
	return ProducerStats{Accepted: producer.accepted.Load(), Succeeded: producer.succeeded.Load(), Failed: producer.failed.Load(), Overloaded: producer.overloaded.Load(), InFlight: producer.inFlight.Load()}
}

var _ service.IModule = (*Producer)(nil)
