// 本示例展示 RPC 风格 Kafka 生产流程，以及 Raw、JSON、PB、同步、异步和批量外观。
package main

import (
	"context"
	"fmt"
	"time"

	"github.com/duanhf2012/origin/v3/application"
	"github.com/duanhf2012/origin/v3/service"
	"github.com/duanhf2012/origin/v3/sysmodule/kafkamodule"
	"google.golang.org/protobuf/types/known/wrapperspb"
)

var app = application.New()

type PlayerEvent struct {
	EventID  string `json:"event_id"`
	PlayerID int64  `json:"player_id"`
	Level    int64  `json:"level"`
}

// PlayerKafkaModule 组合 Producer，把 Topic、Key 和消息版本集中在业务边界。
type PlayerKafkaModule struct{ kafkamodule.Producer }

func (module *PlayerKafkaModule) OnInit() error {
	var current kafkamodule.ProducerConfig
	if err := module.GetServiceConfigStrict("kafka.producer", &current); err != nil {
		return err
	}
	return module.Setup(current)
}

// PublishRPCEvent 适合 RPC 不需要同步等待 Broker 的路径。
// 编码和队列准入在当前 Service task 中完成；Delivery 等待在 Await worker，回调返回 Service 工作协程。
func (module *PlayerKafkaModule) PublishRPCEvent(ctx context.Context, event PlayerEvent) error {
	delivery, err := module.ProduceJSONAsync(kafkamodule.JSONMessage{Topic: "origin-kafka-json", Key: []byte(fmt.Sprint(event.PlayerID)), Value: event, Headers: []kafkamodule.Header{{Key: "schema", Value: []byte("player-event/v1")}}})
	if err != nil {
		return err
	}
	return module.DispatchDelivery(ctx, delivery, func(_ context.Context, result kafkamodule.DeliveryResult) {
		if result.Err != nil {
			module.Logger().Error("Kafka async delivery failed: " + result.Err.Error())
			return
		}
		module.Logger().Info(fmt.Sprintf("Kafka async delivered partition=%d offset=%d", result.Metadata.Partition, result.Metadata.Offset))
	})
}

// PublishCritical 等待 Broker Ack；调用者应从 Service task 用 Await 包住它。
func (module *PlayerKafkaModule) PublishCritical(ctx context.Context, event PlayerEvent) error {
	metadata, err := module.ProduceJSONSync(ctx, kafkamodule.JSONMessage{Topic: "origin-kafka-json", Key: []byte(event.EventID), Value: event})
	if err != nil {
		return err
	}
	module.Logger().Info(fmt.Sprintf("critical event offset=%d", metadata.Offset))
	return nil
}

func (module *PlayerKafkaModule) PublishOtherFormats(ctx context.Context) error {
	if _, err := module.ProduceSync(ctx, kafkamodule.ProducerMessage{Topic: "origin-kafka-raw", Key: []byte("raw-1"), Value: []byte("raw payload")}); err != nil {
		return err
	}
	if _, err := module.ProducePBSync(ctx, kafkamodule.PBMessage{Topic: "origin-kafka-pb", Key: []byte("pb-1"), Value: wrapperspb.String("protobuf payload")}); err != nil {
		return err
	}
	results, err := module.ProduceJSONBatchSync(ctx, []kafkamodule.JSONMessage{{Topic: "origin-kafka-json", Key: []byte("batch-1"), Value: PlayerEvent{EventID: "batch-1", PlayerID: 1001, Level: 20}}, {Topic: "origin-kafka-json", Key: []byte("batch-2"), Value: PlayerEvent{EventID: "batch-2", PlayerID: 1002, Level: 21}}})
	if err != nil {
		return fmt.Errorf("batch results=%+v: %w", results, err)
	}
	return nil
}

type PlayerService struct {
	service.Service
	kafka *PlayerKafkaModule
}

func (target *PlayerService) OnInit() error {
	target.kafka = &PlayerKafkaModule{}
	return target.AddModule(target.kafka)
}
func (target *PlayerService) OnStart(context.Context) error {
	if id := target.AfterFunc(100*time.Millisecond, func(ctx context.Context, _ service.TimerID) {
		event := PlayerEvent{EventID: "login-1001", PlayerID: 1001, Level: 19}
		if err := target.kafka.PublishRPCEvent(ctx, event); err != nil {
			target.Logger().Error(err.Error())
		}
		if err := target.Await(ctx, func(waitCtx context.Context) error {
			if err := target.kafka.PublishCritical(waitCtx, event); err != nil {
				return err
			}
			return target.kafka.PublishOtherFormats(waitCtx)
		}); err != nil {
			target.Logger().Error("Kafka sync workflow failed: " + err.Error())
		}
	}); id == service.InvalidTimerID {
		return fmt.Errorf("schedule Kafka producer demo failed")
	}
	return nil
}

func init() { app.Setup(&PlayerService{}) }
func main() { app.Start() }
