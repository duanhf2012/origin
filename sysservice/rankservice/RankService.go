package rankservice

import (
	"fmt"
	"time"

	"github.com/duanhf2012/origin/log"
	"github.com/duanhf2012/origin/rpc"
	"github.com/duanhf2012/origin/service"
)

const PreMapRankSkipLen = 10

type RankService struct {
	service.Service

	mapRankSkip map[uint64]*RankSkip
	rankModule  IRankModule
}

func (rs *RankService) OnInit() error {
	if rs.rankModule != nil {
		_, err := rs.AddModule(rs.rankModule)
		if err != nil {
			return err
		}
	} else {
		rs.AddModule(&DefaultRankModule{})
	}

	rs.mapRankSkip = make(map[uint64]*RankSkip, PreMapRankSkipLen)
	err := rs.dealCfg()
	if err != nil {
		return err
	}

	return nil
}

func (rs *RankService) OnStart() {
	rs.rankModule.OnStart()
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
	for _, addRankListData := range addInfo.AddList {
		if addRankListData.RankId == 0 {
			return fmt.Errorf("RPC_AddRankSkip must has rank id")
		}

		//重复的排行榜信息不允许添加
		rank := rs.mapRankSkip[addRankListData.RankId]
		if rank != nil {
			continue
		}

		newSkip := NewRankSkip(addRankListData.RankId, addRankListData.RankName, addRankListData.IsDec, transformLevel(addRankListData.SkipListLevel), addRankListData.MaxRank, time.Duration(addRankListData.ExpireMs)*time.Millisecond)
		newSkip.SetupRankModule(rs.rankModule)

		rs.mapRankSkip[addRankListData.RankId] = newSkip
		rs.rankModule.OnSetupRank(true, newSkip)
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

	addCount, updateCount := rankSkip.UpsetRankList(upsetInfo.RankDataList)
	upsetResult.AddCount = addCount
	upsetResult.ModifyCount = updateCount

	if upsetInfo.FindNewRank == true {
		for _, rdata := range upsetInfo.RankDataList {
			_, rank := rankSkip.GetRankNodeData(rdata.Key)
			upsetResult.NewRank = append(upsetResult.NewRank, &rpc.RankInfo{Key: rdata.Key, Rank: rank})
		}
	}

	return nil
}

// RPC_IncreaseRankData 增量更新排行扩展数据
func (rs *RankService) RPC_IncreaseRankData(changeRankData *rpc.IncreaseRankData, changeRankDataRet *rpc.IncreaseRankDataRet) error {
	rankSkip, ok := rs.mapRankSkip[changeRankData.RankId]
	if ok == false || rankSkip == nil {
		return fmt.Errorf("RPC_ChangeRankData[", changeRankData.RankId, "] no this rank id")
	}

	ret := rankSkip.ChangeExtendData(changeRankData)
	if ret == false {
		return fmt.Errorf("RPC_ChangeRankData[", changeRankData.RankId, "] no this key ", changeRankData.Key)
	}

	if changeRankData.ReturnRankData == true {
		rankData, rank := rankSkip.GetRankNodeData(changeRankData.Key)
		changeRankDataRet.PosData = &rpc.RankPosData{}
		changeRankDataRet.PosData.Rank = rank

		changeRankDataRet.PosData.Key = rankData.Key
		changeRankDataRet.PosData.Data = rankData.Data
		changeRankDataRet.PosData.SortData = rankData.SortData
		changeRankDataRet.PosData.ExtendData = rankData.ExData
	}

	return nil
}

// RPC_UpsetRank 更新排行榜
func (rs *RankService) RPC_UpdateRankData(updateRankData *rpc.UpdateRankData, updateRankDataRet *rpc.UpdateRankDataRet) error {
	rankSkip, ok := rs.mapRankSkip[updateRankData.RankId]
	if ok == false || rankSkip == nil {
		updateRankDataRet.Ret = false
		return nil
	}

	updateRankDataRet.Ret = rankSkip.UpdateRankData(updateRankData)
	return nil
}

// RPC_DeleteRankDataByKey 按key从排行榜中进行删除
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

// RPC_FindRankDataByKey 按key查找，返回对应的排行名次信息
func (rs *RankService) RPC_FindRankDataByKey(findInfo *rpc.FindRankDataByKey, findResult *rpc.RankPosData) error {
	rankObj, ok := rs.mapRankSkip[findInfo.RankId]
	if ok == false || rankObj == nil {
		return fmt.Errorf("RPC_FindRankDataByKey[", findInfo.RankId, "] no this rank type")
	}

	findRankData, rank := rankObj.GetRankNodeData(findInfo.Key)
	if findRankData != nil {
		findResult.Data = findRankData.Data
		findResult.Key = findRankData.Key
		findResult.SortData = findRankData.SortData
		findResult.Rank = rank
		findResult.ExtendData = findRankData.ExData
	}
	return nil
}

// RPC_FindRankDataByRank 按pos查找
func (rs *RankService) RPC_FindRankDataByRank(findInfo *rpc.FindRankDataByRank, findResult *rpc.RankPosData) error {
	rankObj, ok := rs.mapRankSkip[findInfo.RankId]
	if ok == false || rankObj == nil {
		return fmt.Errorf("RPC_FindRankDataByKey[", findInfo.RankId, "] no this rank type")
	}

	findRankData, rankPos := rankObj.GetRankNodeDataByRank(findInfo.Rank)
	if findRankData != nil {
		findResult.Data = findRankData.Data
		findResult.Key = findRankData.Key
		findResult.SortData = findRankData.SortData
		findResult.Rank = rankPos
		findResult.ExtendData = findRankData.ExData
	}
	return nil
}

// RPC_FindRankDataList 按StartRank查找,从StartRank开始count个排行数据
func (rs *RankService) RPC_FindRankDataList(findInfo *rpc.FindRankDataList, findResult *rpc.RankDataList) error {
	rankObj, ok := rs.mapRankSkip[findInfo.RankId]
	if ok == false || rankObj == nil {
		err := fmt.Errorf("not config rank %d", findInfo.RankId)
		log.SError(err.Error())
		return err
	}

	findResult.RankDataCount = rankObj.GetRankLen()
	err := rankObj.GetRankDataFromToLimit(findInfo.StartRank-1, findInfo.Count, findResult)
	if err != nil {
		return err
	}

	//查询附带的key
	if findInfo.Key != 0 {
		findRankData, rank := rankObj.GetRankNodeData(findInfo.Key)
		if findRankData != nil {
			findResult.KeyRank = &rpc.RankPosData{}
			findResult.KeyRank.Data = findRankData.Data
			findResult.KeyRank.Key = findRankData.Key
			findResult.KeyRank.SortData = findRankData.SortData
			findResult.KeyRank.Rank = rank
			findResult.KeyRank.ExtendData = findRankData.ExData
		}
	}

	return nil
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
		if okId == false || uint64(rankId) == 0 {
			return fmt.Errorf("RankService SortCfg data must has RankID[number]")
		}

		rankName, okId := mapCfg["RankName"].(string)
		if okId == false || len(rankName) == 0 {
			return fmt.Errorf("RankService SortCfg data must has RankName[string]")
		}

		level, _ := mapCfg["SkipListLevel"].(float64)
		isDec, _ := mapCfg["IsDec"].(bool)
		maxRank, _ := mapCfg["MaxRank"].(float64)
		expireMs, _ := mapCfg["ExpireMs"].(float64)

		newSkip := NewRankSkip(uint64(rankId), rankName, isDec, transformLevel(int32(level)), uint64(maxRank), time.Duration(expireMs)*time.Millisecond)
		newSkip.SetupRankModule(rs.rankModule)
		rs.mapRankSkip[uint64(rankId)] = newSkip
		err := rs.rankModule.OnSetupRank(false, newSkip)
		if err != nil {
			return err
		}
	}

	return nil
}
