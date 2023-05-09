package rankservice

import (
	"fmt"
	"time"

	"github.com/duanhf2012/origin/rpc"
	"github.com/duanhf2012/origin/util/algorithms/skip"
)

type RankSkip struct {
	rankId         uint64               //排行榜ID
	rankName       string               //排行榜名称
	isDes          bool                 //是否为降序 true：降序 false：升序
	skipList       *skip.SkipList       //跳表
	mapRankData    map[uint64]*RankData //排行数据map
	maxLen         uint64               //排行数据长度
	expireMs       time.Duration        //有效时间
	rankModule     IRankModule
	rankDataExpire rankDataHeap
}

const MaxPickExpireNum = 128
const (
	RankDataNone   RankDataChangeType = 0
	RankDataAdd    RankDataChangeType = 1 //数据插入
	RankDataUpdate RankDataChangeType = 2 //数据更新
	RankDataDelete RankDataChangeType = 3 //数据删除
)

// NewRankSkip 创建排行榜
func NewRankSkip(rankId uint64, rankName string, isDes bool, level interface{}, maxLen uint64, expireMs time.Duration) *RankSkip {
	rs := &RankSkip{}

	rs.rankId = rankId
	rs.rankName = rankName
	rs.isDes = isDes
	rs.skipList = skip.New(level)
	rs.mapRankData = make(map[uint64]*RankData, 10240)
	rs.maxLen = maxLen
	rs.expireMs = expireMs
	rs.rankDataExpire.Init(int32(maxLen), expireMs)

	return rs
}

func (rs *RankSkip) pickExpireKey() {
	if rs.expireMs == 0 {
		return
	}

	for i := 1; i <= MaxPickExpireNum; i++ {
		key := rs.rankDataExpire.PopExpireKey()
		if key == 0 {
			return
		}

		rs.DeleteRankData([]uint64{key})
	}
}

func (rs *RankSkip) SetupRankModule(rankModule IRankModule) {
	rs.rankModule = rankModule
}

// GetRankID 获取排行榜Id
func (rs *RankSkip) GetRankID() uint64 {
	return rs.rankId
}

// GetRankName 获取排行榜名称
func (rs *RankSkip) GetRankName() string {
	return rs.rankName
}

// GetRankLen 获取排行榜长度
func (rs *RankSkip) GetRankLen() uint64 {
	return rs.skipList.Len()
}

func (rs *RankSkip) UpsetRankList(upsetRankData []*rpc.RankData) (addCount int32, modifyCount int32) {
	for _, upsetData := range upsetRankData {
		changeType := rs.UpsetRank(upsetData, time.Now().UnixNano(), false)
		if changeType == RankDataAdd {
			addCount += 1
		} else if changeType == RankDataUpdate {
			modifyCount += 1
		}
	}

	rs.pickExpireKey()
	return
}

func (rs *RankSkip) InsertDataOnNonExistent(changeRankData *rpc.IncreaseRankData) bool {
	if changeRankData.InsertDataOnNonExistent == false {
		return false
	}

	var upsetData rpc.RankData
	upsetData.Key = changeRankData.Key
	upsetData.Data = changeRankData.InitData
	upsetData.SortData = changeRankData.InitSortData

	for i := 0; i < len(changeRankData.IncreaseSortData) && i < len(upsetData.SortData); i++ {
		upsetData.SortData[i] += changeRankData.IncreaseSortData[i]
	}

	for _, val := range changeRankData.Extend {
		upsetData.ExData = append(upsetData.ExData, &rpc.ExtendIncData{InitValue: val.InitValue, IncreaseValue: val.IncreaseValue})
	}

	//强制设计指定值
	for _, setData := range changeRankData.SetSortAndExtendData {
		if setData.IsSortData == true {
			if int(setData.Pos) >= len(upsetData.SortData) {
				return false
			}
			upsetData.SortData[setData.Pos] = setData.Data
		} else {
			if int(setData.Pos) < len(upsetData.ExData) {
				upsetData.ExData[setData.Pos].IncreaseValue = 0
				upsetData.ExData[setData.Pos].InitValue = setData.Data
			}
		}
	}

	refreshTimestamp := time.Now().UnixNano()
	newRankData := NewRankData(rs.isDes, &upsetData, refreshTimestamp)
	rs.skipList.Insert(newRankData)
	rs.mapRankData[upsetData.Key] = newRankData

	//刷新有效期和存档数据
	rs.rankDataExpire.PushOrRefreshExpireKey(upsetData.Key, refreshTimestamp)
	rs.rankModule.OnChangeRankData(rs, newRankData)

	return true
}

