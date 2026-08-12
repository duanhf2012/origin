package kafkamodule

import (
	"context"
	"errors"
	"math/rand/v2"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/IBM/sarama"
	"github.com/duanhf2012/origin/v3/service"
)

// Handler 在 Consumer 所属 Service 的串行工作协程中处理单条消息。
// 返回 nil 后 Claim goroutine 才会 Mark；返回错误时不 Mark，并停止当前受管 Consumer。
type Handler func(context.Context, *Message) error

// Batch 是同一 Topic、同一 Partition 的连续消息批次。
type Batch struct {
	// Topic 是该批次中所有消息共同的 Topic。
	Topic string
	// Partition 是该批次中所有消息共同的分区。
	Partition int32
	// Messages 按 Offset 递增排列，仅在 Handler 返回前保证底层字节有效。
	Messages []*Message
}

// BatchHandler 在 Consumer 所属 Service 的串行工作协程中处理一个批次。
// 返回 nil 后整个批次按顺序 Mark；失败时整个批次都不 Mark。
type BatchHandler func(context.Context, Batch) error

type consumerState uint8

const (
	consumerStateUnconfigured consumerState = iota
	consumerStateConfigured
	consumerStateStarting
	consumerStateRunning
	consumerStateStopping
	consumerStateStopped
)

type consumerHolder struct {
	runtime     consumerRuntime
	handler     *managedGroupHandler
	ctx         context.Context
	cancel      context.CancelCauseFunc
	consumeDone chan struct{}
	errorsDone  chan struct{}
	closeOnce   sync.Once
	closeErr    error
}

// Consumer 是一个逻辑 Kafka 集群的受管 Consumer Group Module。
type Consumer struct {
	service.Module
	mu                sync.Mutex
	state             consumerState
	config            ConsumerConfig
	handler           Handler
	batchHandler      BatchHandler
	options           []ConsumerOption
	factory           consumerRuntimeFactory
	current           atomic.Pointer[consumerHolder]
	transitionDone    chan struct{}
	transitionErr     error
	startCancel       context.CancelFunc
	pauseMu           sync.Mutex
	pauseAllDesired   bool
	pausedDesired     map[string]map[int32]struct{}
	resumedExceptions map[string]map[int32]struct{}
	errorMu           sync.RWMutex
	lastError         error
	cancelMu          sync.Mutex
	failureCancel     context.CancelCauseFunc
	received          atomic.Uint64
	handled           atomic.Uint64
	failed            atomic.Uint64
	batches           atomic.Uint64
	rebalances        atomic.Uint64
	dispatchRejected  atomic.Uint64
	runningFlag       atomic.Bool
}

// NewConsumer 校验并冻结单条消费配置与 Handler，不连接 Kafka。
func NewConsumer(config ConsumerConfig, handler Handler, options ...ConsumerOption) (*Consumer, error) {
	consumer := &Consumer{}
	if err := consumer.configure(config, handler, nil, false, options...); err != nil {
		return nil, err
	}
	return consumer, nil
}

// NewBatchConsumer 校验并冻结批量消费配置与 BatchHandler，不连接 Kafka。
func NewBatchConsumer(config ConsumerConfig, handler BatchHandler, options ...ConsumerOption) (*Consumer, error) {
	consumer := &Consumer{}
	if err := consumer.configure(config, nil, handler, true, options...); err != nil {
		return nil, err
	}
	return consumer, nil
}

// Setup 在已绑定业务 Module 的 OnInit 中配置单条 Consumer，只允许成功一次。
func (consumer *Consumer) Setup(config ConsumerConfig, handler Handler, options ...ConsumerOption) error {
	if consumer == nil || consumer.Service() == nil {
		return ErrNotSetup
	}
	return consumer.configure(config, handler, nil, false, options...)
}

// SetupBatch 在已绑定业务 Module 的 OnInit 中配置批量 Consumer，只允许成功一次。
func (consumer *Consumer) SetupBatch(config ConsumerConfig, handler BatchHandler, options ...ConsumerOption) error {
	if consumer == nil || consumer.Service() == nil {
		return ErrNotSetup
	}
	return consumer.configure(config, nil, handler, true, options...)
}

