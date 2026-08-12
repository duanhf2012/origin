// 本示例展示 Managed Producer 与业务自行拥有的 Native Sarama Admin/配置如何并存。
package main

import (
	"context"
	"fmt"
	"time"

	"github.com/IBM/sarama"
	"github.com/duanhf2012/origin/v3/application"
	"github.com/duanhf2012/origin/v3/service"
	"github.com/duanhf2012/origin/v3/sysmodule/kafkamodule"
)

var app = application.New()

type ManagedProducerModule struct{ kafkamodule.Producer }

func (module *ManagedProducerModule) OnInit() error {
	var current kafkamodule.ProducerConfig
	if err := module.GetServiceConfigStrict("kafka.producer", &current); err != nil {
		return err
	}
	// Hook 在 OnStart goroutine 执行；适合低频 Sarama 能力，但不能破坏 Managed 不变量。
	return module.Setup(current, kafkamodule.WithProducerSaramaConfig(func(current *sarama.Config) error {
		current.Metadata.Full = false
		return nil
	}))
}

// NativeAdminModule 自己拥有 ClusterAdmin；框架不会替它关闭 Native 资源。
type NativeAdminModule struct {
	service.Module
	cluster kafkamodule.ClusterConfig
	admin   sarama.ClusterAdmin
}

func (module *NativeAdminModule) OnInit() error {
	return module.GetServiceConfigStrict("kafka.cluster", &module.cluster)
}
func (module *NativeAdminModule) OnStart(context.Context) error {
	current, err := kafkamodule.BuildAdminSaramaConfig(module.cluster)
	if err != nil {
		return err
	}
	module.admin, err = sarama.NewClusterAdmin(module.cluster.Brokers, current)
	return err
}
func (module *NativeAdminModule) OnStop(context.Context) error {
	if module.admin == nil {
		return nil
	}
	err := module.admin.Close()
	module.admin = nil
	return err
}
func (module *NativeAdminModule) ListTopics() (map[string]sarama.TopicDetail, error) {
	return module.admin.ListTopics()
}

// BuildTransactionSkeleton 只展示自由层入口，不宣称完整 EOS。
// 真正事务还必须设计 Begin/Commit/Abort、错误分类、Consumer Offset 与外部数据库一致性边界。
func (module *NativeAdminModule) BuildTransactionSkeleton(transactionID string) (*sarama.Config, error) {
	return kafkamodule.BuildSaramaConfig(module.cluster, kafkamodule.WithSaramaConfig(func(current *sarama.Config) error {
		current.Producer.Transaction.ID = transactionID
		current.Producer.Idempotent = true
		current.Producer.RequiredAcks = sarama.WaitForAll
		current.Net.MaxOpenRequests = 1
		return nil
	}))
}

type KafkaToolsService struct {
	service.Service
	producer *ManagedProducerModule
	admin    *NativeAdminModule
}

func (target *KafkaToolsService) OnInit() error {
	target.producer, target.admin = &ManagedProducerModule{}, &NativeAdminModule{}
	if err := target.AddModule(target.producer); err != nil {
		return err
	}
	return target.AddModule(target.admin)
}
func (target *KafkaToolsService) OnStart(context.Context) error {
	if id := target.AfterFunc(100*time.Millisecond, func(ctx context.Context, _ service.TimerID) {
		if err := target.Await(ctx, func(waitCtx context.Context) error {
			topics, err := target.admin.ListTopics()
			if err != nil {
				return err
			}
			target.Logger().Info(fmt.Sprintf("native Admin listed %d topics", len(topics)))
			_, err = target.producer.ProduceSync(waitCtx, kafkamodule.ProducerMessage{Topic: "origin-kafka-raw", Key: []byte("managed-native"), Value: []byte("managed producer")})
			return err
		}); err != nil {
			target.Logger().Error(err.Error())
		}
	}); id == service.InvalidTimerID {
		return fmt.Errorf("schedule Kafka native demo failed")
	}
	return nil
}

func init() { app.Setup(&KafkaToolsService{}) }
func main() { app.Start() }