func (rs *RankSkip) UpdateRankData(updateRankData *rpc.UpdateRankData) bool {
	rankNode, ok := rs.mapRankData[updateRankData.Key]
	if ok == false {
		return false
	}

	rankNode.Data = updateRankData.Data
	rs.rankDataExpire.PushOrRefreshExpireKey(updateRankData.Key, time.Now().UnixNano())
	rs.rankModule.OnChangeRankData(rs, rankNode)
	return true
}

func (rs *RankSkip) ChangeExtendData(changeRankData *rpc.IncreaseRankData) bool {
	rankNode, ok := rs.mapRankData[changeRankData.Key]
	if ok == false {
		return rs.InsertDataOnNonExistent(changeRankData)
	}

	//先判断是不是有修改
	bChange := false
	for i := 0; i < len(changeRankData.IncreaseSortData) && i < len(rankNode.SortData); i++ {
		if changeRankData.IncreaseSortData[i] != 0 {
			bChange = true
		}
	}

	if bChange == false {
		for _, setSortAndExtendData := range changeRankData.SetSortAndExtendData {
			if setSortAndExtendData.IsSortData == true {
				bChange = true
			}
		}
	}

	//如果有改变，删除原有的数据,重新刷新到跳表
	rankData := rankNode
	refreshTimestamp := time.Now().UnixNano()
	if bChange == true {
		//copy数据
		var upsetData rpc.RankData
		upsetData.Key = rankNode.Key
		upsetData.Data = rankNode.Data
		upsetData.SortData = append(upsetData.SortData, rankNode.SortData...)

		for i := 0; i < len(changeRankData.IncreaseSortData) && i < len(upsetData.SortData); i++ {
			if changeRankData.IncreaseSortData[i] != 0 {
				upsetData.SortData[i] += changeRankData.IncreaseSortData[i]
			}
		}

		for _, setData := range changeRankData.SetSortAndExtendData {
			if setData.IsSortData == true {
				if int(setData.Pos) < len(upsetData.SortData) {
					upsetData.SortData[setData.Pos] = setData.Data
				}
			}
		}

		rankData = NewRankData(rs.isDes, &upsetData, refreshTimestamp)
		rankData.ExData = append(rankData.ExData, rankNode.ExData...)

		//从排行榜中删除
		rs.skipList.Delete(rankNode)
		ReleaseRankData(rankNode)

		rs.skipList.Insert(rankData)
		rs.mapRankData[upsetData.Key] = rankData
	}

	//增长扩展参数
	for i := 0; i < len(changeRankData.Extend); i++ {
		if i < len(rankData.ExData) {
			//直接增长
			rankData.ExData[i] += changeRankData.Extend[i].IncreaseValue
		} else {
			//如果不存在的扩展位置,append补充，并按IncreaseValue增长
			rankData.ExData = append(rankData.ExData, changeRankData.Extend[i].InitValue+changeRankData.Extend[i].IncreaseValue)
		}
	}

	//设置固定值
	for _, setData := range changeRankData.SetSortAndExtendData {
		if setData.IsSortData == false {
			if int(setData.Pos) < len(rankData.ExData) {
				rankData.ExData[setData.Pos] = setData.Data
			}
		}
	}

	rs.rankDataExpire.PushOrRefreshExpireKey(rankData.Key, refreshTimestamp)
	rs.rankModule.OnChangeRankData(rs, rankData)

	return true
}

