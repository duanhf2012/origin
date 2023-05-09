package rankservice

import (
	"fmt"
	"github.com/duanhf2012/origin/log"
	"github.com/duanhf2012/origin/rpc"
	"github.com/duanhf2012/origin/service"
	"github.com/duanhf2012/origin/sysmodule/mongodbmodule"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo/options"
	"runtime"
	"sync"
	"sync/atomic"
	"time"
)

const batchRemoveNum  = 128  //一切删除的最大数量

// RankDataDB 排行表数据
type RankDataDB struct {
	Id                 uint64                      `bson:"_id"`
	RefreshTime        int64                       `bson:"RefreshTime"`
	SortData             []int64  				   `bson:"SortData"`
	Data                 []byte   				   `bson:"Data"`
	ExData               []int64                    `bson:"ExData"`
}

// MongoPersist持久化Module
type MongoPersist struct {
	service.Module
	mongo mongodbmodule.MongoModule

	url        string //Mongodb连接url
	dbName     string //数据库名称
	SaveInterval time.Duration    //落地数据库时间间隔

	sync.Mutex
	mapRemoveRankData map[uint64]map[uint64]struct{} //将要删除的排行数据 map[RankId]map[Key]struct{}
	mapUpsertRankData map[uint64]map[uint64]RankData //需要upsert的排行数据 map[RankId][key]RankData

	mapRankSkip map[uint64]IRankSkip  //所有的排行榜对象map[RankId]IRankSkip
	maxRetrySaveCount int  			  //存档重试次数
	retryTimeIntervalMs time.Duration //重试时间间隔

	lastSaveTime time.Time  //最后一次存档时间

	stop int32 //是否停服
	waitGroup sync.WaitGroup //等待停服
}

func (mp *MongoPersist) OnInit() error {
	mp.mapRemoveRankData = map[uint64]map[uint64]struct{}{}
	mp.mapUpsertRankData = map[uint64]map[uint64]RankData{}
	mp.mapRankSkip = map[uint64]IRankSkip{}

	if errC := mp.ReadCfg(); errC != nil {
		return errC
	}

	//初始化MongoDB
	err := mp.mongo.Init(mp.url, time.Second*15)
	if err != nil {
		return err
	}

	//开始运行
	err = mp.mongo.Start()
	if err != nil {
		log.SError("start dbService[", mp.dbName, "], url[", mp.url, "] init error:", err.Error())
		return err
	}

	//开启协程
	mp.waitGroup.Add(1)
	go mp.persistCoroutine()

	return nil
}

func (mp *MongoPersist) ReadCfg() error {
	mapDBServiceCfg, ok := mp.GetService().GetServiceCfg().(map[string]interface{})
	if ok == false {
		return fmt.Errorf("RankService config is error")
	}

	//读取数据库配置
	saveMongoCfg,ok := mapDBServiceCfg["SaveMongo"]
	if ok == false {
		return fmt.Errorf("RankService.SaveMongo config is error")
	}

	mongodbCfg,ok := saveMongoCfg.(map[string]interface{})
	if ok == false {
		return fmt.Errorf("RankService.SaveMongo config is error")
	}

	url, ok := mongodbCfg["Url"]
	if ok == false {
		return fmt.Errorf("RankService.SaveMongo.Url config  is error")
	}
	mp.url = url.(string)

	dbName, ok := mongodbCfg["DBName"]
	if ok == false {
		return fmt.Errorf("RankService.SaveMongo.DBName config is error")
	}
	mp.dbName = dbName.(string)

	saveInterval, ok := mongodbCfg["SaveIntervalMs"]
	if ok == false {
		return fmt.Errorf("RankService.SaveMongo.SaveIntervalMs config is error")
	}

	mp.SaveInterval = time.Duration(saveInterval.(float64))*time.Millisecond

	maxRetrySaveCount, ok := mongodbCfg["MaxRetrySaveCount"]
	if ok == false {
		return fmt.Errorf("RankService.SaveMongo.MaxRetrySaveCount config is error")
	}
	mp.maxRetrySaveCount = int(maxRetrySaveCount.(float64))

	retryTimeIntervalMs, ok := mongodbCfg["RetryTimeIntervalMs"]
	if ok == false {
		return fmt.Errorf("RankService.SaveMongo.RetryTimeIntervalMs config is error")
	}
	mp.retryTimeIntervalMs = time.Duration(retryTimeIntervalMs.(float64))*time.Millisecond

	return nil
}

//启服从数据库加载
func (mp *MongoPersist) OnStart() {
}

