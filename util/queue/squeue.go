package queue

import (
	"sync"
)

/*
 这是一个循环队列
*/
type SQueue[ElementType any] struct {
	elements []ElementType
	head int
	tail int
	locker sync.RWMutex
}

//游标,通过该游标获取数据
type SCursor[ElementType any] struct {
	pos int
	squeue *SQueue[ElementType]
}

func NewSQueue[ElementType any](maxElementNum int) *SQueue[ElementType]{
	queue := &SQueue[ElementType]{}
	queue.elements = make([]ElementType,maxElementNum+1)

	return queue
}

//游标移动到队首
func (s *SCursor[ElementType]) First(){
	s.squeue.locker.RLock()
	defer s.squeue.locker.RUnlock()
	s.pos = s.squeue.head
}

//从当前位置移动游标,注意如果在多协程读或者pop时，可能会导致游标失效
func (s *SCursor[ElementType]) Next() (elem ElementType,ret bool){
	s.squeue.locker.RLock()
	defer s.squeue.locker.RUnlock()

	if s.pos == s.squeue.tail {
		return
	}

	s.pos++
	s.pos =  (s.pos)%(len(s.squeue.elements))
	return s.squeue.elements[s.pos],true
}

//获取队列元数个数
func (s *SQueue[ElementType]) Len() int {
	s.locker.RLock()
	defer s.locker.RUnlock()

	return s.len()
}

func (s *SQueue[ElementType]) len() int {
	if s.head <= s.tail {
		return s.tail - s.head
	}

	//(len(s.elements)-1-s.head)+(s.tail+1)
	return len(s.elements)-s.head+s.tail
}

//获取游标，默认是队首
func (s *SQueue[ElementType]) GetCursor() (cur SCursor[ElementType]){
	s.locker.RLock()
	defer s.locker.RUnlock()

	cur.squeue = s
	cur.pos = s.head
	return
}

//获取指定位置的游标
func (s *SQueue[ElementType]) GetPosCursor(pos int) (cur SCursor[ElementType],ret bool){
	s.locker.RLock()
	defer s.locker.RUnlock()

	if s.head < s.tail {
		if pos<=s.head || pos>s.tail{
			return
		}

		ret = true
		cur.squeue = s
		cur.pos = pos
		return
	}

	if pos >s.tail && pos <=s.head {
		return
	}

	cur.squeue = s
	cur.pos = pos
	return
}

//从队首移除掉指定数量元素
func (s *SQueue[ElementType]) RemoveElement(elementNum int) (removeNum int) {
	s.locker.Lock()
	defer s.locker.Unlock()

	lens :=  s.len()
	if elementNum > lens{
		removeNum = lens
	}else{
		removeNum = elementNum
	}


	s.head = (s.head + removeNum)%len(s.elements)

	return
}

//从队首Pop元素
func (s *SQueue[ElementType]) Pop() (elem ElementType,ret bool){
	s.locker.Lock()
	defer s.locker.Unlock()

	if s.head == s.tail {
		return
	}

	s.head++
	s.head = s.head%len(s.elements)
	return s.elements[s.head],true
}

//从队尾Push数据
func (s *SQueue[ElementType]) Push(elem ElementType) bool {
	s.locker.Lock()
	defer s.locker.Unlock()

	nextPos := (s.tail+1) % len(s.elements)
	if nextPos == s.head {
		//is full
		return false
	}

	s.tail = nextPos
	s.elements[s.tail] = elem
	return true
}

//判断队列是否为空
func (s *SQueue[ElementType]) IsEmpty() bool{
	s.locker.RLock()
	defer s.locker.RUnlock()

	return s.head == s.tail
}

//判断队列是否已满 
func (s *SQueue[ElementType]) IsFull() bool{
	s.locker.RLock()
	defer s.locker.RUnlock()

	nextPos := (s.tail+1) % len(s.elements)
	return nextPos == s.head
}