func (consumer *Consumer) configure(input ConsumerConfig, handler Handler, batchHandler BatchHandler, batch bool, options ...ConsumerOption) error {
	if consumer == nil || (!batch && handler == nil) || (batch && batchHandler == nil) {
		return ErrInvalidArgument
	}
	consumer.mu.Lock()
	defer consumer.mu.Unlock()
	if consumer.state != consumerStateUnconfigured {
		return ErrAlreadySetup
	}
	current, err := normalizeConsumerConfig(input, batch)
	if err != nil {
		return err
	}
	selected := consumerOptions{factory: newDriverConsumerRuntime}
	for _, option := range options {
		if option == nil {
			return invalidConfig("kafkamodule ConsumerOption 不能为空")
		}
		option.applyConsumer(&selected)
	}
	if selected.factory == nil {
		return invalidConfig("kafkamodule Consumer Runtime Factory 不能为空")
	}
	consumer.config = current
	consumer.handler = handler
	consumer.batchHandler = batchHandler
	consumer.options = append([]ConsumerOption(nil), options...)
	consumer.factory = selected.factory
	consumer.state = consumerStateConfigured
	return nil
}

// OnInit 验证 Consumer 已通过构造函数或 Setup 完成配置。
func (consumer *Consumer) OnInit() error {
	if consumer == nil {
		return ErrInvalidArgument
	}
	consumer.mu.Lock()
	defer consumer.mu.Unlock()
	if consumer.state != consumerStateConfigured {
		return ErrNotSetup
	}
	return nil
}

// OnStart 创建 Consumer Group，并等待首个 Session Setup 成功后才报告 Ready。
func (consumer *Consumer) OnStart(ctx context.Context) error {
	if consumer == nil || ctx == nil || consumer.Service() == nil {
		return ErrInvalidArgument
	}
	consumer.mu.Lock()
	if consumer.state != consumerStateConfigured {
		consumer.mu.Unlock()
		return ErrNotSetup
	}
	consumer.state = consumerStateStarting
	startCtx, startCancel := context.WithCancel(ctx)
	done := make(chan struct{})
	consumer.startCancel = startCancel
	consumer.transitionDone = done
	consumer.transitionErr = nil
	current, factory := consumer.config, consumer.factory
	options := append([]ConsumerOption(nil), consumer.options...)
	handlerFn, batchHandler := consumer.handler, consumer.batchHandler
	owner := consumer.Service()
	consumer.mu.Unlock()
	defer startCancel()

	saramaConfig, err := BuildConsumerSaramaConfig(current, options...)
	if err != nil {
		consumer.failedStart(done)
		return err
	}
	runtime, err := factory(startCtx, current.Cluster.Brokers, current.GroupID, saramaConfig)
	if err != nil {
		consumer.failedStart(done)
		return err
	}
	if runtime == nil {
		consumer.failedStart(done)
		return errors.New("kafkamodule: consumer runtime factory returned nil")
	}
	if err = startCtx.Err(); err != nil {
		closeErr := runtime.close()
		consumer.failedStart(done)
		return errors.Join(err, closeErr)
	}

	lifetime, cancel := context.WithCancelCause(context.Background())
	managed := newManagedGroupHandler(owner, current, handlerFn, batchHandler, consumer)
	holder := &consumerHolder{runtime: runtime, handler: managed, ctx: lifetime, cancel: cancel, consumeDone: make(chan struct{}), errorsDone: make(chan struct{})}
	consumer.current.Store(holder)
	consumer.cancelMu.Lock()
	consumer.failureCancel = cancel
	consumer.cancelMu.Unlock()
	go consumer.consumeLoop(holder)
	go consumer.errorsLoop(holder)

	select {
	case <-managed.ready:
		if startErr := startCtx.Err(); startErr != nil {
			closeErr := consumer.closeHolder(holder, startErr)
			consumer.failedStart(done)
			return errors.Join(startErr, closeErr)
		}
		consumer.mu.Lock()
		if consumer.state != consumerStateStarting || consumer.transitionDone != done {
			consumer.mu.Unlock()
			consumer.closeHolder(holder, context.Canceled)
			consumer.failedStart(done)
			return context.Canceled
		}
		consumer.state = consumerStateRunning
		consumer.startCancel = nil
		consumer.transitionDone = nil
		consumer.transitionErr = nil
		consumer.runningFlag.Store(true)
		close(done)
		consumer.mu.Unlock()
		return nil
	case <-holder.consumeDone:
		cause := context.Cause(holder.ctx)
		if cause == nil {
			cause = consumer.LastError()
		}
		if cause == nil {
			cause = ErrNotRunning
		}
		closeErr := consumer.closeHolder(holder, cause)
		consumer.failedStart(done)
		return errors.Join(cause, closeErr)
	case <-startCtx.Done():
		closeErr := consumer.closeHolder(holder, startCtx.Err())
		consumer.failedStart(done)
		return errors.Join(startCtx.Err(), closeErr)
	}
}

