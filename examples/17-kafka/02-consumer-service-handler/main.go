// 本示例展示 Kafka 单条/批量 Handler 如何在 Origin Service 串行工作协程中处理业务。
package main

import (
	"context"
	"fmt"
	"time"

	"github.com/duanhf2012/origin/v3/application"
	"github.com/duanhf2012/origin/v3/service"
	"github.com/duanhf2012/origin/v3/sysmodule/kafkamodule"
)

var app = application.New()

type PlayerEvent struct {
	EventID  string `json:"event_id"`
	PlayerID int64  `json:"player_id"`
	Level    int64  `json:"level"`
}

// PlayerEventConsumer 的 processed Map 无锁，因为 Handler 始终在所属 Service 串行工作协程执行。
type PlayerEventConsumer struct {
	kafkamodule.Consumer
	processed map[string]struct{}
}

func (module *PlayerEventConsumer) OnInit() error {
	var current kafkamodule.ConsumerConfig
	if err := module.GetServiceConfigStrict("kafka.player_consumer", &current); err != nil {
		return err
	}
	module.processed = make(map[string]struct{})
	return module.Setup(current, module.handle)
}

func (module *PlayerEventConsumer) handle(ctx context.Context, message *kafkamodule.Message) error {
	var event PlayerEvent
	if err := message.DecodeJSON(&event); err != nil {
		return err
	}
	if _, duplicate := module.processed[event.EventID]; duplicate {
		return nil
	}
	// 数据库/HTTP 等阻塞 I/O 必须使用 Await；wait 函数不在 Service 工作协程运行。
	if err := module.Await(ctx, func(waitCtx context.Context) error {
		timer := time.NewTimer(5 * time.Millisecond)
		defer timer.Stop()
		select {
		case <-timer.C:
			return nil
		case <-waitCtx.Done():
			return waitCtx.Err()
		}
	}); err != nil {
		return err
	}
	// 只有外部副作用和幂等记录都成功后才更新内存状态并返回 nil，随后框架才 Mark Offset。
	module.processed[event.EventID] = struct{}{}
	module.Logger().Info(fmt.Sprintf("handled player event=%s offset=%d", event.EventID, message.Offset))
	return nil
}

// AuditBatchConsumer 展示同一 Topic/Partition 连续消息的有界批处理。
type AuditBatchConsumer struct{ kafkamodule.Consumer }

func (module *AuditBatchConsumer) OnInit() error {
	var current kafkamodule.ConsumerConfig
	if err := module.GetServiceConfigStrict("kafka.audit_consumer", &current); err != nil {
		return err
	}
	return module.SetupBatch(current, module.handleBatch)
}
func (module *AuditBatchConsumer) handleBatch(_ context.Context, batch kafkamodule.Batch) error {
	module.Logger().Info(fmt.Sprintf("audit batch topic=%s partition=%d messages=%d", batch.Topic, batch.Partition, len(batch.Messages)))
	return nil
}

type EventService struct {
	service.Service
	players *PlayerEventConsumer
	audits  *AuditBatchConsumer
}

func (target *EventService) OnInit() error {
	target.players, target.audits = &PlayerEventConsumer{}, &AuditBatchConsumer{}
	if err := target.AddModule(target.players); err != nil {
		return err
	}
	return target.AddModule(target.audits)
}

func init() { app.Setup(&EventService{}) }
func main() { app.Start() }
