package messagequeueservice

import (
	"github.com/duanhf2012/origin/util/algorithms"
	"sync"
)

type MemoryQueue struct {
	subscriber *Subscriber

	topicQueue []TopicData
	head       int32
	tail       int32

	locker sync.RWMutex
}

func (mq *MemoryQueue) Init(cap int32) {
	mq.topicQueue = make([]TopicData, cap+1)
}

// 从队尾Push数据
func (mq *MemoryQueue) Push(topicData *TopicData) bool {
	mq.locker.Lock()
	defer mq.locker.Unlock()

	nextPos := (mq.tail + 1) % int32(len(mq.topicQueue))
	//如果队列满了
	if nextPos == mq.head {
		//将对首的数据删除掉
		mq.head++
		mq.head = mq.head % int32(len(mq.topicQueue))
	}

	mq.tail = nextPos
	mq.topicQueue[mq.tail] = *topicData
	return true
}

func (mq *MemoryQueue) findData(startPos int32, startIndex uint64, limit int32) ([]TopicData, bool) {
	//空队列，无数据
	if mq.head == mq.tail {
		return nil, true
	}

	var findStartPos int32
	var findEndPos int32
	findStartPos = startPos //(mq.head + 1) % cap(mq.topicQueue)
	if findStartPos <= mq.tail {
		findEndPos = mq.tail + 1
	} else {
		findEndPos = int32(len(mq.topicQueue))
	}
	
	if findStartPos >= findEndPos {
		return nil, false
	}

	// 要取的Seq 比内存中最小的数据的Seq还小，那么需要返回错误
	if mq.topicQueue[findStartPos].Seq > startIndex {
		return nil, false
	}

	//二分查找位置
	pos := int32(algorithms.BiSearch(mq.topicQueue[findStartPos:findEndPos], startIndex, 1))
	if pos == -1 {
		return nil, false
	}

	pos += findStartPos
	//取得结束位置
	endPos := limit + pos
	if endPos > findEndPos {
		endPos = findEndPos
	}

	return mq.topicQueue[pos:endPos], true
}

// FindData 返回参数[]TopicData 表示查找到的数据，nil表示无数据。bool表示是否不应该在内存中来查
func (mq *MemoryQueue) FindData(startIndex uint64, limit int32, dataQueue []TopicData) ([]TopicData, bool) {
	mq.locker.RLock()
	defer mq.locker.RUnlock()

	//队列为空时，应该从数据库查找
	if mq.head == mq.tail {
		return nil, false
	} else if mq.head < mq.tail {
		// 队列没有折叠
		datas,ret := mq.findData(mq.head + 1, startIndex, limit)
		if ret {
			dataQueue = append(dataQueue, datas...)
		}
		return dataQueue, ret
	} else {
		// 折叠先找后面的部分
		datas,ret := mq.findData(mq.head+1, startIndex, limit)
		if ret {
			dataQueue = append(dataQueue, datas...)
			return dataQueue, ret
		}

		// 后面没找到，从前面开始找
		datas,ret = mq.findData(0, startIndex, limit)
		dataQueue = append(dataQueue, datas...)
		return dataQueue, ret
	}
}
