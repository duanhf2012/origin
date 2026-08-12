package kafkamodule

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/IBM/sarama"
	"github.com/duanhf2012/origin/v3/config"
	originlog "github.com/duanhf2012/origin/v3/log"
	"github.com/duanhf2012/origin/v3/node"
	"github.com/duanhf2012/origin/v3/service"
)

type consumerHandlerService struct{ service.Service }

func startConsumerHandlerService(t *testing.T) (*node.Node, *consumerHandlerService) {
	t.Helper()
	owner := &consumerHandlerService{}
	current, err := node.New(node.Config{ID: "kafka-consumer-handler", Services: []string{"ConsumerService"}, Scheduler: service.DefaultSchedulerConfig()}, []node.ServiceBinding{{Name: "ConsumerService", Template: "ConsumerService", Service: owner}}, originlog.NewNop(), node.Options{MaxTimersPerNode: 64, TimerLocation: time.UTC})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = current.Rollback(ctx)
	})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err = current.Start(ctx); err != nil {
		t.Fatal(err)
	}
	return current, owner
}

type fakeConsumerSession struct {
	ctx    context.Context
	mu     sync.Mutex
	marked []int64
}

func (session *fakeConsumerSession) Claims() map[string][]int32 {
	return map[string][]int32{"events": {0}}
}
func (session *fakeConsumerSession) MemberID() string    { return "member" }
func (session *fakeConsumerSession) GenerationID() int32 { return 1 }
func (session *fakeConsumerSession) MarkOffset(_ string, _ int32, offset int64, _ string) {
	session.mu.Lock()
	session.marked = append(session.marked, offset-1)
	session.mu.Unlock()
}
func (session *fakeConsumerSession) Commit()                                  {}
func (session *fakeConsumerSession) ResetOffset(string, int32, int64, string) {}
func (session *fakeConsumerSession) MarkMessage(message *sarama.ConsumerMessage, _ string) {
	session.mu.Lock()
	session.marked = append(session.marked, message.Offset)
	session.mu.Unlock()
}
func (session *fakeConsumerSession) Context() context.Context { return session.ctx }
func (session *fakeConsumerSession) markedOffsets() []int64 {
	session.mu.Lock()
	defer session.mu.Unlock()
	return append([]int64(nil), session.marked...)
}

type fakeConsumerClaim struct {
	topic         string
	partition     int32
	highWatermark int64
	messages      chan *sarama.ConsumerMessage
}

func (claim *fakeConsumerClaim) Topic() string                            { return claim.topic }
func (claim *fakeConsumerClaim) Partition() int32                         { return claim.partition }
func (claim *fakeConsumerClaim) InitialOffset() int64                     { return 0 }
func (claim *fakeConsumerClaim) HighWaterMarkOffset() int64               { return claim.highWatermark }
func (claim *fakeConsumerClaim) Messages() <-chan *sarama.ConsumerMessage { return claim.messages }

func claimWithOffsets(offsets ...int64) *fakeConsumerClaim {
	claim := &fakeConsumerClaim{topic: "events", partition: 0, highWatermark: 99, messages: make(chan *sarama.ConsumerMessage, len(offsets))}
	for _, offset := range offsets {
		claim.messages <- &sarama.ConsumerMessage{Topic: "events", Partition: 0, Offset: offset, Key: []byte("p"), Value: []byte(`{"level":9}`)}
	}
	close(claim.messages)
	return claim
}

func TestConsumerMarksOnlyAfterServiceHandlerSuccess(t *testing.T) {
	_, owner := startConsumerHandlerService(t)
	current, err := normalizeConsumerConfig(validConsumerConfig(), false)
	if err != nil {
		t.Fatal(err)
	}
	consumer := &Consumer{}
	handler := newManagedGroupHandler(owner, current, func(ctx context.Context, message *Message) error {
		if err := owner.Await(ctx, func(context.Context) error { return nil }); err != nil {
			return err
		}
		return nil
	}, nil, consumer)
	session := &fakeConsumerSession{ctx: context.Background()}
	if err = handler.ConsumeClaim(session, claimWithOffsets(10, 11)); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(session.markedOffsets(), []int64{10, 11}) {
		t.Fatalf("marked=%v", session.markedOffsets())
	}
	stats := consumer.Stats()
	if stats.Received != 2 || stats.Handled != 2 || stats.Failed != 0 {
		t.Fatalf("stats=%+v", stats)
	}
}

func TestConsumerFailureDoesNotMarkAndStopsClaim(t *testing.T) {
	_, owner := startConsumerHandlerService(t)
	current, err := normalizeConsumerConfig(validConsumerConfig(), false)
	if err != nil {
		t.Fatal(err)
	}
	businessErr := errors.New("inventory conflict")
	consumer := &Consumer{}
	handler := newManagedGroupHandler(owner, current, func(context.Context, *Message) error { return businessErr }, nil, consumer)
	session := &fakeConsumerSession{ctx: context.Background()}
	if err = handler.ConsumeClaim(session, claimWithOffsets(10, 11)); !errors.Is(err, businessErr) {
		t.Fatalf("handler error lost: %v", err)
	}
	if len(session.markedOffsets()) != 0 {
		t.Fatalf("failed message marked: %v", session.markedOffsets())
	}
	if !errors.Is(consumer.LastError(), businessErr) {
		t.Fatalf("last error=%v", consumer.LastError())
	}
}