// UpsetRank 更新玩家排行数据,返回变化后的数据及变化类型
func (rs *RankSkip) UpsetRank(upsetData *rpc.RankData, refreshTimestamp int64, fromLoad bool) RankDataChangeType {
	rankNode, ok := rs.mapRankData[upsetData.Key]
	if ok == true {
		//增长扩展数据
		for i := 0; i < len(upsetData.ExData); i++ {
			if i < len(rankNode.ExData) {
				//直接增长
				rankNode.ExData[i] += upsetData.ExData[i].IncreaseValue
			} else {
				//如果不存在的扩展位置,append补充，并按IncreaseValue增长
				rankNode.ExData = append(rankNode.ExData, upsetData.ExData[i].InitValue+upsetData.ExData[i].IncreaseValue)
			}
		}

		//找到的情况对比排名数据是否有变化,无变化进行data更新,有变化则进行删除更新
		if compareIsEqual(rankNode.SortData, upsetData.SortData) {
			rankNode.Data = upsetData.GetData()
			rankNode.refreshTimestamp = refreshTimestamp

			if fromLoad == false {
				rs.rankModule.OnChangeRankData(rs, rankNode)
			}
			rs.rankDataExpire.PushOrRefreshExpireKey(upsetData.Key, refreshTimestamp)
			return RankDataUpdate
		}

		if upsetData.Data == nil {
			upsetData.Data = rankNode.Data
		}

		//设置额外数据
		for idx, exValue := range rankNode.ExData {
			currentIncreaseValue := int64(0)
			if idx < len(upsetData.ExData) {
				currentIncreaseValue = upsetData.ExData[idx].IncreaseValue
			}

			upsetData.ExData = append(upsetData.ExData, &rpc.ExtendIncData{
				InitValue:     exValue,
				IncreaseValue: currentIncreaseValue,
			})
		}

		rs.skipList.Delete(rankNode)
		ReleaseRankData(rankNode)

		newRankData := NewRankData(rs.isDes, upsetData, refreshTimestamp)
		rs.skipList.Insert(newRankData)
		rs.mapRankData[upsetData.Key] = newRankData

		//刷新有效期
		rs.rankDataExpire.PushOrRefreshExpireKey(upsetData.Key, refreshTimestamp)

		if fromLoad == false {
			rs.rankModule.OnChangeRankData(rs, newRankData)
		}
		return RankDataUpdate
	}

	if rs.checkInsertAndReplace(upsetData) {
		newRankData := NewRankData(rs.isDes, upsetData, refreshTimestamp)

		rs.skipList.Insert(newRankData)
		rs.mapRankData[upsetData.Key] = newRankData
		rs.rankDataExpire.PushOrRefreshExpireKey(upsetData.Key, refreshTimestamp)

		if fromLoad == false {
			rs.rankModule.OnEnterRank(rs, newRankData)
		}

		return RankDataAdd
	}

	return RankDataNone
}

// DeleteRankData 删除排行数据
func (rs *RankSkip) DeleteRankData(delKeys []uint64) int32 {
	var removeRankData int32
	//预统计处理，进行回调
	for _, key := range delKeys {
		rankData, ok := rs.mapRankData[key]
		if ok == false {
			continue
		}

		removeRankData += 1
		rs.skipList.Delete(rankData)
		delete(rs.mapRankData, rankData.Key)
		rs.rankDataExpire.RemoveExpireKey(rankData.Key)
		rs.rankModule.OnLeaveRank(rs, rankData)
		ReleaseRankData(rankData)
	}

	return removeRankData
}

// GetRankNodeData 获取,返回排名节点与名次
func (rs *RankSkip) GetRankNodeData(findKey uint64) (*RankData, uint64) {
	rankNode, ok := rs.mapRankData[findKey]
	if ok == false {
		return nil, 0
	}

	rs.pickExpireKey()
	_, index := rs.skipList.GetWithPosition(rankNode)
	return rankNode, index + 1
}

// GetRankNodeDataByPos 获取,返回排名节点与名次
func (rs *RankSkip) GetRankNodeDataByRank(rank uint64) (*RankData, uint64) {
	rs.pickExpireKey()
	rankNode := rs.skipList.ByPosition(rank - 1)
	if rankNode == nil {
		return nil, 0
	}

	return rankNode.(*RankData), rank
}

