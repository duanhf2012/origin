package queue

import "container/heap"

// An Item is something we manage in a Priority queue.
type Item struct {
	Value    interface{} // The Value of the item; arbitrary.
	Priority int         // The Priority of the item in the queue.
	// The Index is needed by update and is maintained by the heap.Interface methods.
	Index int // The Index of the item in the heap.
}

// A PriorityQueueSlice implements heap.Interface and holds Items.
type PriorityQueueSlice []*Item

func (pq PriorityQueueSlice) Len() int { return len(pq) }

func (pq PriorityQueueSlice) Less(i, j int) bool {
	// We want Pop to give us the highest, not lowest, Priority so we use greater than here.
	return pq[i].Priority > pq[j].Priority
}

func (pq PriorityQueueSlice) Swap(i, j int) {
	pq[i], pq[j] = pq[j], pq[i]
	pq[i].Index = i
	pq[j].Index = j
}

func (pq *PriorityQueueSlice) Push(x interface{}) {
	n := len(*pq)
	item := x.(*Item)
	item.Index = n
	*pq = append(*pq, item)
}

func (pq *PriorityQueueSlice) Pop() interface{} {
	old := *pq
	n := len(old)
	item := old[n-1]
	item.Index = -1 // for safety
	*pq = old[0 : n-1]
	return item
}

// update modifies the Priority and Value of an Item in the queue.
func (pq *PriorityQueueSlice) Update(item *Item, value interface{}, priority int) {
	item.Value = value
	item.Priority = priority
	heap.Fix(pq, item.Index)
}

type PriorityQueue struct {
	priorityQueueSlice PriorityQueueSlice
}

//参数是数据大小
func (pq *PriorityQueue) Init(initItemSliceNum int){
	pq.priorityQueueSlice = make(PriorityQueueSlice,0,initItemSliceNum)
}

func (pq *PriorityQueue) Push(item *Item){
	heap.Push(&pq.priorityQueueSlice, item)
}

func (pq *PriorityQueue) Pop() *Item {
	if pq.Len() ==0 {
		return nil
	}
	return heap.Pop(&pq.priorityQueueSlice).(*Item)
}

func (pq *PriorityQueue) Len() int {
	return len(pq.priorityQueueSlice)
}

func (pq *PriorityQueue) Update(item *Item, value interface{}, priority int){
	pq.priorityQueueSlice.Update(item, value, priority)
}

func (pq *PriorityQueue) Remove(item *Item){
	heap.Remove(&pq.priorityQueueSlice,item.Index)
}
