package rankservice

import "github.com/duanhf2012/origin/service"

type RankDataChangeType int8

const (
	RankDataNone   RankDataChangeType = 0
	RankDataAdd    RankDataChangeType = 1 //数据插入
	RankDataUpdate RankDataChangeType = 2 //数据更新
	RankDataDelete RankDataChangeType = 3 //数据删除
)

type IRankSkip interface {
	GetRankID() uint64
	GetRankLen() uint64
}

// RankDataChangeCallBack 排行数据变化时调用
//type RankDataChangeCallBack interface {
//	CB(iRankService service.IService, rankSkip IRankSkip, changeType RankDataChangeType, changed []*RankData)
//}

type IRankModule interface {
	service.IModule

	OnStart(mapRankSkip map[uint64]*RankSkip) error              //服务开启时回调
	OnEnterRank(rankSkip IRankSkip, enterData []*RankData)       //进入排行
	OnLeaveRank(rankSkip IRankSkip, leaveData []*RankData)       //离开排行
	OnChangeRankData(rankSkip IRankSkip, changeData []*RankData) //当排行数据变化时
	OnStop(mapRankSkip map[uint64]*RankSkip)                     //服务结束时回调
}

type DefaultRankModule struct {
	service.Module
}

func (dr *DefaultRankModule) OnStart(mapRankSkip map[uint64]*RankSkip) error {
	return nil
}

func (dr *DefaultRankModule) OnEnterRank(rankSkip IRankSkip, enterData []*RankData) {
}

func (dr *DefaultRankModule) OnLeaveRank(rankSkip IRankSkip, leaveData []*RankData) {
}

func (dr *DefaultRankModule) OnChangeRankData(rankSkip IRankSkip, changeData []*RankData) {
}

func (dr *DefaultRankModule) OnStop(mapRankSkip map[uint64]*RankSkip) {
}
