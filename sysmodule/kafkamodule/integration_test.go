package kafkamodule

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/IBM/sarama"
	"github.com/duanhf2012/origin/v3/config"
	originlog "github.com/duanhf2012/origin/v3/log"
	"github.com/duanhf2012/origin/v3/node"
	"github.com/duanhf2012/origin/v3/service"
	"google.golang.org/protobuf/types/known/wrapperspb"
)

const kafkaIntegrationTimeout = 30 * time.Second

type kafkaIntegrationService struct {
	service.Service
	producer *Producer
	consumer *Consumer
}

func (owner *kafkaIntegrationService) OnInit() error {
	if owner.producer != nil {
		if err := owner.AddModule(owner.producer); err != nil {
			return err
		}
	}
	if owner.consumer != nil {
		return owner.AddModule(owner.consumer)
	}
	return nil
}

func integrationCluster(t *testing.T) (ClusterConfig, string) {
	t.Helper()
	raw := strings.TrimSpace(os.Getenv("ORIGIN_KAFKA_BROKERS"))
	if raw == "" {
		t.Skip("ORIGIN_KAFKA_BROKERS is not set")
	}
	brokers := strings.Split(raw, ",")
	for index := range brokers {
		brokers[index] = strings.TrimSpace(brokers[index])
	}
	prefix := strings.TrimSpace(os.Getenv("ORIGIN_KAFKA_TOPIC_PREFIX"))
	if prefix == "" {
		prefix = "origin-kafka"
	}
	return ClusterConfig{Brokers: brokers, Version: "4.0.0", ClientID: "origin-kafka-integration", MetadataTimeout: config.Duration(5 * time.Second)}, prefix
}

func newIntegrationNode(t *testing.T, producer *Producer, consumer *Consumer) (*node.Node, *kafkaIntegrationService) {
	t.Helper()
	identifier := fmt.Sprintf("kafka-integration-%d", time.Now().UnixNano())
	owner := &kafkaIntegrationService{producer: producer, consumer: consumer}
	current, err := node.New(node.Config{ID: identifier, Services: []string{"KafkaIntegration"}, Scheduler: service.DefaultSchedulerConfig()}, []node.ServiceBinding{{Name: "KafkaIntegration", Template: "KafkaIntegration", Service: owner}}, originlog.NewNop(), node.Options{MaxTimersPerNode: 128, TimerLocation: time.UTC})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), kafkaIntegrationTimeout)
		defer cancel()
		_ = current.Rollback(ctx)
	})
	ctx, cancel := context.WithTimeout(context.Background(), kafkaIntegrationTimeout)
	defer cancel()
	if err = current.Start(ctx); err != nil {
		t.Fatal(err)
	}
	return current, owner
}

func cloneIntegrationMessage(message *Message) *Message {
	clone := *message
	clone.Key = append([]byte(nil), message.Key...)
	if message.Value != nil {
		clone.Value = append([]byte(nil), message.Value...)
	}
	clone.Headers = make([]Header, len(message.Headers))
	for index, header := range message.Headers {
		clone.Headers[index] = Header{Key: header.Key, Value: append([]byte(nil), header.Value...)}
	}
	return &clone
}