func (mp *MongoPersist)  OnSetupRank(manual bool,rankSkip *RankSkip) error{
	if mp.mapRankSkip == nil {
		mp.mapRankSkip = map[uint64]IRankSkip{}
	}

	mp.mapRankSkip[rankSkip.GetRankID()] = rankSkip
	if manual == true {
		return nil
	}

	log.SRelease("start load rank ",rankSkip.GetRankName()," from mongodb.")
	err := mp.loadFromDB(rankSkip.GetRankID(),rankSkip.GetRankName())
	if  err != nil {
		log.SError("load from db is fail :%s",err.Error())
		return err
	}
	log.SRelease("finish load rank ",rankSkip.GetRankName()," from mongodb.")
	return nil
}

func (mp *MongoPersist) loadFromDB(rankId uint64,rankCollectName string) error{
	s := mp.mongo.TakeSession()
	ctx, cancel := s.GetDefaultContext()
	defer cancel()

	condition := bson.D{}
	cursor, err := s.Collection(mp.dbName, rankCollectName).Find(ctx, condition)
	if err != nil {
		log.SError("find collect name ", rankCollectName, " is error:", err.Error())
		return err
	}

	if cursor.Err()!=nil {
		log.SError("find collect name ", rankCollectName, " is error:", cursor.Err().Error())
		return err
	}

	rankSkip := mp.mapRankSkip[rankId]
	if rankSkip == nil {
		err = fmt.Errorf("rank ", rankCollectName, " is not setup:")
		log.SError(err.Error())
		return err
	}

	defer cursor.Close(ctx)
	for cursor.Next(ctx) {
		var rankDataDB RankDataDB
		err = cursor.Decode(&rankDataDB)
		if err != nil {
			log.SError(" collect name ", rankCollectName, " Decode is error:", err.Error())
			return err
		}

		var rankData rpc.RankData
		rankData.Data = rankDataDB.Data
		rankData.Key = rankDataDB.Id
		rankData.SortData = rankDataDB.SortData
		for _,eData := range rankDataDB.ExData{
			rankData.ExData = append(rankData.ExData,&rpc.ExtendIncData{InitValue:eData})
		}

		//更新到排行榜
		rankSkip.UpsetRank(&rankData,rankDataDB.RefreshTime,true)
	}

	return nil
}

func (mp *MongoPersist) lazyInitRemoveMap(rankId uint64){
	if mp.mapRemoveRankData[rankId] == nil {
		mp.mapRemoveRankData[rankId] = make(map[uint64]struct{},256)
	}
}

func (mp *MongoPersist) lazyInitUpsertMap(rankId uint64){
	if mp.mapUpsertRankData[rankId] == nil {
		mp.mapUpsertRankData[rankId] = make(map[uint64]RankData,256)
	}
}

func (mp *MongoPersist) OnEnterRank(rankSkip IRankSkip, enterData *RankData){
	mp.Lock()
	defer mp.Unlock()

	delete(mp.mapRemoveRankData,enterData.Key)

	mp.lazyInitUpsertMap(rankSkip.GetRankID())
	mp.mapUpsertRankData[rankSkip.GetRankID()][enterData.Key] = *enterData
}


func (mp *MongoPersist) OnLeaveRank(rankSkip IRankSkip, leaveData *RankData){
	mp.Lock()
	defer mp.Unlock()

	//先删掉更新中的数据
	delete(mp.mapUpsertRankData,leaveData.Key)
	mp.lazyInitRemoveMap(rankSkip.GetRankID())
	mp.mapRemoveRankData[rankSkip.GetRankID()][leaveData.Key] = struct{}{}
}

func (mp *MongoPersist) OnChangeRankData(rankSkip IRankSkip, changeData *RankData){
	mp.Lock()
	defer mp.Unlock()

	//先删掉要删除的数据
	delete(mp.mapRemoveRankData,changeData.Key)

	//更新数据
	mp.lazyInitUpsertMap(rankSkip.GetRankID())
	mp.mapUpsertRankData[rankSkip.GetRankID()][changeData.Key] = *changeData
}

//停存持久化到DB
func (mp *MongoPersist) OnStop(mapRankSkip map[uint64]*RankSkip){
	atomic.StoreInt32(&mp.stop,1)
	mp.waitGroup.Wait()
}

func (mp *MongoPersist) JugeTimeoutSave() bool{
	timeout := time.Now()
	isTimeOut := timeout.Sub(mp.lastSaveTime) >= mp.SaveInterval
	if isTimeOut == true {
		mp.lastSaveTime = timeout
	}

	return isTimeOut
}

func (mp *MongoPersist)  persistCoroutine(){
	defer mp.waitGroup.Done()
	for atomic.LoadInt32(&mp.stop)==0 || mp.hasPersistData(){
		//间隔时间sleep
		time.Sleep(time.Second*1)

		//没有持久化数据continue
		if mp.hasPersistData() == false {
			continue
		}

		if mp.JugeTimeoutSave() == false{
			continue
		}

		//存档数据到数据库
		mp.saveToDB()
	}

	//退出时存一次档
	mp.saveToDB()
}

