package messagequeueservice

import (
	"errors"
	"fmt"
	"github.com/duanhf2012/origin/v2/cluster"
	"github.com/duanhf2012/origin/v2/log"
	"github.com/duanhf2012/origin/v2/rpc"
	"github.com/duanhf2012/origin/v2/util/coroutine"
	"strings"
	"sync/atomic"
	"time"
)

type CustomerSubscriber struct {
	rpc.IRpcHandler
	topic             string
	subscriber        *Subscriber
	fromNodeId        string
	callBackRpcMethod string
	serviceName       string
	StartIndex        uint64
	oneBatchQuantity  int32
	subscribeMethod   SubscribeMethod
	customerId        string

	isStop     int32       //退出标记
	topicCache []TopicData // 从消息队列中取出来的消息的缓存
}

const DefaultOneBatchQuantity = 1000

type SubscribeMethod = int32

const (
	MethodCustom SubscribeMethod = 0 //自定义模式，以消费者设置的StartIndex开始获取或订阅
	MethodLast   SubscribeMethod = 1 //Last模式，以该消费者上次记录的位置开始订阅
)

func (cs *CustomerSubscriber) trySetSubscriberBaseInfo(rpcHandler rpc.IRpcHandler, ss *Subscriber, topic string, subscribeMethod SubscribeMethod, customerId string, fromNodeId string, callBackRpcMethod string, startIndex uint64, oneBatchQuantity int32) error {
	cs.subscriber = ss
	cs.fromNodeId = fromNodeId
	cs.callBackRpcMethod = callBackRpcMethod
	//cs.StartIndex = startIndex
	cs.subscribeMethod = subscribeMethod
	cs.customerId = customerId
	cs.StartIndex = startIndex
	cs.topic = topic
	cs.IRpcHandler = rpcHandler
	if oneBatchQuantity == 0 {
		cs.oneBatchQuantity = DefaultOneBatchQuantity
	} else {
		cs.oneBatchQuantity = oneBatchQuantity
	}

	strRpcMethod := strings.Split(callBackRpcMethod, ".")
	if len(strRpcMethod) != 2 {
		err := errors.New("RpcMethod " + callBackRpcMethod + " is error")
		log.SError(err.Error())
		return err
	}
	cs.serviceName = strRpcMethod[0]

	if cluster.HasService(fromNodeId, cs.serviceName) == false {
		err := fmt.Errorf("nodeId %s cannot found %s", fromNodeId, cs.serviceName)
		log.SError(err.Error())
		return err
	}

	if cluster.GetCluster().IsNodeConnected(fromNodeId) == false {
		err := fmt.Errorf("nodeId %s is disconnect", fromNodeId)
		log.SError(err.Error())
		return err
	}

	if startIndex == 0 {
		now := time.Now()
		zeroTime := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
		//fmt.Println(zeroTime.Unix())
		cs.StartIndex = uint64(zeroTime.Unix() << 32)
	}

	cs.topicCache = make([]TopicData, oneBatchQuantity)
	return nil
}

// Subscribe 开始订阅
func (cs *CustomerSubscriber) Subscribe(rpcHandler rpc.IRpcHandler, ss *Subscriber, topic string, subscribeMethod SubscribeMethod, customerId string, fromNodeId string, callBackRpcMethod string, startIndex uint64, oneBatchQuantity int32) error {
	err := cs.trySetSubscriberBaseInfo(rpcHandler, ss, topic, subscribeMethod, customerId, fromNodeId, callBackRpcMethod, startIndex, oneBatchQuantity)
	if err != nil {
		return err
	}

	cs.subscriber.queueWait.Add(1)
	coroutine.GoRecover(cs.SubscribeRun, -1)
	return nil
}

// UnSubscribe 取消订阅
func (cs *CustomerSubscriber) UnSubscribe() {
	atomic.StoreInt32(&cs.isStop, 1)
}

