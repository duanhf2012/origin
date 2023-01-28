package messagequeueservice

import (
	"fmt"
	"testing"
)

type In int

func (i In) GetValue() int {
	return int(i)
}

func Test_BiSearch(t *testing.T) {
	var memQueue MemoryQueue
	memQueue.Init(5)

	for i := 1; i <= 8; i++ {
		memQueue.Push(&TopicData{Seq: uint64(i)})
	}

	startindex := uint64(0)
	for {
		retData, ret := memQueue.FindData(startindex+1, 10)
		fmt.Println(retData, ret)
		for _, d := range retData {
			if d.Seq > startindex {
				startindex = d.Seq
			}
		}
		if ret == false {
			break
		}
	}

}
