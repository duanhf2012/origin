package messagequeueservice

import (
	"github.com/duanhf2012/origin/log"
	"github.com/duanhf2012/origin/rpc"

	"sync"
)

// 订阅器
type Subscriber struct {
	customerLocker sync.RWMutex
	mapCustomer    map[string]*CustomerSubscriber
	queue          MemoryQueue
	dataPersist    QueueDataPersist //对列数据处理器
	queueWait      *sync.WaitGroup
}

func (ss *Subscriber) Init(memoryQueueCap int32) {
	ss.queue.Init(memoryQueueCap)
	ss.mapCustomer = make(map[string]*CustomerSubscriber, 5)
}

func (ss *Subscriber) PushTopicDataToQueue(topic string, topics []TopicData) {
	for i := 0; i < len(topics); i++ {
		ss.queue.Push(&topics[i])
	}
}

func (ss *Subscriber) PersistTopicData(topic string, topics []TopicData, retryCount int) ([]TopicData, []TopicData, bool) {
	return ss.dataPersist.PersistTopicData(topic, topics, retryCount)
}

func (ss *Subscriber) TopicSubscribe(rpcHandler rpc.IRpcHandler, subScribeType rpc.SubscribeType, subscribeMethod SubscribeMethod, fromNodeId int, callBackRpcMethod string, topic string, customerId string, StartIndex uint64, oneBatchQuantity int32) error {
	//取消订阅时
	if subScribeType == rpc.SubscribeType_Unsubscribe {
		ss.UnSubscribe(customerId)
		return nil
	} else {
		ss.customerLocker.Lock()
		customerSubscriber, ok := ss.mapCustomer[customerId]
		if ok == true {
			//已经订阅过，则取消订阅
			customerSubscriber.UnSubscribe()
			delete(ss.mapCustomer, customerId)
		}

		//不存在，则订阅
		customerSubscriber = &CustomerSubscriber{}
		ss.mapCustomer[customerId] = customerSubscriber
		ss.customerLocker.Unlock()

		err := customerSubscriber.Subscribe(rpcHandler, ss, topic, subscribeMethod, customerId, fromNodeId, callBackRpcMethod, StartIndex, oneBatchQuantity)
		if err != nil {
			return err
		}

		if ok == true {
			log.SRelease("repeat subscription for customer ", customerId)
		} else {
			log.SRelease("subscription for customer ", customerId)
		}

	}

	return nil
}

func (ss *Subscriber) UnSubscribe(customerId string) {
	ss.customerLocker.RLocker()
	defer ss.customerLocker.RUnlock()

	customerSubscriber, ok := ss.mapCustomer[customerId]
	if ok == false {
		log.SWarning("failed to unsubscribe customer " + customerId)
		return
	}

	customerSubscriber.UnSubscribe()
}

func (ss *Subscriber) removeCustomer(customerId string, cs *CustomerSubscriber) {

	ss.customerLocker.Lock()
	//确保删掉是当前的关系。有可能在替换订阅时，将该customer替换的情况
	customer, _ := ss.mapCustomer[customerId]
	if customer == cs {
		delete(ss.mapCustomer, customerId)
	}
	ss.customerLocker.Unlock()
}
