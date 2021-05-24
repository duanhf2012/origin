package queue

import "testing"

func TestPriorityQueueSimple(t *testing.T) {
	var queue PriorityQueue
	queue.Init(100)
	var item1,item2,item3,item4 Item
	item1.Value = "xxxx1"
	item1.Priority = 100

	item2.Value = "xxxx2"
	item2.Priority = 99

	item3.Value = "xxxx3"
	item3.Priority = 85

	item4.Value = "xxxx4"
	item4.Priority = 10

	queue.Push(&item1)
	queue.Push(&item2)
	queue.Push(&item3)
	queue.Push(&item4)

	queue.Remove(&item2)

	for{
		item := queue.Pop()
		if item == nil {
			break
		}
		t.Log(item)
	}
}
