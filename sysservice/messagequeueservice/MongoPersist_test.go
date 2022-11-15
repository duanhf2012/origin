package messagequeueservice

import (
	"fmt"
	"go.mongodb.org/mongo-driver/bson"
	"testing"
	"time"
)

var seq uint64
var lastTime int64

func NextSeq(addDays int) uint64 {
	now := time.Now().AddDate(0, 0, addDays)

	nowSec := now.Unix()
	if nowSec != lastTime {
		seq = 0
		lastTime = nowSec
	}
	//必需从1开始，查询时seq>0
	seq += 1

	return uint64(nowSec)<<32 | uint64(seq)
}

func Test_MongoPersist(t *testing.T) {
	//1.初始化
	var mongoPersist MongoPersist
	mongoPersist.url = "mongodb://admin:123456@192.168.2.15:27017/?minPoolSize=5&maxPoolSize=35&maxIdleTimeMS=30000"
	mongoPersist.dbName = "MongoPersistTest"
	mongoPersist.retryCount = 10
	mongoPersist.OnInit()

	//2.
	//加载索引
	index, ret := mongoPersist.LoadCustomerIndex("TestTopic", "TestCustomer")
	fmt.Println(index, ret)

	now := time.Now()
	zeroTime := time.Date(now.Year(), now.Month(), now.Day()+1, 0, 0, 0, 0, now.Location())
	//fmt.Println(zeroTime.Unix())
	startIndex := uint64(zeroTime.Unix()<<32) | 1

	//存储索引
	mongoPersist.PersistIndex("TestTopic", "TestCustomer", startIndex)

	//加载索引
	index, ret = mongoPersist.LoadCustomerIndex("TestTopic", "TestCustomer")

	type RowTest struct {
		Name    string      `bson:"Name,omitempty"`
		MapTest map[int]int `bson:"MapTest,omitempty"`
		Message string      `bson:"Message,omitempty"`
	}

	type RowTest2 struct {
		Id      uint64      `bson:"_id,omitempty"`
		Name    string      `bson:"Name,omitempty"`
		MapTest map[int]int `bson:"MapTest,omitempty"`
		Message string      `bson:"Message,omitempty"`
	}

	//存档
	var findStartIndex uint64
	var topicData []TopicData
	for i := 1; i <= 1000; i++ {

		var rowTest RowTest
		rowTest.Name = fmt.Sprintf("Name_%d", i)
		rowTest.MapTest = make(map[int]int, 1)
		rowTest.MapTest[i] = i*1000 + i
		rowTest.Message = fmt.Sprintf("xxxxxxxxxxxxxxxxxx%d", i)
		byteRet, _ := bson.Marshal(rowTest)

		var dataSeq uint64
		if i <= 500 {
			dataSeq = NextSeq(-1)
		} else {
			dataSeq = NextSeq(0)
		}

		topicData = append(topicData, TopicData{RawData: byteRet, Seq: dataSeq})

		if i == 1 {
			findStartIndex = topicData[0].Seq
		}
	}

	mongoPersist.OnReceiveTopicData("TestTopic", topicData)

	for {
		if len(topicData) == 0 {
			break
		}

		topicData, ret = mongoPersist.PersistTopicData("TestTopic", topicData, 1)
		fmt.Println(ret)
	}

	//
	for {
		retTopicData := mongoPersist.FindTopicData("TestTopic", findStartIndex, 300)
		for i, data := range retTopicData {
			var rowTest RowTest2
			bson.Unmarshal(data.RawData, &rowTest)
			t.Log(rowTest.Name)

			if i == len(retTopicData)-1 {
				findStartIndex = mongoPersist.GetIndex(&data)
			}
		}

		t.Log("..................")
		if len(retTopicData) == 0 {
			break
		}
	}

	//t.Log(mongoPersist.GetIndex(&retTopicData[0]))
	//t.Log(mongoPersist.GetIndex(&retTopicData[len(retTopicData)-1]))
}