func (consumer *Consumer) failedStart(done chan struct{}) {
	consumer.current.Store(nil)
	consumer.runningFlag.Store(false)
	consumer.cancelMu.Lock()
	consumer.failureCancel = nil
	consumer.cancelMu.Unlock()
	consumer.mu.Lock()
	if consumer.transitionDone == done {
		consumer.state = consumerStateStopped
		consumer.startCancel = nil
		consumer.transitionDone = nil
		consumer.transitionErr = nil
		close(done)
	}
	consumer.mu.Unlock()
}

func (consumer *Consumer) consumeLoop(holder *consumerHolder) {
	defer func() {
		consumer.runningFlag.Store(false)
		close(holder.consumeDone)
	}()
	backoff := consumer.config.RecoveryInitialBackoff.Duration()
	for holder.ctx.Err() == nil {
		err := holder.runtime.consume(holder.ctx, consumer.config.Topics, holder.handler)
		if holder.ctx.Err() != nil {
			return
		}
		if err == nil {
			backoff = consumer.config.RecoveryInitialBackoff.Duration()
			continue
		}
		consumer.setLastError(err)
		if isFatalConsumerError(err) {
			holder.cancel(err)
			return
		}
		timer := time.NewTimer(jitteredBackoff(backoff, consumer.config.RecoveryMaxBackoff.Duration()))
		select {
		case <-timer.C:
		case <-holder.ctx.Done():
			if !timer.Stop() {
				<-timer.C
			}
			return
		}
		backoff *= 2
		if backoff > consumer.config.RecoveryMaxBackoff.Duration() {
			backoff = consumer.config.RecoveryMaxBackoff.Duration()
		}
	}
}

func (consumer *Consumer) errorsLoop(holder *consumerHolder) {
	defer close(holder.errorsDone)
	errorsChannel := holder.runtime.errors()
	if errorsChannel == nil {
		return
	}
	for err := range errorsChannel {
		if err != nil {
			consumer.setLastError(err)
		}
	}
}

func (consumer *Consumer) setLastError(err error) {
	if err == nil {
		return
	}
	consumer.errorMu.Lock()
	consumer.lastError = err
	consumer.errorMu.Unlock()
}

func isFatalConsumerError(err error) bool {
	return errors.Is(err, sarama.ErrSASLAuthenticationFailed) || errors.Is(err, sarama.ErrTopicAuthorizationFailed) || errors.Is(err, sarama.ErrGroupAuthorizationFailed) || errors.Is(err, sarama.ErrUnsupportedVersion) || errors.Is(err, sarama.ErrUnsupportedForMessageFormat)
}

func jitteredBackoff(base, maximum time.Duration) time.Duration {
	if base <= 0 {
		return 0
	}
	// 0.8 到 1.2 的轻量抖动避免多个实例同时重连；不影响配置的硬上限数量级。
	factor := 0.8 + rand.Float64()*0.4
	result := time.Duration(float64(base) * factor)
	if maximum > 0 && result > maximum {
		return maximum
	}
	return result
}

