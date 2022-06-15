package algorithms

import (
	"testing"
	"fmt"
)

type MyElement struct {
	Score int
}

func (s MyElement) GetValue() int {
	return s.Score
}

func Test_BiSearch(t *testing.T){
	var schedulePoolCfgList []MyElement = []MyElement{MyElement{10}, MyElement{12}, MyElement{14}, MyElement{16}} //
	index := BiSearch[int, MyElement](schedulePoolCfgList, 9, true)
	index = BiSearch[int, MyElement](schedulePoolCfgList, 10, true)
	index = BiSearch[int, MyElement](schedulePoolCfgList, 11, true)
	index = BiSearch[int, MyElement](schedulePoolCfgList, 12, true)
	index = BiSearch[int, MyElement](schedulePoolCfgList, 13, true)
	index = BiSearch[int, MyElement](schedulePoolCfgList, 14, true)
	index = BiSearch[int, MyElement](schedulePoolCfgList, 15, true)
	index = BiSearch[int, MyElement](schedulePoolCfgList, 16, true)
	index = BiSearch[int, MyElement](schedulePoolCfgList, 17, true)
	fmt.Println(index)
}