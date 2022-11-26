package messagequeueservice

import (
	"errors"
	"github.com/duanhf2012/origin/log"
	"github.com/duanhf2012/origin/util/coroutine"
	"sync"
	"sync/atomic"
	"time"
)

type TopicData struct {
	Seq     uint64 //序号
	RawData []byte //原始数据

	ExtendParam interface{} //扩展参数
}

func (t TopicData) GetValue() uint64 {
	return t.Seq
}

var topicFullError = errors.New("topic room is full")

const DefaultOnceProcessTopicDataNum = 1024 //一次处理的topic数量，考虑批量落地的数量
const DefaultMaxTopicBacklogNum = 100000    //处理的channel最大数量
const DefaultMemoryQueueLen = 50000         //内存的最大长度
const maxTryPersistNum = 3000               //最大重试次数,约>5分钟

type TopicRoom struct {
	topic        string         //主题名称
	channelTopic chan TopicData //主题push过来待处理的数据

	Subscriber //订阅器

	//序号生成
	seq      uint32
	lastTime int64

	//onceProcessTopicDataNum int //一次处理的订阅数据最大量，方便订阅器Subscriber和QueueDataProcessor批量处理
	StagingBuff []TopicData

	isStop int32
}

// maxProcessTopicBacklogNum:主题最大积压数量
func (tr *TopicRoom) Init(maxTopicBacklogNum int32, memoryQueueLen int32, topic string, queueWait *sync.WaitGroup, dataPersist QueueDataPersist) {
	if maxTopicBacklogNum == 0 {
		maxTopicBacklogNum = DefaultMaxTopicBacklogNum
	}

	tr.channelTopic = make(chan TopicData, maxTopicBacklogNum)
	tr.topic = topic
	tr.dataPersist = dataPersist
	tr.queueWait = queueWait
	tr.StagingBuff = make([]TopicData, DefaultOnceProcessTopicDataNum)
	tr.queueWait.Add(1)
	tr.Subscriber.Init(memoryQueueLen)
	coroutine.GoRecover(tr.topicRoomRun, -1)
}

func (tr *TopicRoom) Publish(data [][]byte) error {
	if len(tr.channelTopic)+len(data) > cap(tr.channelTopic) {
		return topicFullError
	}

	//生成有序序号
	for _, rawData := range data {
		tr.channelTopic <- TopicData{RawData: rawData, Seq: tr.NextSeq()}
	}

	return nil
}

func (tr *TopicRoom) NextSeq() uint64 {
	now := time.Now()

	nowSec := now.Unix()
	if nowSec != tr.lastTime {
		tr.seq = 0
		tr.lastTime = nowSec
	}
	//必需从1开始，查询时seq>0
	tr.seq += 1

	return uint64(nowSec)<<32 | uint64(tr.seq)
}

func (tr *TopicRoom) Stop() {
	atomic.StoreInt32(&tr.isStop, 1)
}

func (tr *TopicRoom) topicRoomRun() {
	defer tr.queueWait.Done()

	log.SRelease("topic room ", tr.topic, " is running..")
	for {
		if atomic.LoadInt32(&tr.isStop) != 0 {
			break
		}
		stagingBuff := tr.StagingBuff[:0]

		for i := 0; i < len(tr.channelTopic) && i < DefaultOnceProcessTopicDataNum; i++ {
			topicData := <-tr.channelTopic

			stagingBuff = append(stagingBuff, topicData)
		}
		tr.Subscriber.dataPersist.OnReceiveTopicData(tr.topic, stagingBuff)
		//持久化与放内存
		if len(stagingBuff) == 0 {
			time.Sleep(time.Millisecond * 50)
			continue
		}

		//如果落地失败，最大重试maxTryPersistNum次数
		for retryCount := 0; retryCount < maxTryPersistNum; {
			//持久化处理
			stagingBuff, savedBuff, ret := tr.PersistTopicData(tr.topic, stagingBuff, retryCount+1)
			
			if ret == true {
				// 1. 把成功存储的数据放入内存中
				if len(savedBuff) > 0 {
					tr.PushTopicDataToQueue(tr.topic, savedBuff)
				}
				
				// 2. 如果存档成功，并且有后续批次，则继续存档
				if ret == true && len(stagingBuff) > 0 {
					continue
				}
				
				// 3. 成功了，跳出
				break
			} else {
				//计数增加一次，并且等待100ms，继续重试
				retryCount++
				time.Sleep(time.Millisecond * 100)
			}
		}
	}

	//将所有的订阅取消
	tr.customerLocker.Lock()
	for _, customer := range tr.mapCustomer {
		customer.UnSubscribe()
	}
	tr.customerLocker.Unlock()

	log.SRelease("topic room ", tr.topic, " is stop")
}