// GetRankKeyPrevToLimit 获取key前count名的数据
func (rs *RankSkip) GetRankKeyPrevToLimit(findKey, count uint64, result *rpc.RankDataList) error {
	if rs.GetRankLen() <= 0 {
		return fmt.Errorf("rank[%d] no data", rs.rankId)
	}

	findData, ok := rs.mapRankData[findKey]
	if ok == false {
		return fmt.Errorf("rank[%d] no data", rs.rankId)
	}

	_, rankPos := rs.skipList.GetWithPosition(findData)
	iter := rs.skipList.Iter(findData)
	iterCount := uint64(0)
	for iter.Prev() && iterCount < count {
		rankData := iter.Value().(*RankData)
		result.RankPosDataList = append(result.RankPosDataList, &rpc.RankPosData{
			Key:        rankData.Key,
			Rank:       rankPos - iterCount + 1,
			SortData:   rankData.SortData,
			Data:       rankData.Data,
			ExtendData: rankData.ExData,
		})
		iterCount++
	}

	return nil
}

// GetRankKeyPrevToLimit 获取key前count名的数据
func (rs *RankSkip) GetRankKeyNextToLimit(findKey, count uint64, result *rpc.RankDataList) error {
	if rs.GetRankLen() <= 0 {
		return fmt.Errorf("rank[%d] no data", rs.rankId)
	}

	findData, ok := rs.mapRankData[findKey]
	if ok == false {
		return fmt.Errorf("rank[%d] no data", rs.rankId)
	}

	_, rankPos := rs.skipList.GetWithPosition(findData)
	iter := rs.skipList.Iter(findData)
	iterCount := uint64(0)
	for iter.Next() && iterCount < count {
		rankData := iter.Value().(*RankData)
		result.RankPosDataList = append(result.RankPosDataList, &rpc.RankPosData{
			Key:        rankData.Key,
			Rank:       rankPos + iterCount + 1,
			SortData:   rankData.SortData,
			Data:       rankData.Data,
			ExtendData: rankData.ExData,
		})
		iterCount++
	}

	return nil
}

// GetRankList 获取排行榜数据,startPos开始的count个数据
func (rs *RankSkip) GetRankDataFromToLimit(startPos, count uint64, result *rpc.RankDataList) error {
	if rs.GetRankLen() <= 0 {
		//初始排行榜可能没有数据
		return nil
	}

	rs.pickExpireKey()
	if result.RankDataCount < startPos {
		startPos = result.RankDataCount - 1
	}

	iter := rs.skipList.IterAtPosition(startPos)
	iterCount := uint64(0)
	for iter.Next() && iterCount < count {
		rankData := iter.Value().(*RankData)
		result.RankPosDataList = append(result.RankPosDataList, &rpc.RankPosData{
			Key:        rankData.Key,
			Rank:       iterCount + startPos + 1,
			SortData:   rankData.SortData,
			Data:       rankData.Data,
			ExtendData: rankData.ExData,
		})
		iterCount++
	}

	return nil
}

// checkCanInsert 检查是否能插入
func (rs *RankSkip) checkInsertAndReplace(upsetData *rpc.RankData) bool {
	//maxLen为0，不限制长度
	if rs.maxLen == 0 {
		return true
	}

	//没有放满,则进行插入
	rankLen := rs.skipList.Len()
	if rs.maxLen > rankLen {
		return true
	}

	//已经放满了,进行数据比较
	lastPosData := rs.skipList.ByPosition(rankLen - 1)
	lastRankData := lastPosData.(*RankData)
	moreThanFlag := compareMoreThan(upsetData.SortData, lastRankData.SortData)
	//降序排列,比最后一位小,不能插入                 升序排列,比最后一位大,不能插入
	if (rs.isDes == true && moreThanFlag < 0) || (rs.isDes == false && moreThanFlag > 0) || moreThanFlag == 0 {
		return false
	}

	//移除最后一位
	//回调模块，该RandData从排行中删除
	rs.rankDataExpire.RemoveExpireKey(lastRankData.Key)
	rs.rankModule.OnLeaveRank(rs, lastRankData)
	rs.skipList.Delete(lastPosData)
	delete(rs.mapRankData, lastRankData.Key)
	ReleaseRankData(lastRankData)
	return true
}
