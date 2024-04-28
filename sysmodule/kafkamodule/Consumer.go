package kafkamodule

import (
	"context"
	"fmt"
	"github.com/IBM/sarama"
	"github.com/duanhf2012/origin/v2/log"
	"sync"
	"time"
)

type ConsumerGroup struct {
	sarama.ConsumerGroup
	waitGroup sync.WaitGroup

	chanExit chan error
	ready    chan bool
	cancel   context.CancelFunc
	groupId  string
}

func NewConsumerConfig(kafkaVersion string, assignor string, offsetsInitial int64) (*sarama.Config, error) {
	var err error

	config := sarama.NewConfig()
	config.Version, err = sarama.ParseKafkaVersion(kafkaVersion)
	config.Consumer.Offsets.Initial = offsetsInitial
	config.Consumer.Offsets.AutoCommit.Enable = false

	switch assignor {
	case "sticky":
		// 黏性roundRobin,rebalance之后首先保证前面的分配,从后面剥离
		// topic:T0{P0,P1,P2,P3,P4,P5},消费者:C1,C2
		//	---------------before rebalance:即roundRobin
		// C1: T0{P0}	 T0{P2}		T0{P4}
		// C2:      T0{P1}    T0{P3}     T0{P5}
		// ----------------after rebalance:增加了一个消费者
		// C1: T0{P0}	 T0{P2}
		// C2:      T0{P1}    T0{P3}
		// C3:						 T0{P4}   T0{P5} until每个消费者的分区数误差不超过1
		config.Consumer.Group.Rebalance.GroupStrategies = []sarama.BalanceStrategy{sarama.NewBalanceStrategySticky()}
	case "roundrobin":
		// roundRobin --逐个平均分发
		// topic: T0{P0,P1,P2},T1{P0,P1,P2,P3}两个消费者C1,C2
		// C1: T0{P0}	 T0{P2}	   T1{P1}     T1{P3}
		// C2: 		T0{P1}	  T1{P0}    T1{P2}
		config.Consumer.Group.Rebalance.GroupStrategies = []sarama.BalanceStrategy{sarama.NewBalanceStrategyRoundRobin()}
	case "range":
		// 默认值 --一次平均分发
		// topic: T0{P0,P1,P2,P3},T1{P0,P1,P2,P3},两个消费者C1,C2
		// T1分区总数6 / 消费者数2 = 3 ,即该会话的分区每个消费者分3个
		// T2分区总数4 / 消费者数2 = 2 ,即该会话的分区每个消费者分2个
		// C1: T0{P0, P1, P2} 			  T1{P0, P1}
		// C2: 				T0{P3, P4, P5}  		 T1{P2, P3}
		config.Consumer.Group.Rebalance.GroupStrategies = []sarama.BalanceStrategy{sarama.NewBalanceStrategyRange()}
	default:
		return nil, fmt.Errorf("Unrecognized consumer group partition assignor: %s", assignor)
	}

	if err != nil {
		return nil, err
	}

	return config, nil
}

type IMsgReceiver interface {
	Receiver(msgs []*sarama.ConsumerMessage) bool
}

func (c *ConsumerGroup) Setup(addr []string, topics []string, groupId string, config *sarama.Config, receiverInterval time.Duration, maxReceiverNum int, msgReceiver IMsgReceiver) error {
	var err error
	c.ConsumerGroup, err = sarama.NewConsumerGroup(addr, groupId, config)
	if err != nil {
		return nil
	}
	c.groupId = groupId
	c.chanExit = make(chan error, 1)

	var handler ConsumerGroupHandler
	handler.receiver = msgReceiver
	handler.maxReceiverNum = maxReceiverNum
	handler.receiverInterval = receiverInterval
	handler.chanExit = c.chanExit

	var ctx context.Context
	ctx, c.cancel = context.WithCancel(context.Background())

	c.waitGroup.Add(1)
	go func() {
		defer c.waitGroup.Done()

		for {
			if err = c.Consume(ctx, topics, &handler); err != nil {
				// 当setup失败的时候，error会返回到这里
				log.Error("Error from consumer", log.Any("err", err))
				return
			}

			// check if context was cancelled, signaling that the consumer should stop
			if ctx.Err() != nil {
				log.Info("consumer stop", log.Any("info", ctx.Err()))
			}

			c.chanExit <- err
		}
	}()

	err = <-c.chanExit
	//已经准备好了
	return err
}

func (c *ConsumerGroup) Close() {
	log.Info("close consumerGroup")
	//1.cancel掉
	c.cancel()

	//2.关闭连接
	err := c.ConsumerGroup.Close()
	if err != nil {
		log.Error("close consumerGroup fail", log.Any("err", err.Error()))
	}

	//3.等待退出
	c.waitGroup.Wait()
}

type ConsumerGroupHandler struct {
	receiver IMsgReceiver

	receiverInterval time.Duration
	maxReceiverNum   int

	//mapTopicOffset map[string]map[int32]int //map[topic]map[partitionId]offsetInfo
	mapTopicData map[string]*MsgData
	mx           sync.Mutex

	chanExit    chan error
	isRebalance bool //是否为再平衡
	//stopSig      *int32
}