// OnStop 取消当前 Session、关闭 Consumer Group 并等待 Claim 退出；重复与并发停止安全。
func (consumer *Consumer) OnStop(ctx context.Context) error {
	if consumer == nil || ctx == nil {
		return ErrInvalidArgument
	}
	consumer.mu.Lock()
	if consumer.state == consumerStateUnconfigured || consumer.state == consumerStateConfigured || consumer.state == consumerStateStopped {
		consumer.mu.Unlock()
		return nil
	}
	if consumer.state == consumerStateStarting {
		consumer.state = consumerStateStopping
		if consumer.startCancel != nil {
			consumer.startCancel()
		}
		done := consumer.transitionDone
		consumer.mu.Unlock()
		return consumer.waitTransition(ctx, done)
	}
	if consumer.state == consumerStateStopping {
		done := consumer.transitionDone
		consumer.mu.Unlock()
		return consumer.waitTransition(ctx, done)
	}
	if consumer.state != consumerStateRunning {
		consumer.mu.Unlock()
		return ErrNotRunning
	}
	consumer.state = consumerStateStopping
	done := make(chan struct{})
	consumer.transitionDone = done
	consumer.transitionErr = nil
	holder := consumer.current.Swap(nil)
	consumer.runningFlag.Store(false)
	consumer.mu.Unlock()
	go consumer.finishStop(holder, done)
	return consumer.waitTransition(ctx, done)
}

func (consumer *Consumer) finishStop(holder *consumerHolder, done chan struct{}) {
	err := consumer.closeHolder(holder, context.Canceled)
	consumer.cancelMu.Lock()
	consumer.failureCancel = nil
	consumer.cancelMu.Unlock()
	consumer.mu.Lock()
	if consumer.transitionDone == done {
		consumer.state = consumerStateStopped
		consumer.transitionErr = err
		close(done)
	}
	consumer.mu.Unlock()
}

func (consumer *Consumer) closeHolder(holder *consumerHolder, cause error) error {
	if holder == nil {
		return nil
	}
	holder.closeOnce.Do(func() {
		holder.cancel(cause)
		holder.closeErr = holder.runtime.close()
		<-holder.consumeDone
		<-holder.errorsDone
	})
	return holder.closeErr
}

