package kafkamodule

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/IBM/sarama"
	"github.com/duanhf2012/origin/v3/errs"
)

type fakeProducerRuntime struct {
	input     chan *sarama.ProducerMessage
	successes chan *sarama.ProducerMessage
	errors    chan *sarama.ProducerError
	fail      error
	closed    atomic.Int32
	once      sync.Once
}

func newFakeProducerRuntime(fail error) *fakeProducerRuntime {
	runtime := &fakeProducerRuntime{input: make(chan *sarama.ProducerMessage), successes: make(chan *sarama.ProducerMessage), errors: make(chan *sarama.ProducerError), fail: fail}
	go func() {
		defer close(runtime.successes)
		defer close(runtime.errors)
		for message := range runtime.input {
			if runtime.fail != nil {
				runtime.errors <- &sarama.ProducerError{Msg: message, Err: runtime.fail}
				continue
			}
			message.Partition = 2
			message.Offset = 11
			runtime.successes <- message
		}
	}()
	return runtime
}

func (runtime *fakeProducerRuntime) inputChannel() chan<- *sarama.ProducerMessage {
	return runtime.input
}
func (runtime *fakeProducerRuntime) successChannel() <-chan *sarama.ProducerMessage {
	return runtime.successes
}
func (runtime *fakeProducerRuntime) errorChannel() <-chan *sarama.ProducerError {
	return runtime.errors
}
func (runtime *fakeProducerRuntime) asyncClose()        { runtime.once.Do(func() { close(runtime.input) }) }
func (runtime *fakeProducerRuntime) closeClient() error { runtime.closed.Add(1); return nil }

func newStartedTestProducer(t *testing.T, failure error) (*Producer, *fakeProducerRuntime) {
	t.Helper()
	runtime := newFakeProducerRuntime(failure)
	producer, err := NewProducer(validProducerConfig(), withProducerRuntimeFactory(func(context.Context, []string, *sarama.Config) (producerRuntime, error) {
		return runtime, nil
	}))
	if err != nil {
		t.Fatal(err)
	}
	if err = producer.OnInit(); err != nil {
		t.Fatal(err)
	}
	if err = producer.OnStart(context.Background()); err != nil {
		t.Fatal(err)
	}
	return producer, runtime
}

func TestProducerSyncAsyncAndStats(t *testing.T) {
	producer, runtime := newStartedTestProducer(t, nil)
	delivery, err := producer.ProduceAsync(ProducerMessage{Topic: "events", Key: []byte("p1"), Value: []byte("value")})
	if err != nil {
		t.Fatal(err)
	}
	metadata, err := delivery.Wait(context.Background())
	if err != nil || metadata.Partition != 2 || metadata.Offset != 11 {
		t.Fatalf("unexpected delivery: %+v, %v", metadata, err)
	}
	metadata, err = producer.ProduceJSONSync(context.Background(), JSONMessage{Topic: "events", Value: map[string]int64{"level": 9}})
	if err != nil || metadata.Offset != 11 {
		t.Fatalf("unexpected JSON delivery: %+v, %v", metadata, err)
	}
	stats := producer.Stats()
	if stats.Accepted != 2 || stats.Succeeded != 2 || stats.Failed != 0 || stats.InFlight != 0 {
		t.Fatalf("unexpected stats: %+v", stats)
	}
	if err = producer.OnStop(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err = producer.OnStop(context.Background()); err != nil {
		t.Fatal(err)
	}
	if runtime.closed.Load() != 1 {
		t.Fatalf("client closed %d times", runtime.closed.Load())
	}
}

func TestProducerPropagatesBrokerErrorAndRejectsAfterStop(t *testing.T) {
	brokerErr := errors.New("broker rejected")
	producer, _ := newStartedTestProducer(t, brokerErr)
	if _, err := producer.ProduceSync(context.Background(), ProducerMessage{Topic: "events", Value: []byte("x")}); !errors.Is(err, brokerErr) {
		t.Fatalf("broker error chain lost: %v", err)
	}
	if err := producer.OnStop(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := producer.ProduceAsync(ProducerMessage{Topic: "events", Value: []byte("x")}); !errors.Is(err, ErrNotRunning) {
		t.Fatalf("stopped producer accepted message: %v", err)
	}
}

func TestProducerBatchAsyncReportsPartialAcceptance(t *testing.T) {
	producer, _ := newStartedTestProducer(t, nil)
	producer.running.Load().queue.messageLimit = 1
	deliveries, err := producer.ProduceBatchAsync([]ProducerMessage{{Topic: "events", Value: []byte("one")}, {Topic: "events", Value: []byte("two")}})
	if len(deliveries) != 1 || !errors.Is(err, errs.ErrTransportOverloaded) {
		t.Fatalf("partial acceptance not reported: %d, %v", len(deliveries), err)
	}
	var batchErr *BatchError
	if !errors.As(err, &batchErr) || batchErr.Accepted != 1 {
		t.Fatalf("accepted count missing: %#v", err)
	}
	if _, waitErr := deliveries[0].Wait(context.Background()); waitErr != nil {
		t.Fatal(waitErr)
	}
	if stopErr := producer.OnStop(context.Background()); stopErr != nil {
		t.Fatal(stopErr)
	}
}

func TestProducerDoesNotEncodeZeroTimestamp(t *testing.T) {
	envelope := testEnvelope(1)
	message := toSaramaProducerMessage(envelope)
	if !message.Timestamp.IsZero() {
		t.Fatalf("zero timestamp was encoded: %v", message.Timestamp)
	}
	expected := time.Now().UTC().Truncate(time.Millisecond)
	envelope.encoded.timestamp = expected
	message = toSaramaProducerMessage(envelope)
	if !message.Timestamp.Equal(expected) {
		t.Fatalf("explicit timestamp lost: %v", message.Timestamp)
	}
}

func TestProducerStartFailureCleansRuntimeAndStopDuringStart(t *testing.T) {
	created := newFakeProducerRuntime(nil)
	gate := make(chan struct{})
	producer, err := NewProducer(validProducerConfig(), withProducerRuntimeFactory(func(ctx context.Context, _ []string, _ *sarama.Config) (producerRuntime, error) {
		<-gate
		return created, nil
	}))
	if err != nil {
		t.Fatal(err)
	}
	startResult := make(chan error, 1)
	go func() { startResult <- producer.OnStart(context.Background()) }()
	deadline := time.After(time.Second)
	for {
		producer.mu.Lock()
		starting := producer.state == producerStateStarting
		producer.mu.Unlock()
		if starting {
			break
		}
		select {
		case <-deadline:
			t.Fatal("producer did not enter starting")
		default:
			time.Sleep(time.Millisecond)
		}
	}
	stopResult := make(chan error, 1)
	go func() { stopResult <- producer.OnStop(context.Background()) }()
	deadline = time.After(time.Second)
	for {
		producer.mu.Lock()
		stopping := producer.state == producerStateStopping
		producer.mu.Unlock()
		if stopping {
			break
		}
		select {
		case <-deadline:
			t.Fatal("producer did not enter stopping")
		default:
			time.Sleep(time.Millisecond)
		}
	}
	close(gate)
	if err = <-startResult; !errors.Is(err, context.Canceled) {
		t.Fatalf("unexpected start result: %v", err)
	}
	if err = <-stopResult; err != nil {
		t.Fatalf("unexpected stop result: %v", err)
	}
	if created.closed.Load() != 1 {
		t.Fatalf("cancelled start leaked runtime: %d", created.closed.Load())
	}
}
