package rankservice

import (
	"github.com/duanhf2012/origin/rpc"
	"github.com/duanhf2012/origin/util/algorithms/skip"
	"github.com/duanhf2012/origin/util/sync"
)

var emptyRankData RankData

var RankDataPool = sync.NewPoolEx(make(chan sync.IPoolData, 10240), func() sync.IPoolData {
	var newRankData RankData
	return &newRankData
})

type RankData struct {
	Key                  uint64
	SortData             []int64
	Data                 []byte
	ExData               []int64

	refreshTimestamp int64 //刷新时间
	//bRelease bool
	ref         bool
	compareFunc func(other skip.Comparator) int
}

func NewRankData(isDec bool, data *rpc.RankData,refreshTimestamp int64) *RankData {
	ret := RankDataPool.Get().(*RankData)
	ret.compareFunc = ret.ascCompare
	if isDec {
		ret.compareFunc = ret.desCompare
	}
	ret.Key = data.Key
	ret.SortData = data.SortData
	ret.Data = data.Data

	for _,d := range data.ExData{
		ret.ExData = append(ret.ExData,d.InitValue+d.IncreaseValue)
	}

	ret.refreshTimestamp = refreshTimestamp

	return ret
}

func ReleaseRankData(rankData *RankData) {
	RankDataPool.Put(rankData)
}

func (p *RankData) Reset() {
	*p = emptyRankData
}

func (p *RankData) IsRef() bool {
	return p.ref
}

func (p *RankData) Ref() {
	p.ref = true
}

func (p *RankData) UnRef() {
	p.ref = false
}

func (p *RankData) Compare(other skip.Comparator) int {
	return p.compareFunc(other)
}

func (p *RankData) GetKey() uint64 {
	return p.Key
}

func (p *RankData) ascCompare(other skip.Comparator) int {
	otherRankData := other.(*RankData)

	if otherRankData.Key == p.Key {
		return 0
	}

	retFlg := compareMoreThan(p.SortData, otherRankData.SortData)
	if retFlg == 0 {
		if p.Key > otherRankData.Key {
			retFlg = 1
		} else {
			retFlg = -1
		}
	}
	return retFlg
}

func (p *RankData) desCompare(other skip.Comparator) int {
	otherRankData := other.(*RankData)

	if otherRankData.Key == p.Key {
		return 0
	}

	retFlg := compareMoreThan(otherRankData.SortData, p.SortData)
	if retFlg == 0 {
		if p.Key > otherRankData.Key {
			retFlg = -1
		} else {
			retFlg = 1
		}
	}
	return retFlg
}
