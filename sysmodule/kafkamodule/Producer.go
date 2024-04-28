package kafkamodule

import (
	"context"
	"github.com/IBM/sarama"
	"github.com/duanhf2012/origin/v2/log"
	"github.com/duanhf2012/origin/v2/service"
	"time"
)

type IProducer interface {
}

type SyncProducer struct {
}

type AsyncProducer struct {
}

type Producer struct {
	service.Module

	sarama.SyncProducer

	sarama.AsyncProducer
}

// NewProducerConfig 新建producerConfig
// kafkaVersion kafka版本
// returnErr，returnSucc 是否返回错误与成功
// requiredAcks  -1 #全量同步确认，强可靠性保证（当所有的 leader 和 follower 都接收成功时）#WaitForAll  1   #leader 确认收到, 默认（仅 leader 反馈）#WaitForLocal  0 #不确认，但是吞吐量大（不 care 结果） #NoResponse
// Idempotent（幂等） 确保信息都准确写入一份副本,用于幂等生产者，当这一项设置为true的时候，生产者将保证生产的消息一定是有序且精确一次的
// partitioner 生成分区器，用于选择向哪个分区发送信息,默认情况下对消息密钥进行散列
func NewProducerConfig(kafkaVersion string, returnErr bool, returnSucc bool, requiredAcks sarama.RequiredAcks, Idempotent bool,
	partitioner sarama.PartitionerConstructor) (*sarama.Config, error) {
	config := sarama.NewConfig()
	var err error
	config.Version, err = sarama.ParseKafkaVersion(kafkaVersion)
	if err != nil {
		return nil, err
	}

	config.Producer.Return.Errors = returnErr
	config.Producer.Return.Successes = returnSucc
	config.Producer.RequiredAcks = requiredAcks
	config.Producer.Partitioner = partitioner
	config.Producer.Timeout = 10 * time.Second

	config.Producer.Idempotent = Idempotent
	if Idempotent == true {
		config.Net.MaxOpenRequests = 1
	}
	return config, nil
}

func (p *Producer) SyncSetup(addr []string, config *sarama.Config) error {
	var err error
	p.SyncProducer, err = sarama.NewSyncProducer(addr, config)
	if err != nil {
		return err
	}

	return nil
}

func (p *Producer) ASyncSetup(addr []string, config *sarama.Config) error {
	var err error
	p.AsyncProducer, err = sarama.NewAsyncProducer(addr, config)
	if err != nil {
		return err
	}

	go func() {
		p.asyncRun()
	}()
	return nil
}

func (p *Producer) asyncRun() {
	for {
		select {
		case sm := <-p.Successes():
			if sm.Metadata == nil {
				break
			}
			asyncReturn := sm.Metadata.(*AsyncReturn)
			asyncReturn.chanReturn <- asyncReturn
		case em := <-p.Errors():
			log.Error("async kafkamodule error", log.ErrorAttr("err", em.Err))
			if em.Msg.Metadata == nil {
				break
			}
			asyncReturn := em.Msg.Metadata.(*AsyncReturn)
			asyncReturn.Err = em.Err
			asyncReturn.chanReturn <- asyncReturn
		}
	}
}

type AsyncReturn struct {
	Msg        *sarama.ProducerMessage
	Err        error
	chanReturn chan *AsyncReturn
}

func (ar *AsyncReturn) WaitOk(ctx context.Context) (*sarama.ProducerMessage, error) {
	asyncReturn := ar.Msg.Metadata.(*AsyncReturn)
	select {
	case <-asyncReturn.chanReturn:
		return asyncReturn.Msg, asyncReturn.Err
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (p *Producer) AsyncSendMessage(msg *sarama.ProducerMessage) *AsyncReturn {
	asyncReturn := AsyncReturn{Msg: msg, chanReturn: make(chan *AsyncReturn, 1)}
	msg.Metadata = &asyncReturn
	p.AsyncProducer.Input() <- msg

	return &asyncReturn
}

func (p *Producer) AsyncPushMessage(msg *sarama.ProducerMessage) {
	p.AsyncProducer.Input() <- msg
}

func (p *Producer) Close() {
	if p.SyncProducer != nil {
		p.SyncProducer.Close()
		p.SyncProducer = nil
	}

	if p.AsyncProducer != nil {
		p.AsyncProducer.Close()
		p.AsyncProducer = nil
	}
}

func (p *Producer) SendMessage(msg *sarama.ProducerMessage) (partition int32, offset int64, err error) {
	return p.SyncProducer.SendMessage(msg)
}

func (p *Producer) SendMessages(msgs []*sarama.ProducerMessage) error {
	return p.SyncProducer.SendMessages(msgs)
}
