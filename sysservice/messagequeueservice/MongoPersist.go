package messagequeueservice

import (
	"errors"
	"fmt"
	"github.com/duanhf2012/origin/log"
	"github.com/duanhf2012/origin/service"
	"github.com/duanhf2012/origin/sysmodule/mongodbmodule"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo/options"
	"time"
)

const MaxDays = 180

type DataType interface {
	int | uint | int64 | uint64 | float32 | float64 | int32 | uint32 | int16 | uint16
}

func convertToNumber[DType DataType](val interface{}) (error, DType) {
	switch val.(type) {
	case int64:
		return nil, DType(val.(int64))
	case int:
		return nil, DType(val.(int))
	case uint:
		return nil, DType(val.(uint))
	case uint64:
		return nil, DType(val.(uint64))
	case float32:
		return nil, DType(val.(float32))
	case float64:
		return nil, DType(val.(float64))
	case int32:
		return nil, DType(val.(int32))
	case uint32:
		return nil, DType(val.(uint32))
	case int16:
		return nil, DType(val.(int16))
	case uint16:
		return nil, DType(val.(uint16))
	}

	return errors.New("unsupported type"), 0
}

type MongoPersist struct {
	service.Module
	mongo mongodbmodule.MongoModule

	url        string //连接url
	dbName     string //数据库名称
	retryCount int    //落地数据库重试次数
}

const CustomerCollectName = "SysCustomer"

func (mp *MongoPersist) OnInit() error {
	if errC := mp.ReadCfg(); errC != nil {
		return errC
	}

	err := mp.mongo.Init(mp.url, time.Second*15)
	if err != nil {
		return err
	}

	err = mp.mongo.Start()
	if err != nil {
		log.SError("start dbService[", mp.dbName, "], url[", mp.url, "] init error:", err.Error())
		return err
	}

	//添加索引
	var IndexKey [][]string
	var keys []string
	keys = append(keys, "Customer", "Topic")
	IndexKey = append(IndexKey, keys)
	s := mp.mongo.TakeSession()
	if err := s.EnsureUniqueIndex(mp.dbName, CustomerCollectName, IndexKey, true, true,true); err != nil {
		log.SError("EnsureUniqueIndex is fail ", err.Error())
		return err
	}

	return nil
}

func (mp *MongoPersist) ReadCfg() error {
	mapDBServiceCfg, ok := mp.GetService().GetServiceCfg().(map[string]interface{})
	if ok == false {
		return fmt.Errorf("MessageQueueService config is error")
	}

	//parse MsgRouter
	url, ok := mapDBServiceCfg["Url"]
	if ok == false {
		return fmt.Errorf("MessageQueueService config is error")
	}
	mp.url = url.(string)

	dbName, ok := mapDBServiceCfg["DBName"]
	if ok == false {
		return fmt.Errorf("MessageQueueService config is error")
	}
	mp.dbName = dbName.(string)

	//
	goroutineNum, ok := mapDBServiceCfg["RetryCount"]
	if ok == false {
		return fmt.Errorf("MongoPersist config is error")
	}
	mp.retryCount = int(goroutineNum.(float64))

	return nil
}

func (mp *MongoPersist) OnExit() {
}

// OnReceiveTopicData 当收到推送过来的数据时
func (mp *MongoPersist) OnReceiveTopicData(topic string, topicData []TopicData) {
	//1.收到推送过来的数据，在里面插入_id字段
	for i := 0; i < len(topicData); i++ {
		var document bson.D
		err := bson.Unmarshal(topicData[i].RawData, &document)
		if err != nil {
			topicData[i].RawData = nil
			log.SError(topic, " data Unmarshal is fail ", err.Error())
			continue
		}

		document = append(document, bson.E{Key: "_id", Value: topicData[i].Seq})

		byteRet, err := bson.Marshal(document)
		if err != nil {
			topicData[i].RawData = nil
			log.SError(topic, " data Marshal is fail ", err.Error())
			continue
		}
		topicData[i].ExtendParam = document
		topicData[i].RawData = byteRet
	}
}

// OnPushTopicDataToCustomer 当推送数据到Customer时回调
func (mp *MongoPersist) OnPushTopicDataToCustomer(topic string, topicData []TopicData) {
}

// PersistTopicData 持久化数据
func (mp *MongoPersist) persistTopicData(collectionName string, topicData []TopicData, retryCount int) bool {
	s := mp.mongo.TakeSession()
	ctx, cancel := s.GetDefaultContext()
	defer cancel()

	var documents []interface{}
	for _, tData := range topicData {
		if tData.ExtendParam == nil {
			continue
		}
		documents = append(documents, tData.ExtendParam)
	}

	_, err := s.Collection(mp.dbName, collectionName).InsertMany(ctx, documents)
	if err != nil {
		log.SError("PersistTopicData InsertMany fail,collect name is ", collectionName," error:",err.Error())

		//失败最大重试数量
		return retryCount >= mp.retryCount
	}

	return true
}

