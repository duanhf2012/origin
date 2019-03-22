package synccacheservice

import (
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/duanhf2012/origin/cluster"
	"github.com/duanhf2012/origin/service"
	"github.com/duanhf2012/origin/util"
)

const (
	MAX_SYNC_DATA_CHAN_NUM = 10000
)

//CReportService ...
type CSyncCacheService struct {
	service.BaseService
	mapCache  util.Map
	syncQueue *util.SyncQueue

	nodeIdList       []int
	syncDataChanList []chan *SyncCacheData
}

type SyncCacheData struct {
	OperType   int8 //0 表示添加或者更新  1表示删除
	Key        string
	Val        string
	Wxpire     int32 //ms
	NodeIdList []int
	reTryCount uint32
	reTryTime  int64
}

//OnInit ...
func (slf *CSyncCacheService) OnInit() error {
	slf.syncQueue = util.NewSyncQueue()
	var callServiceName string
	slf.nodeIdList = cluster.InstanceClusterMgr().GetNodeList("CSyncCacheService.RPC_SyncString", &callServiceName, nil)
	for _, nodeId := range slf.nodeIdList {
		syncCacheData := make(chan *SyncCacheData, MAX_SYNC_DATA_CHAN_NUM)
		slf.syncDataChanList = append(slf.syncDataChanList, syncCacheData)
		go slf.syncRouter(nodeId, syncCacheData)
	}

	return nil
}

func (slf *CSyncCacheService) syncRouter(nodeId int, syncDataChan chan *SyncCacheData) error {

	tryCount := 0
	for {
		select {
		case <-slf.ExitChan:
			break
		case data := <-syncDataChan:
			var ret int
			cluster.CallNode(nodeId, "CSyncCacheService.RPC_SyncString", data, &ret)
			if ret == 0 {
				if tryCount < 3 {
					time.Sleep(800 * time.Millisecond)
				} else {
					time.Sleep(1500 * time.Millisecond)
				}

				slf.tryPushSyncData(syncDataChan, data)
				tryCount++
			} else {
				tryCount = 0
			}
		}
	}

	return nil
}

func (slf *CSyncCacheService) tryPushSyncData(syncDataChan chan *SyncCacheData, syncData *SyncCacheData) bool {
	if len(syncDataChan) >= MAX_SYNC_DATA_CHAN_NUM {
		return false
	}
	syncDataChan <- syncData

	return true
}

func (slf *CSyncCacheService) RPC_SyncString(request *SyncCacheData, ret *int) error {

	if request.OperType == 0 {
		slf.mapCache.Set(request.Key, request.Val)
	} else {
		slf.mapCache.Del(request.Key)
	}

	*ret = 1
	return nil
}

func SetStringJson(key string, val interface{}) error {

	byteBuf, err := json.Marshal(val)
	if err != nil {
		return err
	}

	SetString(key, string(byteBuf))
	return nil
}

func GetStringJson(key string, val interface{}) error {
	ret, err := GetString(key)
	if err != nil {
		return err
	}

	err = json.Unmarshal([]byte(ret), val)
	return err
}

func DelString(key string) error {
	pubcacheservice := service.InstanceServiceMgr().FindService("CSyncCacheService")
	if pubcacheservice == nil {
		return errors.New("Cannot find CSyncCacheService")
	}

	pPubCacheService := pubcacheservice.(*CSyncCacheService)
	syncCacheData := SyncCacheData{1, key, "", 0, pPubCacheService.nodeIdList[:], 0, 0}

	for _, syncChan := range pPubCacheService.syncDataChanList {
		pPubCacheService.tryPushSyncData(syncChan, &syncCacheData)
	}

	return nil
}

func SetString(key string, val string) error {
	pubcacheservice := service.InstanceServiceMgr().FindService("CSyncCacheService")
	if pubcacheservice == nil {
		return errors.New("Cannot find CSyncCacheService")
	}

	//同步所有远程结点
	pPubCacheService := pubcacheservice.(*CSyncCacheService)
	syncCacheData := SyncCacheData{0, key, val, 0, pPubCacheService.nodeIdList[:], 0, 0}
	for _, syncChan := range pPubCacheService.syncDataChanList {
		pPubCacheService.tryPushSyncData(syncChan, &syncCacheData)
	}

	return nil
}

func GetString(key string) (string, error) {
	pubcacheservice := service.InstanceServiceMgr().FindService("CSyncCacheService")
	if pubcacheservice == nil {
		return "", errors.New("Cannot find CSyncCacheService")
	}

	pPubCacheService := pubcacheservice.(*CSyncCacheService)
	ret := pPubCacheService.mapCache.Get(key)
	if ret == nil {
		return "", errors.New(fmt.Sprintf("Cannot find key :%s", key))
	}

	return ret.(string), nil
}