func TestConsumerBatchHonorsMessageBoundaryAndMarksWholeBatch(t *testing.T) {
	_, owner := startConsumerHandlerService(t)
	input := validConsumerConfig()
	input.Batch = BatchConfig{MaxMessages: 2, MaxSize: 1 << 20, MaxWait: configDuration(50 * time.Millisecond)}
	current, err := normalizeConsumerConfig(input, true)
	if err != nil {
		t.Fatal(err)
	}
	consumer := &Consumer{}
	var sizes []int
	handler := newManagedGroupHandler(owner, current, nil, func(_ context.Context, batch Batch) error { sizes = append(sizes, len(batch.Messages)); return nil }, consumer)
	session := &fakeConsumerSession{ctx: context.Background()}
	if err = handler.ConsumeClaim(session, claimWithOffsets(1, 2, 3)); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(sizes, []int{2, 1}) || !reflect.DeepEqual(session.markedOffsets(), []int64{1, 2, 3}) {
		t.Fatalf("sizes=%v marked=%v", sizes, session.markedOffsets())
	}
}

func TestConsumerBatchDeliversOversizedSingleAsOneBatch(t *testing.T) {
	_, owner := startConsumerHandlerService(t)
	input := validConsumerConfig()
	input.Batch = BatchConfig{MaxMessages: 100, MaxSize: 4, MaxWait: configDuration(50 * time.Millisecond)}
	current, err := normalizeConsumerConfig(input, true)
	if err != nil {
		t.Fatal(err)
	}
	consumer := &Consumer{}
	var sizes []int
	handler := newManagedGroupHandler(owner, current, nil, func(_ context.Context, batch Batch) error {
		sizes = append(sizes, len(batch.Messages))
		return nil
	}, consumer)
	claim := &fakeConsumerClaim{topic: "events", partition: 0, messages: make(chan *sarama.ConsumerMessage, 1)}
	claim.messages <- &sarama.ConsumerMessage{Topic: "events", Partition: 0, Offset: 9, Value: []byte("larger-than-batch-threshold")}
	close(claim.messages)
	session := &fakeConsumerSession{ctx: context.Background()}
	if err = handler.ConsumeClaim(session, claim); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(sizes, []int{1}) || !reflect.DeepEqual(session.markedOffsets(), []int64{9}) {
		t.Fatalf("sizes=%v marked=%v", sizes, session.markedOffsets())
	}
}

func TestConsumerHandlerRetriesWithServiceAwait(t *testing.T) {
	_, owner := startConsumerHandlerService(t)
	input := validConsumerConfig()
	input.HandlerRetryMax = 2
	input.HandlerRetryBackoff = configDuration(time.Millisecond)
	current, err := normalizeConsumerConfig(input, false)
	if err != nil {
		t.Fatal(err)
	}
	consumer := &Consumer{}
	attempts := 0
	handler := newManagedGroupHandler(owner, current, func(context.Context, *Message) error {
		attempts++
		if attempts < 3 {
			return errors.New("retry")
		}
		return nil
	}, nil, consumer)
	session := &fakeConsumerSession{ctx: context.Background()}
	if err = handler.ConsumeClaim(session, claimWithOffsets(7)); err != nil {
		t.Fatal(err)
	}
	if attempts != 3 || !reflect.DeepEqual(session.markedOffsets(), []int64{7}) {
		t.Fatalf("attempts=%d marked=%v", attempts, session.markedOffsets())
	}
}

func TestConsumerHandlerPanicIsSanitizedAndNotMarked(t *testing.T) {
	_, owner := startConsumerHandlerService(t)
	current, err := normalizeConsumerConfig(validConsumerConfig(), false)
	if err != nil {
		t.Fatal(err)
	}
	consumer := &Consumer{}
	handler := newManagedGroupHandler(owner, current, func(context.Context, *Message) error { panic("secret-payload") }, nil, consumer)
	session := &fakeConsumerSession{ctx: context.Background()}
	err = handler.ConsumeClaim(session, claimWithOffsets(8))
	if err == nil || strings.Contains(err.Error(), "secret-payload") {
		t.Fatalf("panic was not sanitized: %v", err)
	}
	if len(session.markedOffsets()) != 0 {
		t.Fatalf("panic message marked: %v", session.markedOffsets())
	}
}

func TestConsumerBatchFlushesAtMaxWait(t *testing.T) {
	_, owner := startConsumerHandlerService(t)
	input := validConsumerConfig()
	input.Batch = BatchConfig{MaxMessages: 100, MaxSize: 1 << 20, MaxWait: configDuration(5 * time.Millisecond)}
	current, err := normalizeConsumerConfig(input, true)
	if err != nil {
		t.Fatal(err)
	}
	consumer := &Consumer{}
	flushed := make(chan struct{}, 1)
	handler := newManagedGroupHandler(owner, current, nil, func(context.Context, Batch) error { flushed <- struct{}{}; return nil }, consumer)
	claim := &fakeConsumerClaim{topic: "events", partition: 0, messages: make(chan *sarama.ConsumerMessage, 1)}
	claim.messages <- &sarama.ConsumerMessage{Topic: "events", Partition: 0, Offset: 9, Value: []byte("x")}
	session := &fakeConsumerSession{ctx: context.Background()}
	done := make(chan error, 1)
	go func() { done <- handler.ConsumeClaim(session, claim) }()
	select {
	case <-flushed:
	case <-time.After(time.Second):
		t.Fatal("batch did not flush at max_wait")
	}
	close(claim.messages)
	if err = <-done; err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(session.markedOffsets(), []int64{9}) {
		t.Fatalf("marked=%v", session.markedOffsets())
	}
}

func configDuration(value time.Duration) config.Duration { return config.Duration(value) }
