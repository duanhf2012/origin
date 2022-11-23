package rankservice

import (
	"container/heap"
	"github.com/duanhf2012/origin/util/sync"
	"time"
)

var expireDataPool = sync.NewPoolEx(make(chan sync.IPoolData, 10240), func() sync.IPoolData {
	return &ExpireData{}
})

type ExpireData struct {
	Index int
	Key uint64
	RefreshTimestamp int64
	ref bool
}

type rankDataHeap struct {
	rankDatas []*ExpireData
	expireMs int64
	mapExpireData map[uint64]*ExpireData
}

var expireData ExpireData
func (ed *ExpireData) Reset(){
	*ed = expireData
}

func (ed *ExpireData) IsRef() bool{
	return ed.ref
}

func (ed *ExpireData) Ref(){
	ed.ref = true
}

func (ed *ExpireData) UnRef(){
	ed.ref = false
}

func (rd *rankDataHeap) Init(maxRankDataCount int32,expireMs time.Duration){
	rd.rankDatas = make([]*ExpireData,0,maxRankDataCount)
	rd.expireMs = int64(expireMs)
	rd.mapExpireData = make(map[uint64]*ExpireData,512)
	heap.Init(rd)
}

func (rd *rankDataHeap) Len() int {
	return len(rd.rankDatas)
}

func (rd *rankDataHeap) Less(i, j int) bool {
	return rd.rankDatas[i].RefreshTimestamp < rd.rankDatas[j].RefreshTimestamp
}

func (rd *rankDataHeap) Swap(i, j int) {
	rd.rankDatas[i], rd.rankDatas[j] = rd.rankDatas[j], rd.rankDatas[i]
	rd.rankDatas[i].Index,rd.rankDatas[j].Index = i,j
}

func (rd *rankDataHeap) Push(x interface{}) {
	ed := x.(*ExpireData)
	ed.Index = len(rd.rankDatas)
	rd.rankDatas = append(rd.rankDatas,ed)
}

func (rd *rankDataHeap) Pop() (ret interface{}) {
	l := len(rd.rankDatas)
	var retData *ExpireData
	rd.rankDatas, retData = rd.rankDatas[:l-1], rd.rankDatas[l-1]
	retData.Index = -1
	ret = retData

	return
}

func (rd *rankDataHeap) PopExpireKey() uint64{
	if rd.Len() <= 0 {
		return 0
	}

	if rd.rankDatas[0].RefreshTimestamp+rd.expireMs > time.Now().UnixNano() {
		return 0
	}

	rankData := heap.Pop(rd).(*ExpireData)
	delete(rd.mapExpireData,rankData.Key)

	return  rankData.Key
}

func (rd *rankDataHeap) PushOrRefreshExpireKey(key uint64,refreshTimestamp int64){
	//1.先删掉之前的
	expData ,ok := rd.mapExpireData[key]
	if ok == true {
		expData.RefreshTimestamp = refreshTimestamp
		heap.Fix(rd,expData.Index)
		return
	}

	//2.直接插入
	expData = expireDataPool.Get().(*ExpireData)
	expData.Key = key
	expData.RefreshTimestamp = refreshTimestamp
	rd.mapExpireData[key] = expData

	heap.Push(rd,expData)
}

func (rd *rankDataHeap) RemoveExpireKey(key uint64){
	expData ,ok := rd.mapExpireData[key]
	if ok == false {
		return
	}

	delete(rd.mapExpireData,key)
	heap.Remove(rd,expData.Index)
	expireDataPool.Put(expData)
}