func (consumer *Consumer) waitTransition(ctx context.Context, done <-chan struct{}) error {
	if done == nil {
		return ErrNotRunning
	}
	select {
	case <-done:
		consumer.mu.Lock()
		err := consumer.transitionErr
		consumer.mu.Unlock()
		return err
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (consumer *Consumer) runningHolder() (*consumerHolder, error) {
	if consumer == nil {
		return nil, ErrNotRunning
	}
	holder := consumer.current.Load()
	if holder == nil || !consumer.runningFlag.Load() {
		return nil, ErrNotRunning
	}
	select {
	case <-holder.consumeDone:
		return nil, ErrNotRunning
	default:
		return holder, nil
	}
}

// PauseAll 暂停所有已分配 Partition 的后续 Fetch，不影响已进入 Service 队列的任务。
func (consumer *Consumer) PauseAll() error {
	holder, err := consumer.runningHolder()
	if err != nil {
		return err
	}
	consumer.pauseMu.Lock()
	consumer.pauseAllDesired = true
	consumer.pausedDesired = nil
	consumer.resumedExceptions = nil
	consumer.pauseMu.Unlock()
	holder.runtime.pauseAll()
	return nil
}

// ResumeAll 恢复所有由 PauseAll 暂停的 Partition。
func (consumer *Consumer) ResumeAll() error {
	holder, err := consumer.runningHolder()
	if err != nil {
		return err
	}
	consumer.pauseMu.Lock()
	consumer.pauseAllDesired = false
	consumer.pausedDesired = nil
	consumer.resumedExceptions = nil
	consumer.pauseMu.Unlock()
	holder.runtime.resumeAll()
	return nil
}

// Pause 暂停指定 Topic/Partition 的后续 Fetch；参数 Map 在返回前复制。
func (consumer *Consumer) Pause(partitions map[string][]int32) error {
	current, err := normalizePartitions(partitions)
	if err != nil {
		return err
	}
	holder, err := consumer.runningHolder()
	if err != nil {
		return err
	}
	consumer.pauseMu.Lock()
	if consumer.pauseAllDesired {
		removePartitionSet(consumer.resumedExceptions, current)
	} else {
		if consumer.pausedDesired == nil {
			consumer.pausedDesired = make(map[string]map[int32]struct{})
		}
		addPartitionSet(consumer.pausedDesired, current)
	}
	consumer.pauseMu.Unlock()
	holder.runtime.pause(current)
	return nil
}

// Resume 恢复指定 Topic/Partition 的 Fetch；参数 Map 在返回前复制。
func (consumer *Consumer) Resume(partitions map[string][]int32) error {
	current, err := normalizePartitions(partitions)
	if err != nil {
		return err
	}
	holder, err := consumer.runningHolder()
	if err != nil {
		return err
	}
	consumer.pauseMu.Lock()
	if consumer.pauseAllDesired {
		if consumer.resumedExceptions == nil {
			consumer.resumedExceptions = make(map[string]map[int32]struct{})
		}
		addPartitionSet(consumer.resumedExceptions, current)
	} else {
		removePartitionSet(consumer.pausedDesired, current)
	}
	consumer.pauseMu.Unlock()
	holder.runtime.resume(current)
	return nil
}

func addPartitionSet(target map[string]map[int32]struct{}, partitions map[string][]int32) {
	for topic, values := range partitions {
		set := target[topic]
		if set == nil {
			set = make(map[int32]struct{}, len(values))
			target[topic] = set
		}
		for _, partition := range values {
			set[partition] = struct{}{}
		}
	}
}

func removePartitionSet(target map[string]map[int32]struct{}, partitions map[string][]int32) {
	for topic, values := range partitions {
		set := target[topic]
		for _, partition := range values {
			delete(set, partition)
		}
		if len(set) == 0 {
			delete(target, topic)
		}
	}
}

func snapshotPartitionSet(source map[string]map[int32]struct{}) map[string][]int32 {
	result := make(map[string][]int32, len(source))
	for topic, set := range source {
		result[topic] = make([]int32, 0, len(set))
		for partition := range set {
			result[topic] = append(result[topic], partition)
		}
	}
	return result
}

func (consumer *Consumer) applyDesiredPause() {
	if consumer == nil {
		return
	}
	holder := consumer.current.Load()
	if holder == nil {
		return
	}
	consumer.pauseMu.Lock()
	pauseAll := consumer.pauseAllDesired
	paused := snapshotPartitionSet(consumer.pausedDesired)
	resumed := snapshotPartitionSet(consumer.resumedExceptions)
	consumer.pauseMu.Unlock()
	if pauseAll {
		holder.runtime.pauseAll()
		if len(resumed) > 0 {
			holder.runtime.resume(resumed)
		}
		return
	}
	if len(paused) > 0 {
		holder.runtime.pause(paused)
	}
}

func normalizePartitions(input map[string][]int32) (map[string][]int32, error) {
	if len(input) == 0 {
		return nil, ErrInvalidArgument
	}
	result := make(map[string][]int32, len(input))
	for topic, partitions := range input {
		topic = strings.TrimSpace(topic)
		if topic == "" || len(partitions) == 0 {
			return nil, ErrInvalidArgument
		}
		seen := make(map[int32]struct{}, len(partitions))
		result[topic] = make([]int32, len(partitions))
		for index, partition := range partitions {
			if partition < 0 {
				return nil, ErrInvalidArgument
			}
			if _, exists := seen[partition]; exists {
				return nil, ErrInvalidArgument
			}
			seen[partition] = struct{}{}
			result[topic][index] = partition
		}
	}
	return result, nil
}

func (consumer *Consumer) stopWithError(err error) {
	if consumer == nil || err == nil {
		return
	}
	consumer.errorMu.Lock()
	consumer.lastError = err
	consumer.errorMu.Unlock()
	consumer.cancelMu.Lock()
	cancel := consumer.failureCancel
	consumer.cancelMu.Unlock()
	if cancel != nil {
		cancel(err)
	}
}

// LastError 返回最近一次业务 Handler、消费或恢复错误的只读快照；没有错误时返回 nil。
func (consumer *Consumer) LastError() error {
	if consumer == nil {
		return nil
	}
	consumer.errorMu.RLock()
	defer consumer.errorMu.RUnlock()
	return consumer.lastError
}

// Stats 返回当前累计计数和运行状态的原子快照。
func (consumer *Consumer) Stats() ConsumerStats {
	if consumer == nil {
		return ConsumerStats{}
	}
	return ConsumerStats{Received: consumer.received.Load(), Handled: consumer.handled.Load(), Failed: consumer.failed.Load(), Batches: consumer.batches.Load(), Rebalances: consumer.rebalances.Load(), DispatchRejected: consumer.dispatchRejected.Load(), Running: consumer.runningFlag.Load()}
}

var _ service.IModule = (*Consumer)(nil)