func (mp *MongoPersist) hasPersistData() bool{
	mp.Lock()
	defer mp.Unlock()

	return len(mp.mapUpsertRankData)>0 || len(mp.mapRemoveRankData) >0
}

func (mp *MongoPersist) saveToDB(){
	defer func() {
		if r := recover(); r != nil {
			buf := make([]byte, 4096)
			l := runtime.Stack(buf, false)
			errString := fmt.Sprint(r)
			log.SError(" Core dump info[", errString, "]\n", string(buf[:l]))
		}
	}()

	//1.copy数据
	mp.Lock()
	mapRemoveRankData := mp.mapRemoveRankData
	mapUpsertRankData := mp.mapUpsertRankData
	mp.mapRemoveRankData = map[uint64]map[uint64]struct{}{}
	mp.mapUpsertRankData = map[uint64]map[uint64]RankData{}
	mp.Unlock()

	//2.存档
	for len(mapUpsertRankData) > 0 {
		mp.upsertRankDataToDB(mapUpsertRankData)
	}

	for len(mapRemoveRankData) >0 {
		mp.removeRankDataToDB(mapRemoveRankData)
	}

}

func (mp *MongoPersist) removeToDB(collectName string,keys []uint64) error{
	s := mp.mongo.TakeSession()
	ctx, cancel := s.GetDefaultContext()
	defer cancel()

	condition := bson.D{{Key: "_id", Value: bson.M{"$in": keys}}}

	_, err := s.Collection(mp.dbName, collectName).DeleteMany(ctx, condition)
	if err != nil {
		log.SError("MongoPersist DeleteMany fail,collect name is ", collectName)
		return err
	}

	return nil
}

func (mp *MongoPersist) removeRankData(rankId uint64,keys []uint64) bool {
	rank := mp.mapRankSkip[rankId]
	if rank== nil {
		log.SError("cannot find rankId ",rankId,"config")
		return false
	}

	//不成功则重试maxRetrySaveCount次
	for i:=0;i<mp.maxRetrySaveCount;i++{
		if mp.removeToDB(rank.GetRankName(),keys)!= nil {
			time.Sleep(mp.retryTimeIntervalMs)
			continue
		}
		break
	}

	return true
}

func (mp *MongoPersist) upsertToDB(collectName string,rankData *RankData) error{
	condition := bson.D{{"_id", rankData.Key}}
	upsert := bson.M{"_id":rankData.Key,"RefreshTime": rankData.refreshTimestamp, "SortData": rankData.SortData, "Data": rankData.Data,"ExData":rankData.ExData}
	update := bson.M{"$set": upsert}

	s := mp.mongo.TakeSession()
	ctx, cancel := s.GetDefaultContext()
	defer cancel()

	updateOpts := options.Update().SetUpsert(true)
	_, err := s.Collection(mp.dbName, collectName).UpdateOne(ctx, condition,update,updateOpts)
	if err != nil {
		log.SError("MongoPersist upsertDB fail,collect name is ", collectName)
		return err
	}

	return nil
}

func (mp *MongoPersist) upsertRankDataToDB(mapUpsertRankData map[uint64]map[uint64]RankData) error{
	for rankId,mapRankData := range  mapUpsertRankData{
		rank,ok := mp.mapRankSkip[rankId]
		if ok == false {
			log.SError("cannot find rankId ",rankId,",config is error")
			delete(mapUpsertRankData,rankId)
			continue
		}

		for key,rankData := range mapRankData{
			//最大重试mp.maxRetrySaveCount次
			for i:=0;i<mp.maxRetrySaveCount;i++{
				err := mp.upsertToDB(rank.GetRankName(),&rankData)
				if err != nil {
					time.Sleep(mp.retryTimeIntervalMs)
					continue
				}
				break
			}

			//存完删掉指定key
			delete(mapRankData,key)
		}

		if len(mapRankData) == 0 {
			delete(mapUpsertRankData,rankId)
		}
	}

	return nil
}

func (mp *MongoPersist) removeRankDataToDB(mapRemoveRankData map[uint64]map[uint64]struct{}) {
	for rankId ,mapRemoveKey := range  mapRemoveRankData{
		//每100个一删
		keyList := make([]uint64,0,batchRemoveNum)
		 for key := range mapRemoveKey {
			 delete(mapRemoveKey,key)
			 keyList = append(keyList,key)
			 if len(keyList) >= batchRemoveNum {
				 break
			 }
		 }

		mp.removeRankData(rankId,keyList)

		 //如果删完，删掉rankid下所有
		 if len(mapRemoveKey) == 0 {
			 delete(mapRemoveRankData,rankId)
		 }
	}

}
