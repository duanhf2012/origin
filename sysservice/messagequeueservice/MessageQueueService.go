package messagequeueservice

import (
	"errors"
	"fmt"
	"github.com/duanhf2012/origin/log"
	"github.com/duanhf2012/origin/service"
	"github.com/duanhf2012/origin/rpc"
	"sync"
)

type QueueDataPersist interface {
	service.IModule

	OnExit()
	OnReceiveTopicData(topic string, topicData []TopicData)                                   //当收到推送过来的数据时
	OnPushTopicDataToCustomer(topic string, topicData []TopicData)                            //当推送数据到Customer时回调
	PersistTopicData(topic string, topicData []TopicData, retryCount int) ([]TopicData, []TopicData, bool) //持久化数据,失败则返回false，上层会重复尝试，直到成功，建议在函数中加入次数，超过次数则返回true
	FindTopicData(topic string, startIndex uint64, limit int64, topicBuff []TopicData) []TopicData                   //查找数据,参数bool代表数据库查找是否成功
	LoadCustomerIndex(topic string, customerId string) (uint64, bool)                         //false时代表获取失败，一般是读取错误，会进行重试。如果不存在时，返回(0,true)
	GetIndex(topicData *TopicData) uint64                                                     //通过topic数据获取进度索引号
	PersistIndex(topic string, customerId string, index uint64)                               //持久化进度索引号
}

type MessageQueueService struct {
	service.Service

	sync.Mutex
	mapTopicRoom map[string]*TopicRoom

	queueWait   sync.WaitGroup
	dataPersist QueueDataPersist

	memoryQueueLen            int32
	maxProcessTopicBacklogNum int32 //最大积压的数据量，因为是写入到channel中，然后由协程取出再持久化,不设置有默认值100000
}

func (ms *MessageQueueService) OnInit() error {
	ms.mapTopicRoom = map[string]*TopicRoom{}
	errC := ms.ReadCfg()
	if errC != nil {
		return errC
	}

	if ms.dataPersist == nil {
		return errors.New("not setup QueueDataPersist.")
	}

	_, err := ms.AddModule(ms.dataPersist)
	if err != nil {
		return err
	}

	return nil
}

func (ms *MessageQueueService) ReadCfg() error {
	mapDBServiceCfg, ok := ms.GetService().GetServiceCfg().(map[string]interface{})
	if ok == false {
		return fmt.Errorf("MessageQueueService config is error")
	}

	maxProcessTopicBacklogNum, ok := mapDBServiceCfg["MaxProcessTopicBacklogNum"]
	if ok == false {
		ms.maxProcessTopicBacklogNum = DefaultMaxTopicBacklogNum
		log.SRelease("MaxProcessTopicBacklogNum config is set to the default value of ", maxProcessTopicBacklogNum)
	} else {
		ms.maxProcessTopicBacklogNum = int32(maxProcessTopicBacklogNum.(float64))
	}

	memoryQueueLen, ok := mapDBServiceCfg["MemoryQueueLen"]
	if ok == false {
		ms.memoryQueueLen = DefaultMemoryQueueLen
		log.SRelease("MemoryQueueLen config is set to the default value of ", DefaultMemoryQueueLen)
	} else {
		ms.memoryQueueLen = int32(memoryQueueLen.(float64))
	}

	return nil
}

func (ms *MessageQueueService) Setup(dataPersist QueueDataPersist) {
	ms.dataPersist = dataPersist
}

func (ms *MessageQueueService) OnRelease() {

	//停止所有的TopicRoom房间
	ms.Lock()
	for _, room := range ms.mapTopicRoom {
		room.Stop()
	}
	ms.Unlock()

	//释放时确保所有的协程退出
	ms.queueWait.Wait()

	//通知持久化对象
	ms.dataPersist.OnExit()
}

func (ms *MessageQueueService) GetTopicRoom(topic string) *TopicRoom {
	ms.Lock()
	defer ms.Unlock()
	topicRoom := ms.mapTopicRoom[topic]
	if topicRoom != nil {
		return topicRoom
	}

	topicRoom = &TopicRoom{}
	topicRoom.Init(ms.maxProcessTopicBacklogNum, ms.memoryQueueLen, topic, &ms.queueWait, ms.dataPersist)
	ms.mapTopicRoom[topic] = topicRoom

	return topicRoom
}

func (ms *MessageQueueService) RPC_Publish(inParam *rpc.DBQueuePublishReq, outParam *rpc.DBQueuePublishRes) error {

	topicRoom := ms.GetTopicRoom(inParam.TopicName)
	return topicRoom.Publish(inParam.PushData)
}

func (ms *MessageQueueService) RPC_Subscribe(req *rpc.DBQueueSubscribeReq, res *rpc.DBQueueSubscribeRes) error {
	topicRoom := ms.GetTopicRoom(req.TopicName)
	return topicRoom.TopicSubscribe(ms.GetRpcHandler(), req.SubType, int32(req.Method), int(req.FromNodeId), req.RpcMethod, req.TopicName, req.CustomerId, req.StartIndex, req.OneBatchQuantity)
}