func (mp *MongoPersist) IsSameDay(timestamp1 int64,timestamp2 int64) bool{
	t1 := time.Unix(timestamp1, 0)
	t2 := time.Unix(timestamp2, 0)
	return t1.Year() == t2.Year() && t1.Month() == t2.Month()&&t1.Day() == t2.Day()
}

// PersistTopicData 持久化数据
func (mp *MongoPersist) PersistTopicData(topic string, topicData []TopicData, retryCount int) ([]TopicData, []TopicData, bool) {
	if len(topicData) == 0 {
		return nil, nil,true
	}

	preDate := topicData[0].Seq >> 32
	var findPos int
	for findPos = 1; findPos < len(topicData); findPos++ {
		newDate := topicData[findPos].Seq >> 32
		//说明换天了
		if mp.IsSameDay(int64(preDate),int64(newDate)) == false {
			break
		}
	}

	collectName := fmt.Sprintf("%s_%s", topic, mp.GetDateByIndex(topicData[0].Seq))
	ret := mp.persistTopicData(collectName, topicData[:findPos], retryCount)
	//如果失败，下次重试
	if ret == false {
		return nil, nil, false
	}

	//如果成功
	return topicData[findPos:len(topicData)], topicData[0:findPos], true
}

// FindTopicData 查找数据
func (mp *MongoPersist) findTopicData(topic string, startIndex uint64, limit int64,topicBuff []TopicData) ([]TopicData, bool) {
	s := mp.mongo.TakeSession()


	condition := bson.D{{Key: "_id", Value: bson.D{{Key: "$gt", Value: startIndex}}}}

	var findOption options.FindOptions
	findOption.SetLimit(limit)
	var findOptions []*options.FindOptions
	findOptions = append(findOptions, &findOption)

	ctx, cancel := s.GetDefaultContext()
	defer cancel()
	collectName := fmt.Sprintf("%s_%s", topic, mp.GetDateByIndex(startIndex))
	cursor, err := s.Collection(mp.dbName, collectName).Find(ctx, condition, findOptions...)
	if err != nil || cursor.Err() != nil {
		if err == nil {
			err = cursor.Err()
		}

		if err != nil {
			log.SError("find collect name ", topic, " is error:", err.Error())
			return nil, false
		}

		return nil, false
	}

	var res []interface{}
	ctxAll, cancelAll := s.GetDefaultContext()
	defer cancelAll()
	err = cursor.All(ctxAll, &res)
	if err != nil {
		if err != nil {
			log.SError("find collect name ", topic, " is error:", err.Error())
			return nil, false
		}

		return nil, false
	}

	//序列化返回
	for i := 0; i < len(res); i++ {
		rawData, errM := bson.Marshal(res[i])
		if errM != nil {
			if errM != nil {
				log.SError("collect name ", topic, " Marshal is error:", err.Error())
				return nil, false
			}
			continue
		}
		topicBuff = append(topicBuff, TopicData{RawData: rawData})
	}

	return topicBuff, true
}

func (mp *MongoPersist) IsYesterday(startIndex uint64) (bool,string){
	timeStamp := int64(startIndex>>32)

	startTime := time.Unix(timeStamp, 0).AddDate(0,0,1)
	nowTm := time.Now()

	return startTime.Year() == nowTm.Year() && startTime.Month() == nowTm.Month()&&startTime.Day() == nowTm.Day(),nowTm.Format("20060102")
}

func (mp *MongoPersist) getCollectCount(topic string,today string) (int64 ,error){
	s := mp.mongo.TakeSession()
	ctx, cancel := s.GetDefaultContext()
	defer cancel()
	collectName := fmt.Sprintf("%s_%s", topic, today)
	count, err := s.Collection(mp.dbName, collectName).EstimatedDocumentCount(ctx)
	return count,err
}

