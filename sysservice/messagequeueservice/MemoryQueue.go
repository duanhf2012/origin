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
		findEndPos = int32(cap(mq.topicQueue))
	}

	//二分查找位置
	pos := int32(algorithms.BiSearch(mq.topicQueue[findStartPos:findEndPos], startIndex, 1))
	if pos == -1 {
		return nil, true
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
func (mq *MemoryQueue) FindData(startIndex uint64, limit int32) ([]TopicData, bool) {
	mq.locker.RLock()
	defer mq.locker.RUnlock()

	//队列为空时，应该从数据库查找
	if mq.head == mq.tail {
		return nil, false
	}

	/*
		//先判断startIndex是否比第一个元素要大
		headTopic := (mq.head + 1) % int32(len(mq.topicQueue))
		//此时需要从持久化数据中取
		if  startIndex+1 > mq.topicQueue[headTopic].Seq {
			return nil, false
		}
	*/

	retData, ret := mq.findData(mq.head+1, startIndex, limit)
	if mq.head <= mq.tail || ret == true {
		return retData, true
	}

	//如果是正常head在后，尾在前，从数组0下标开始找到tail
	return mq.findData(0, startIndex, limit)
}
