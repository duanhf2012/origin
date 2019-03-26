package queue

import "sync"

type SyncQueue struct {
	que   *Queue
	mutex sync.RWMutex
}

func (q *SyncQueue) Len() int {
	q.mutex.RLock()
	defer q.mutex.RUnlock()

	return q.que.Length()
}

func (q *SyncQueue) Add(elem interface{}) {
	q.mutex.Lock()
	defer q.mutex.Unlock()

	q.que.Add(elem)
}

func (q *SyncQueue) Peek() interface{} {
	q.mutex.RLock()
	defer q.mutex.RUnlock()

	return q.que.Peek()
}

func (q *SyncQueue) Get(i int) interface{} {
	q.mutex.RLock()
	defer q.mutex.RUnlock()

	return q.que.Get(i)
}

func (q *SyncQueue) Pop() interface{} {

	q.mutex.Lock()
	defer q.mutex.Unlock()

	return q.que.Pop()
}

func (q *SyncQueue) RLockRange(f func(interface{})) {
	q.mutex.RLock()
	defer q.mutex.RUnlock()

	for i := 0; i < q.que.Length(); i++ {
		f(q.Get(i))
	}
}

func NewSyncQueue() *SyncQueue {
	syncQueue := SyncQueue{}
	syncQueue.que = NewQueue()

	return &syncQueue
}