type MsgData struct {
	sync.Mutex
	msg []*sarama.ConsumerMessage

	mapPartitionOffset map[int32]int64
}

func (ch *ConsumerGroupHandler) Flush(session sarama.ConsumerGroupSession, topic string) {
	if topic != "" {
		msgData := ch.GetMsgData(topic)
		msgData.flush(session, ch.receiver, topic)
		return
	}

	for tp, msgData := range ch.mapTopicData {
		msgData.flush(session, ch.receiver, tp)
	}
}

func (ch *ConsumerGroupHandler) GetMsgData(topic string) *MsgData {
	ch.mx.Lock()
	defer ch.mx.Unlock()

	msgData := ch.mapTopicData[topic]
	if msgData == nil {
		msgData = &MsgData{}
		msgData.msg = make([]*sarama.ConsumerMessage, 0, ch.maxReceiverNum)
		ch.mapTopicData[topic] = msgData
	}

	return msgData
}

func (md *MsgData) flush(session sarama.ConsumerGroupSession, receiver IMsgReceiver, topic string) {
	if len(md.msg) == 0 {
		return
	}

	//发送给接收者
	for {
		ok := receiver.Receiver(md.msg)
		if ok == true {
			break
		}
	}

	for pId, offset := range md.mapPartitionOffset {

		session.MarkOffset(topic, pId, offset+1, "")
		log.Info(fmt.Sprintf("topic %s,pid %d,offset %d", topic, pId, offset+1))
	}
	session.Commit()
	//log.Info("commit")
	//time.Sleep(1000 * time.Second)
	//置空
	md.msg = md.msg[:0]
	clear(md.mapPartitionOffset)
}

func (md *MsgData) appendMsg(session sarama.ConsumerGroupSession, msg *sarama.ConsumerMessage, receiver IMsgReceiver, maxReceiverNum int) {
	md.Lock()
	defer md.Unlock()

	//收到的offset只会越来越大在
	if md.mapPartitionOffset == nil {
		md.mapPartitionOffset = make(map[int32]int64, 10)
	}

	md.mapPartitionOffset[msg.Partition] = msg.Offset

	md.msg = append(md.msg, msg)
	if len(md.msg) < maxReceiverNum {
		return
	}

	md.flush(session, receiver, msg.Topic)
}

func (ch *ConsumerGroupHandler) AppendMsg(session sarama.ConsumerGroupSession, msg *sarama.ConsumerMessage) {
	dataMsg := ch.GetMsgData(msg.Topic)
	dataMsg.appendMsg(session, msg, ch.receiver, ch.maxReceiverNum)
}

func (ch *ConsumerGroupHandler) Setup(session sarama.ConsumerGroupSession) error {
	ch.mapTopicData = make(map[string]*MsgData, 128)

	if ch.isRebalance == false {
		ch.chanExit <- nil
	}

	ch.isRebalance = true
	return nil
}

func (ch *ConsumerGroupHandler) Cleanup(session sarama.ConsumerGroupSession) error {
	ch.Flush(session, "")
	return nil
}

func (ch *ConsumerGroupHandler) ConsumeClaim(session sarama.ConsumerGroupSession, claim sarama.ConsumerGroupClaim) error {

	ticker := time.NewTicker(ch.receiverInterval)

	for {
		select {
		case msg := <-claim.Messages():
			if msg == nil {
				log.SWarning("claim will exit", log.Any("topic", claim.Topic()), log.Any("Partition", claim.Partition()))
				return nil
			}
			ch.AppendMsg(session, msg)
		case <-ticker.C:
			ch.Flush(session, claim.Topic())
		case <-session.Context().Done():
			return nil
		}
	}
}

/*
阿里云参数说明：https://sls.aliyun.com/doc/oscompatibledemo/sarama_go_kafka_consume.html
conf.Net.TLS.Enable = true
	conf.Net.SASL.Enable = true
	conf.Net.SASL.User = project
	conf.Net.SASL.Password = fmt.Sprintf("%s#%s", accessId, accessKey)
	conf.Net.SASL.Mechanism = "PLAIN"



	conf.Net.TLS.Enable = true
	conf.Net.SASL.Enable = true
	conf.Net.SASL.User = project
	conf.Net.SASL.Password = fmt.Sprintf("%s#%s", accessId, accessKey)
	conf.Net.SASL.Mechanism = "PLAIN"

	conf.Consumer.Fetch.Min = 1
	conf.Consumer.Fetch.Default = 1024 * 1024
	conf.Consumer.Retry.Backoff = 2 * time.Second
	conf.Consumer.MaxWaitTime = 250 * time.Millisecond
	conf.Consumer.MaxProcessingTime = 100 * time.Millisecond
	conf.Consumer.Return.Errors = false
	conf.Consumer.Offsets.AutoCommit.Enable = true
	conf.Consumer.Offsets.AutoCommit.Interval = 1 * time.Second
	conf.Consumer.Offsets.Initial = sarama.OffsetOldest
	conf.Consumer.Offsets.Retry.Max = 3
*/