func (cs *CustomerSubscriber) LoadLastIndex() {
	for {
		if atomic.LoadInt32(&cs.isStop) != 0 {
			log.Info("topic ", cs.topic, "  out of subscription")
			break
		}

		log.Info("customer ", cs.customerId, " start load last index ")
		lastIndex, ret := cs.subscriber.dataPersist.LoadCustomerIndex(cs.topic, cs.customerId)
		if ret == true {
			if lastIndex > 0 {
				cs.StartIndex = lastIndex
			} else {
				//否则直接使用客户端发回来的
			}
			log.Info("customer ", cs.customerId, " load finish,start index is ", cs.StartIndex)
			break
		}

		log.Info("customer ", cs.customerId, " load last index is fail...")
		time.Sleep(5 * time.Second)
	}
}

func (cs *CustomerSubscriber) SubscribeRun() {
	defer cs.subscriber.queueWait.Done()
	log.Info("topic ", cs.topic, "  start subscription")

	//加载之前的位置
	if cs.subscribeMethod == MethodLast {
		cs.LoadLastIndex()
	}

	for {
		if atomic.LoadInt32(&cs.isStop) != 0 {
			log.Info("topic ", cs.topic, "  out of subscription")
			break
		}

		if cs.checkCustomerIsValid() == false {
			break
		}

		//todo 检测退出
		if cs.subscribe() == false {
			log.Info("topic ", cs.topic, "  out of subscription")
			break
		}
	}

	//删除订阅关系
	cs.subscriber.removeCustomer(cs.customerId, cs)
	log.Info("topic ", cs.topic, "  unsubscription")
}

func (cs *CustomerSubscriber) subscribe() bool {
	//先从内存中查找
	topicData, ret := cs.subscriber.queue.FindData(cs.StartIndex+1, cs.oneBatchQuantity, cs.topicCache[:0])
	if ret == true {
		cs.publishToCustomer(topicData)
		return true
	}

	//从持久化数据中来找
	topicData = cs.subscriber.dataPersist.FindTopicData(cs.topic, cs.StartIndex, int64(cs.oneBatchQuantity), cs.topicCache[:0])
	return cs.publishToCustomer(topicData)
}

func (cs *CustomerSubscriber) checkCustomerIsValid() bool {
	//1.检查nodeId是否在线，不在线，直接取消订阅
	if cluster.GetCluster().IsNodeConnected(cs.fromNodeId) == false {
		return false
	}

	//2.验证是否有该服务，如果没有则退出
	if cluster.HasService(cs.fromNodeId, cs.serviceName) == false {
		return false
	}

	return true
}

func (cs *CustomerSubscriber) publishToCustomer(topicData []TopicData) bool {
	if cs.checkCustomerIsValid() == false {
		return false
	}

	if len(topicData) == 0 {
		//没有任何数据待一秒吧
		time.Sleep(time.Second * 1)
		return true
	}

	//3.发送失败重试发送
	var dbQueuePublishReq rpc.DBQueuePublishReq
	var dbQueuePushRes rpc.DBQueuePublishRes
	dbQueuePublishReq.TopicName = cs.topic
	cs.subscriber.dataPersist.OnPushTopicDataToCustomer(cs.topic, topicData)
	for i := 0; i < len(topicData); i++ {
		dbQueuePublishReq.PushData = append(dbQueuePublishReq.PushData, topicData[i].RawData)
	}

	for {
		if atomic.LoadInt32(&cs.isStop) != 0 {
			break
		}

		if cs.checkCustomerIsValid() == false {
			return false
		}

		//推送数据
		err := cs.CallNodeWithTimeout(4*time.Minute, cs.fromNodeId, cs.callBackRpcMethod, &dbQueuePublishReq, &dbQueuePushRes)
		if err != nil {
			time.Sleep(time.Second * 1)
			continue
		}

		//持久化进度
		endIndex := cs.subscriber.dataPersist.GetIndex(&topicData[len(topicData)-1])
		cs.StartIndex = endIndex
		cs.subscriber.dataPersist.PersistIndex(cs.topic, cs.customerId, endIndex)

		return true
	}

	return true
}
