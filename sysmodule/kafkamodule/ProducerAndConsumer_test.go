package kafkamodule

import (
	"context"
	"fmt"
	"github.com/IBM/sarama"
	"testing"
	"time"
)

// 对各参数和机制名称的说明：https://blog.csdn.net/u013311345/article/details/129217728
type MsgReceiver struct {
	t *testing.T
}

func (mr *MsgReceiver) Receiver(msgs []*sarama.ConsumerMessage) bool {
	for _, m := range msgs {
		mr.t.Logf("time:%s, topic:%s, partition:%d, offset:%d, key:%s, value:%s", time.Now().Format("2006-01-02 15:04:05.000"), m.Topic, m.Partition, m.Offset, m.Key, string(m.Value))
	}

	return true
}

var addr = []string{"192.168.13.24:9092", "192.168.13.24:9093", "192.168.13.24:9094", "192.168.13.24:9095"}
var topicName = []string{"test_topic_1", "test_topic_2"}
var kafkaVersion = "3.3.1"

func producer(t *testing.T) {
	var admin KafkaAdmin
	err := admin.Setup(kafkaVersion, addr)
	if err != nil {
		t.Fatal(err)
	}

	for _, tName := range topicName {
		if admin.HasTopic(tName) == false {
			err = admin.CreateTopic(tName, 2, 2, false)
			t.Log(err)
		}
	}

	var pd Producer
	cfg, err := NewProducerConfig(kafkaVersion, true, true, sarama.WaitForAll, false, sarama.NewHashPartitioner)
	if err != nil {
		t.Fatal(err)
	}

	err = pd.SyncSetup(addr, cfg)
	if err != nil {
		t.Fatal(err)
	}

	now := time.Now()

	//msgs := make([]*sarama.ProducerMessage, 0, 20000)
	for i := 0; i < 20000; i++ {
		var msg sarama.ProducerMessage
		msg.Key = sarama.StringEncoder(fmt.Sprintf("%d", i))
		msg.Topic = topicName[0]
		msg.Value = sarama.StringEncoder(fmt.Sprintf("i'm %d", i))
		pd.SendMessage(&msg)
		//msgs = append(msgs, &msg)
	}
	//err = pd.SendMessages(msgs)
	//t.Log(err)
	t.Log(time.Now().Sub(now).Milliseconds())
	pd.Close()
}

func producer_async(t *testing.T) {
	var admin KafkaAdmin
	err := admin.Setup(kafkaVersion, addr)
	if err != nil {
		t.Fatal(err)
	}

	for _, tName := range topicName {
		if admin.HasTopic(tName) == false {
			err = admin.CreateTopic(tName, 10, 2, false)
			t.Log(err)
		}
	}

	var pd Producer
	cfg, err := NewProducerConfig(kafkaVersion, true, true, sarama.WaitForAll, false, sarama.NewHashPartitioner)
	if err != nil {
		t.Fatal(err)
	}

	err = pd.ASyncSetup(addr, cfg)
	if err != nil {
		t.Fatal(err)
	}

	now := time.Now()

	msgs := make([]*AsyncReturn, 0, 20000)
	for i := 0; i < 200000; i++ {
		var msg sarama.ProducerMessage
		msg.Key = sarama.StringEncoder(fmt.Sprintf("%d", i))
		msg.Topic = topicName[0]
		msg.Value = sarama.StringEncoder(fmt.Sprintf("i'm %d", i))

		r := pd.AsyncSendMessage(&msg)
		msgs = append(msgs, r)
	}
	//err = pd.SendMessages(msgs)
	//t.Log(err)

	for _, r := range msgs {
		r.WaitOk(context.Background())
		//t.Log(m, e)
	}
	t.Log(time.Now().Sub(now).Milliseconds())

	time.Sleep(1000 * time.Second)
	pd.Close()
}

func consumer(t *testing.T) {
	var admin KafkaAdmin
	err := admin.Setup(kafkaVersion, addr)
	if err != nil {
		t.Fatal(err)
	}

	for _, tName := range topicName {
		if admin.HasTopic(tName) == false {
			err = admin.CreateTopic(tName, 10, 2, false)
			t.Log(err)
		}
	}

	var cg ConsumerGroup
	cfg, err := NewConsumerConfig(kafkaVersion, "sticky", sarama.OffsetOldest)
	if err != nil {
		t.Fatal(err)
	}

	err = cg.Setup(addr, topicName, "test_groupId", cfg, 50*time.Second, 10, &MsgReceiver{t: t})
	t.Log(err)
	time.Sleep(10000 * time.Second)
	cg.Close()
}

func TestConsumerAndProducer(t *testing.T) {
	producer_async(t)
	//go producer(t)
	//producer(t)
	//consumer(t)
}
