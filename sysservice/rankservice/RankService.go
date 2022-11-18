package rankservice

import (
	"fmt"
	"github.com/duanhf2012/origin/log"
	"github.com/duanhf2012/origin/node"
	"github.com/duanhf2012/origin/rpc"
	"github.com/duanhf2012/origin/service"
)

func init() {
	node.Setup(&RankService{})
}

const PreMapRankSkipLen = 10
const ManualAddRankSkip = "RPC_ManualAddRankSkip"
const UpsetRank = "RPC_UpsetRank"
const DeleteRankDataByKey = "RPC_DeleteRankDataByKey"
const FindRankDataByKey = "RPC_FindRankDataByKey"
const FindRankDataByPos = "RPC_FindRankDataByPos"
const FindRankDataListStartTo = "RPC_FindRankDataListStartTo"

type RankService struct {
	service.Service

	mapRankSkip map[uint64]*RankSkip
	rankModule  IRankModule
}

func (rs *RankService) OnInit() error {
	rs.mapRankSkip = make(map[uint64]*RankSkip, PreMapRankSkipLen)
	err := rs.dealCfg()
	if err != nil {
		return err
	}

	if rs.rankModule != nil {
		_, err = rs.AddModule(rs.rankModule)
		if err != nil {
			return err
		}
	} else {
		rs.AddModule(&DefaultRankModule{})
	}

	return nil
}

func (rs *RankService) OnStart() {
	rs.rankModule.OnStart(rs.mapRankSkip)
}

func (rs *RankService) OnRelease() {
	rs.rankModule.OnStop(rs.mapRankSkip)
}

// 安装排行模块
func (rs *RankService) SetupRankModule(rankModule IRankModule) {
	rs.rankModule = rankModule
}

// RPC_ManualAddRankSkip 提供手动添加排行榜
func (rs *RankService) RPC_ManualAddRankSkip(addInfo *rpc.AddRankList, addResult *rpc.RankResult) error {
	addList := make([]uint64, 0, PreMapRankSkipLen)
	for _, addRankListData := range addInfo.AddList {
		if addRankListData.RankId == 0 {
			rs.deleteRankList(addList)
			return fmt.Errorf("RPC_AddRankSkip must has rank id")
		}

		newSkip := NewRankSkip(addRankListData.IsDec, transformLevel(addRankListData.SkipListLevel), addRankListData.MaxRank)
		rs.mapRankSkip[addRankListData.RankId] = newSkip
		addList = append(addList, addRankListData.RankId)
	}

	addResult.AddCount = 1

	return nil
}

// RPC_UpsetRank 更新排行榜
func (rs *RankService) RPC_UpsetRank(upsetInfo *rpc.UpsetRankData, upsetResult *rpc.RankResult) error {
	rankSkip, ok := rs.mapRankSkip[upsetInfo.RankId]
	if ok == false || rankSkip == nil {
		return fmt.Errorf("RPC_UpsetRank[", upsetInfo.RankId, "] no this rank id")
	}

	addCount, updateCount := rankSkip.UpsetRank(upsetInfo.RankDataList)
	upsetResult.AddCount = addCount
	upsetResult.ModifyCount = updateCount
	return nil
}

// RPC_DeleteRankDataByKey 按排行的key进行删除
func (rs *RankService) RPC_DeleteRankDataByKey(delInfo *rpc.DeleteByKey, delResult *rpc.RankResult) error {
	rankSkip, ok := rs.mapRankSkip[delInfo.RankId]
	if ok == false || rankSkip == nil {
		return fmt.Errorf("RPC_DeleteRankDataByKey[", delInfo.RankId, "] no this rank type")
	}

	removeCount := rankSkip.DeleteRankData(delInfo.KeyList)
	if removeCount == 0 {
		log.SError("remove count is zero")
	}

	delResult.RemoveCount = removeCount
	return nil
}

// RPC_FindRankDataByKey 按key查找
func (rs *RankService) RPC_FindRankDataByKey(findInfo *rpc.FindRankDataByKey, findResult *rpc.RankPosData) error {
	rankObj, ok := rs.mapRankSkip[findInfo.RankId]
	if ok == false || rankObj == nil {
		return fmt.Errorf("RPC_FindRankDataByKey[", findInfo.RankId, "] no this rank type")
	}

	findRankData, rankPos := rankObj.GetRankNodeData(findInfo.Key)
	if findRankData != nil {
		findResult.Data = findRankData.Data
		findResult.Key = findRankData.Key
		findResult.SortData = findRankData.SortData
		findResult.RankPos = rankPos
	}
	return nil
}

// RPC_FindRankDataByPos 按pos查找
func (rs *RankService) RPC_FindRankDataByPos(findInfo *rpc.FindRankDataByPos, findResult *rpc.RankPosData) error {
	rankObj, ok := rs.mapRankSkip[findInfo.RankId]
	if ok == false || rankObj == nil {
		return fmt.Errorf("RPC_FindRankDataByKey[", findInfo.RankId, "] no this rank type")
	}

	findRankData, rankPos := rankObj.GetRankNodeDataByPos(findInfo.Pos)
	if findRankData != nil {
		findResult.Data = findRankData.Data
		findResult.Key = findRankData.Key
		findResult.SortData = findRankData.SortData
		findResult.RankPos = rankPos
	}
	return nil
}

// RPC_FindRankDataListStartTo 按pos查找,start开始count个排行数据
func (rs *RankService) RPC_FindRankDataListStartTo(findInfo *rpc.FindRankDataListStartTo, findResult *rpc.RankDataList) error {
	rankObj, ok := rs.mapRankSkip[findInfo.RankId]
	if ok == false || rankObj == nil {
		return fmt.Errorf("RPC_FindRankDataListStartTo[", findInfo.RankId, "] no this rank type")
	}

	findResult.RankDataCount = rankObj.GetRankLen()
	return rankObj.GetRankDataFromToLimit(findInfo.StartPos, findInfo.Count, findResult)
}

func (rs *RankService) deleteRankList(delIdList []uint64) {
	if rs.mapRankSkip == nil {
		return
	}

	for _, id := range delIdList {
		delete(rs.mapRankSkip, id)
	}
}

func (rs *RankService) dealCfg() error {
	mapDBServiceCfg, ok := rs.GetServiceCfg().(map[string]interface{})
	if ok == false {
		return nil
	}

	cfgList, okList := mapDBServiceCfg["SortCfg"].([]interface{})
	if okList == false {
		return fmt.Errorf("RankService SortCfg must be list")
	}

	for _, cfg := range cfgList {
		mapCfg, okCfg := cfg.(map[string]interface{})
		if okCfg == false {
			return fmt.Errorf("RankService SortCfg data must be map or struct")
		}

		rankId, okId := mapCfg["RankID"].(float64)
		if okId == false {
			return fmt.Errorf("RankService SortCfg data must has RankID[number]")
		}

		level, _ := mapCfg["SkipListLevel"].(float64)
		isDec, _ := mapCfg["IsDec"].(bool)
		maxRank, _ := mapCfg["MaxRank"].(float64)

		newSkip := NewRankSkip(isDec, transformLevel(int32(level)), uint64(maxRank))
		rs.mapRankSkip[uint64(rankId)] = newSkip
	}
	return nil
}