func TestIntegrationManagedProducerConsumerCodecsAndPause(t *testing.T) {
	cluster, prefix := integrationCluster(t)
	topics := []string{prefix + "-raw", prefix + "-json", prefix + "-pb", prefix + "-compacted"}
	received := make(chan *Message, 32)
	consumer, err := NewConsumer(ConsumerConfig{Cluster: cluster, GroupID: fmt.Sprintf("origin-integration-%d", time.Now().UnixNano()), Topics: topics, InitialOffset: "newest"}, func(_ context.Context, message *Message) error {
		received <- cloneIntegrationMessage(message)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	producer, err := NewProducer(ProducerConfig{Cluster: cluster})
	if err != nil {
		t.Fatal(err)
	}
	current, _ := newIntegrationNode(t, producer, consumer)

	if err = consumer.PauseAll(); err != nil {
		t.Fatal(err)
	}
	// Pause 只阻止后续 Fetch；先让调用前已经发出的长轮询 Fetch 超时返回，避免把在途 Fetch
	// 误判成 Pause 失效。该行为与公开文档“不撤回已在途任务”保持一致。
	time.Sleep(750 * time.Millisecond)
	keyPrefix := fmt.Sprintf("integration-%d", time.Now().UnixNano())
	pauseKey := keyPrefix + "-paused"
	ctx, cancel := context.WithTimeout(context.Background(), kafkaIntegrationTimeout)
	defer cancel()
	if _, err = producer.ProduceSync(ctx, ProducerMessage{Topic: topics[0], Key: []byte(pauseKey), Value: []byte("paused")}); err != nil {
		t.Fatal(err)
	}
	select {
	case message := <-received:
		t.Fatalf("PauseAll still delivered offset %d", message.Offset)
	case <-time.After(250 * time.Millisecond):
	}
	if err = consumer.ResumeAll(); err != nil {
		t.Fatal(err)
	}

	rawKey := keyPrefix + "-raw"
	jsonKey := keyPrefix + "-json"
	pbKey := keyPrefix + "-pb"
	tombstoneKey := keyPrefix + "-deleted"
	if _, err = producer.ProduceSync(ctx, ProducerMessage{Topic: topics[0], Key: []byte(rawKey), Value: []byte("raw-event"), Headers: []Header{{Key: "event_type", Value: []byte("login")}}}); err != nil {
		t.Fatal(err)
	}
	if _, err = producer.ProduceJSONSync(ctx, JSONMessage{Topic: topics[1], Key: []byte(jsonKey), Value: map[string]any{"player_id": int64(9007199254740991), "level": int64(9)}}); err != nil {
		t.Fatal(err)
	}
	if _, err = producer.ProducePBSync(ctx, PBMessage{Topic: topics[2], Key: []byte(pbKey), Value: wrapperspb.String("protobuf-event")}); err != nil {
		t.Fatal(err)
	}
	deliveries, err := producer.ProduceBatchAsync([]ProducerMessage{{Topic: topics[0], Key: []byte(keyPrefix + "-batch-raw"), Value: []byte("batch-raw")}, {Topic: topics[1], Key: []byte(keyPrefix + "-batch-json"), Value: []byte(`{"batch":true}`)}})
	if err != nil {
		t.Fatal(err)
	}
	for _, delivery := range deliveries {
		if _, err = delivery.Wait(ctx); err != nil {
			t.Fatal(err)
		}
	}
	if _, err = producer.ProduceSync(ctx, ProducerMessage{Topic: topics[3], Key: []byte(tombstoneKey), Value: nil}); err != nil {
		t.Fatal(err)
	}
	if _, err = producer.ProduceAsync(ProducerMessage{Topic: topics[0], Value: make([]byte, (1<<20)+1)}); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("oversized message error=%v", err)
	}

	wanted := map[string]bool{pauseKey: false, rawKey: false, jsonKey: false, pbKey: false, keyPrefix + "-batch-raw": false, keyPrefix + "-batch-json": false, tombstoneKey: false}
	for remaining := len(wanted); remaining > 0; {
		select {
		case message := <-received:
			key := string(message.Key)
			if _, exists := wanted[key]; !exists || wanted[key] {
				continue
			}
			wanted[key] = true
			remaining--
			switch key {
			case rawKey:
				if string(message.Value) != "raw-event" || len(message.Headers) != 1 || message.Headers[0].Key != "event_type" {
					t.Fatalf("raw message=%+v", message)
				}
			case jsonKey:
				var decoded map[string]any
				if err = message.DecodeJSON(&decoded); err != nil {
					t.Fatal(err)
				}
				if decoded["player_id"] != int64(9007199254740991) {
					t.Fatalf("json=%#v", decoded)
				}
			case pbKey:
				decoded := &wrapperspb.StringValue{}
				if err = message.DecodePB(decoded); err != nil || decoded.Value != "protobuf-event" {
					t.Fatalf("pb=%+v err=%v", decoded, err)
				}
			case tombstoneKey:
				if message.Value != nil {
					t.Fatalf("tombstone became non-nil: %#v", message.Value)
				}
			}
		case <-ctx.Done():
			t.Fatalf("messages not received: %#v", wanted)
		}
	}
	stats := producer.Stats()
	if stats.Accepted != 7 || stats.Succeeded != 7 || stats.InFlight != 0 {
		t.Fatalf("producer stats=%+v", stats)
	}
	stopCtx, stopCancel := context.WithTimeout(context.Background(), kafkaIntegrationTimeout)
	defer stopCancel()
	if err = current.Stop(stopCtx); err != nil {
		t.Fatal(err)
	}
}

func TestIntegrationServiceSelfKafkaWorkflow(t *testing.T) {
	cluster, prefix := integrationCluster(t)
	topic := prefix + "-json"
	key := fmt.Sprintf("self-workflow-%d", time.Now().UnixNano())
	processed := make(map[string]int64)
	handled := make(chan struct{}, 1)
	var consumer *Consumer
	// 新 Group 使用 oldest 并按唯一 Key 过滤，避免“Session Setup 已完成但 newest 初始位置仍在建立”的测试时序竞态。
	consumer, err := NewConsumer(ConsumerConfig{Cluster: cluster, GroupID: fmt.Sprintf("origin-self-workflow-%d", time.Now().UnixNano()), Topics: []string{topic}, InitialOffset: "oldest"}, func(ctx context.Context, message *Message) error {
		if string(message.Key) != key {
			return nil
		}
		var event map[string]any
		if decodeErr := message.DecodeJSON(&event); decodeErr != nil {
			return decodeErr
		}
		// 模拟数据库等待：Await 的 wait 函数不占用 Service 工作协程，返回后再安全更新串行业务状态。
		if awaitErr := consumer.Await(ctx, func(waitCtx context.Context) error {
			select {
			case <-time.After(5 * time.Millisecond):
				return nil
			case <-waitCtx.Done():
				return waitCtx.Err()
			}
		}); awaitErr != nil {
			return awaitErr
		}
		processed[string(message.Key)] = event["player_id"].(int64)
		handled <- struct{}{}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	producer, err := NewProducer(ProducerConfig{Cluster: cluster})
	if err != nil {
		t.Fatal(err)
	}
	current, owner := newIntegrationNode(t, producer, consumer)
	ctx, cancel := context.WithTimeout(context.Background(), kafkaIntegrationTimeout)
	defer cancel()
	sent := make(chan error, 1)
	if err = owner.DispatchAsync(func(taskCtx context.Context) {
		sent <- owner.Await(taskCtx, func(waitCtx context.Context) error {
			_, produceErr := producer.ProduceJSONSync(waitCtx, JSONMessage{Topic: topic, Key: []byte(key), Value: map[string]int64{"player_id": 9007199254740991}})
			return produceErr
		})
	}); err != nil {
		t.Fatal(err)
	}
	select {
	case err = <-sent:
		if err != nil {
			t.Fatal(err)
		}
	case <-ctx.Done():
		t.Fatal("Service self workflow did not finish producing")
	}
	select {
	case <-handled:
		if processed[key] != int64(9007199254740991) {
			t.Fatalf("Service state was not updated: %#v", processed)
		}
	case <-ctx.Done():
		t.Fatal("Service self workflow did not consume")
	}
	if err = current.Stop(ctx); err != nil {
		t.Fatal(err)
	}
}

func TestIntegrationAutoTopicCreationRemainsDisabled(t *testing.T) {
	cluster, prefix := integrationCluster(t)
	unknown := fmt.Sprintf("%s-missing-%d", prefix, time.Now().UnixNano())
	adminConfig, err := BuildAdminSaramaConfig(cluster)
	if err != nil {
		t.Fatal(err)
	}
	admin, err := sarama.NewClusterAdmin(cluster.Brokers, adminConfig)
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close()
	topics, err := admin.ListTopics()
	if err != nil {
		t.Fatal(err)
	}
	if _, exists := topics[unknown]; exists {
		t.Fatalf("unexpected Topic already exists: %s", unknown)
	}
	producerConfig := ProducerConfig{Cluster: cluster}
	producerConfig.Cluster.MetadataTimeout = config.Duration(time.Second)
	producerConfig.Cluster.MetadataRetryMax = 1
	producerConfig.Cluster.MetadataRetryBackoff = config.Duration(10 * time.Millisecond)
	producer, err := NewProducer(producerConfig)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err = producer.OnStart(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err = producer.ProduceSync(ctx, ProducerMessage{Topic: unknown, Value: []byte("must-not-create")}); err == nil {
		t.Fatal("unknown Topic was accepted")
	}
	stopCtx, stopCancel := context.WithTimeout(context.Background(), kafkaIntegrationTimeout)
	defer stopCancel()
	_ = producer.OnStop(stopCtx)
	topics, err = admin.ListTopics()
	if err != nil {
		t.Fatal(err)
	}
	if _, exists := topics[unknown]; exists {
		t.Fatalf("Broker auto-created Topic %s", unknown)
	}
}

func TestIntegrationConsumerFailureRedeliversUnmarkedMessage(t *testing.T) {
	cluster, prefix := integrationCluster(t)
	topic := prefix + "-consumer"
	groupID := fmt.Sprintf("origin-redelivery-%d", time.Now().UnixNano())
	key := fmt.Sprintf("redelivery-%d", time.Now().UnixNano())
	producer, err := NewProducer(ProducerConfig{Cluster: cluster})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), kafkaIntegrationTimeout)
	defer cancel()
	if err = producer.OnStart(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err = producer.ProduceSync(ctx, ProducerMessage{Topic: topic, Key: []byte(key), Value: []byte("retry-me")}); err != nil {
		t.Fatal(err)
	}
	defer stopIntegrationProducer(t, producer)

	businessErr := errors.New("intentional handler failure")
	failed := make(chan struct{}, 1)
	first, err := NewConsumer(ConsumerConfig{Cluster: cluster, GroupID: groupID, Topics: []string{topic}, InitialOffset: "oldest"}, func(_ context.Context, message *Message) error {
		if string(message.Key) == key {
			failed <- struct{}{}
			return businessErr
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	firstNode, _ := newIntegrationNode(t, nil, first)
	select {
	case <-failed:
	case <-ctx.Done():
		t.Fatal("first consumer did not receive target")
	}
	deadline := time.Now().Add(5 * time.Second)
	for !errors.Is(first.LastError(), businessErr) && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	stopCtx, stopCancel := context.WithTimeout(context.Background(), kafkaIntegrationTimeout)
	if err = firstNode.Stop(stopCtx); err != nil {
		stopCancel()
		t.Fatal(err)
	}
	stopCancel()

	redelivered := make(chan struct{}, 1)
	second, err := NewConsumer(ConsumerConfig{Cluster: cluster, GroupID: groupID, Topics: []string{topic}, InitialOffset: "oldest"}, func(_ context.Context, message *Message) error {
		if string(message.Key) == key {
			redelivered <- struct{}{}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	secondNode, _ := newIntegrationNode(t, nil, second)
	select {
	case <-redelivered:
	case <-ctx.Done():
		t.Fatal("unmarked message was not redelivered")
	}
	stopCtx, stopCancel = context.WithTimeout(context.Background(), kafkaIntegrationTimeout)
	defer stopCancel()
	if err = secondNode.Stop(stopCtx); err != nil {
		t.Fatal(err)
	}
}

func TestIntegrationCooperativeRebalanceAndClaimRecovery(t *testing.T) {
	cluster, prefix := integrationCluster(t)
	topic := prefix + "-consumer"
	groupID := fmt.Sprintf("origin-rebalance-%d", time.Now().UnixNano())
	config := ConsumerConfig{Cluster: cluster, GroupID: groupID, Topics: []string{topic}, InitialOffset: "newest", BalanceStrategy: "cooperative_sticky"}
	first, err := NewConsumer(config, func(context.Context, *Message) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	firstNode, _ := newIntegrationNode(t, nil, first)
	second, err := NewConsumer(config, func(context.Context, *Message) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	secondNode, _ := newIntegrationNode(t, nil, second)

	ctx, cancel := context.WithTimeout(context.Background(), kafkaIntegrationTimeout)
	defer cancel()
	for first.Stats().Rebalances < 2 || second.Stats().Rebalances < 1 {
		select {
		case <-ctx.Done():
			t.Fatalf("join rebalance did not finish: first=%+v second=%+v", first.Stats(), second.Stats())
		case <-time.After(25 * time.Millisecond):
		}
	}
	if err = secondNode.Stop(ctx); err != nil {
		t.Fatal(err)
	}
	for first.Stats().Rebalances < 3 {
		select {
		case <-ctx.Done():
			t.Fatalf("leave rebalance did not recover first consumer: %+v", first.Stats())
		case <-time.After(25 * time.Millisecond):
		}
	}
	if err = firstNode.Stop(ctx); err != nil {
		t.Fatal(err)
	}
}

func TestIntegrationRecovery(t *testing.T) {
	cluster, prefix := integrationCluster(t)
	producer, err := NewProducer(ProducerConfig{Cluster: cluster})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	if err = producer.OnStart(ctx); err != nil {
		t.Fatal(err)
	}
	defer stopIntegrationProducer(t, producer)
	topic := prefix + "-recovery"
	if _, err = producer.ProduceSync(ctx, ProducerMessage{Topic: topic, Key: []byte("before-restart"), Value: []byte("ok")}); err != nil {
		t.Fatal(err)
	}
	if os.Getenv("ORIGIN_KAFKA_EXPECT_RESTART") == "1" {
		time.Sleep(5 * time.Second)
	}
	deadline := time.Now().Add(60 * time.Second)
	for {
		attemptCtx, attemptCancel := context.WithTimeout(ctx, 5*time.Second)
		_, err = producer.ProduceSync(attemptCtx, ProducerMessage{Topic: topic, Key: []byte("after-restart"), Value: []byte("ok")})
		attemptCancel()
		if err == nil {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("producer did not recover: %v", err)
		}
		time.Sleep(250 * time.Millisecond)
	}
}

func stopIntegrationProducer(t *testing.T, producer *Producer) {
	t.Helper()
	stopCtx, stopCancel := context.WithTimeout(context.Background(), kafkaIntegrationTimeout)
	defer stopCancel()
	if err := producer.OnStop(stopCtx); err != nil {
		t.Errorf("stop integration producer: %v", err)
	}
}
