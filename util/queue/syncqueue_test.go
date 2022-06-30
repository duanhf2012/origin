package queue

import (
	"testing"
)

func Test_Example(t *testing.T) {
	//1.创建阶列
	queue := NewSQueue[int](5)

	//2.判断是否为空
	t.Log("is empty :", queue.IsEmpty())
	t.Log("is full :", queue.IsFull())

	//3.游标使用
	cursor := queue.GetCursor()
	cursor.First()
	for {
		elem, ret := cursor.Next()
		if ret == false {
			break
		}
		t.Log("elem:", elem)
	}

	//4.push数据
	for i := 0; i < 6; i++ {
		t.Log("push:", queue.Push(i))
	}

	t.Log("is empty :", queue.IsEmpty())
	t.Log("is full :", queue.IsFull())

	//5.游标遍历
	cursor.First()
	for {
		elem, ret := cursor.Next()
		if ret == false {
			break
		}
		t.Log("elem:", elem)
	}

	//pop数据所有
	for i := 0; i < 6; i++ {
		elem, ret := queue.Pop()
		t.Log("pop:", elem, "-", ret, " len:", queue.Len())
	}

	t.Log("is empty :", queue.IsEmpty())
	t.Log("is full :", queue.IsFull())
}