// FindTopicData 查找数据
func (mp *MongoPersist) FindTopicData(topic string, startIndex uint64, limit int64,topicBuff []TopicData) []TopicData {
	//某表找不到，一直往前找，找到当前置为止
	for days := 1; days <= MaxDays; days++ {
		//是否可以跳天
		//在换天时，如果记录在其他协程还没insert完成时，因为没查到直接跳到第二天，导致漏掉数据
		//解决的方法是在换天时，先判断新换的当天有没有记录，有记录时，说明昨天的数据已经插入完成，才可以跳天查询
		IsJumpDays := true

		//如果是昨天，先判断当天有没有表数据
		bYesterday,strToday := mp.IsYesterday(startIndex)
		if  bYesterday {
			count,err := mp.getCollectCount(topic,strToday)
			if err != nil {
				//失败时，重新开始
				log.SError("getCollectCount ",topic,"_",strToday," is fail:",err.Error())
				return nil
			}
			//当天没有记录，则不能跳表，有可能当天还有数据
			if count == 0 {
				IsJumpDays = false
			}
		}

		//从startIndex开始一直往后查
		topicData, isSucc := mp.findTopicData(topic, startIndex, limit,topicBuff)
		//有数据或者数据库出错时返回，返回后，会进行下一轮的查询遍历
		if len(topicData) > 0 || isSucc == false {
			return topicData
		}

		//找不到数据时，判断当前日期是否一致
		if mp.GetDateByIndex(startIndex) >= mp.GetNowTime() {
			break
		}

		//不允许跳天，则直接跳出
		if IsJumpDays == false {
			break
		}

		startIndex = mp.GetNextIndex(startIndex, days)
	}

	return nil
}

func (mp *MongoPersist) GetNowTime() string {
	now := time.Now()
	zeroTime := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	return zeroTime.Format("20060102")
}

func (mp *MongoPersist) GetDateByIndex(startIndex uint64) string {
	startTm := int64(startIndex >> 32)
	return time.Unix(startTm, 0).Format("20060102")
}

func (mp *MongoPersist) GetNextIndex(startIndex uint64, addDay int) uint64 {
	startTime := time.Unix(int64(startIndex>>32), 0)
	dateTime := time.Date(startTime.Year(), startTime.Month(), startTime.Day(), 0, 0, 0, 0, startTime.Location())
	newDateTime := dateTime.AddDate(0, 0, addDay)
	nextIndex := uint64(newDateTime.Unix()) << 32
	return nextIndex
}

// LoadCustomerIndex false时代表获取失败，一般是读取错误，会进行重试。如果不存在时，返回(0,true)
func (mp *MongoPersist) LoadCustomerIndex(topic string, customerId string) (uint64, bool) {
	s := mp.mongo.TakeSession()
	ctx, cancel := s.GetDefaultContext()
	defer cancel()

	condition := bson.D{{Key: "Customer", Value: customerId}, {Key: "Topic", Value: topic}}
	cursor, err := s.Collection(mp.dbName, CustomerCollectName).Find(ctx, condition)
	if err != nil {
		log.SError("Load topic ", topic, " customer ", customerId, " is fail:", err.Error())
		return 0, false
	}

	type findRes struct {
		Index uint64 `bson:"Index,omitempty"`
	}

	var res []findRes
	ctxAll, cancelAll := s.GetDefaultContext()
	defer cancelAll()
	err = cursor.All(ctxAll, &res)
	if err != nil {
		log.SError("Load topic ", topic, " customer ", customerId, " is fail:", err.Error())
		return 0, false
	}

	if len(res) == 0 {
		return 0, true
	}

	return res[0].Index, true
}

// GetIndex 通过topic数据获取进度索引号
func (mp *MongoPersist) GetIndex(topicData *TopicData) uint64 {
	if topicData.Seq > 0 {
		return topicData.Seq
	}

	var document bson.D
	err := bson.Unmarshal(topicData.RawData, &document)
	if err != nil {
		log.SError("GetIndex is fail ", err.Error())
		return 0
	}

	for _, e := range document {
		if e.Key == "_id" {
			errC, seq := convertToNumber[uint64](e.Value)
			if errC != nil {
				log.Error("value is error:%s,%+v, ", errC.Error(), e.Value)
			}

			return seq
		}
	}
	return topicData.Seq
}

// PersistIndex 持久化进度索引号
func (mp *MongoPersist) PersistIndex(topic string, customerId string, index uint64) {
	s := mp.mongo.TakeSession()

	condition := bson.D{{Key: "Customer", Value: customerId}, {Key: "Topic", Value: topic}}
	upsert := bson.M{"Customer": customerId, "Topic": topic, "Index": index}
	updata := bson.M{"$set": upsert}

	var UpdateOptionsOpts []*options.UpdateOptions
	UpdateOptionsOpts = append(UpdateOptionsOpts, options.Update().SetUpsert(true))

	ctx, cancel := s.GetDefaultContext()
	defer cancel()
	_, err := s.Collection(mp.dbName, CustomerCollectName).UpdateOne(ctx, condition, updata, UpdateOptionsOpts...)
	if err != nil {
		log.SError("PersistIndex fail :", err.Error())
	}
}
