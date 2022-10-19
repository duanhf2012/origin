package redismodule

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/duanhf2012/origin/log"
	"strconv"
	"time"

	"github.com/duanhf2012/origin/service"
	"github.com/gomodule/redigo/redis"
)

type RetError struct {
	resultChan chan error
}

func (slf *RetError) Get() error {
	return <-slf.resultChan
}

type ErrorMapStringBool struct {
	resultError         error
	resultMapStringBool map[string]bool
}

type RetMapString struct {
	resultChan chan ErrorMapStringBool
}

func (slf *RetMapString) Get() (error, map[string]bool) {
	ret := <-slf.resultChan
	return ret.resultError, ret.resultMapStringBool
}

type RedisModule struct {
	service.Module
	redisPool *redis.Pool
}

// ConfigRedis 服务器配置
type ConfigRedis struct {
	IP          string
	Port        int
	Password    string
	DbIndex     int
	MaxIdle     int //最大的空闲连接数，表示即使没有redis连接时依然可以保持N个空闲的连接，而不被清除，随时处于待命状态。
	MaxActive   int //最大的激活连接数，表示同时最多有N个连接
	IdleTimeout int //最大的空闲连接等待时间，超过此时间后，空闲连接将被关闭
}

func (m *RedisModule) Init(redisCfg *ConfigRedis) {
	redisServer := fmt.Sprintf("%s:%d", redisCfg.IP, redisCfg.Port)
	m.redisPool = &redis.Pool{
		Wait:        true,
		MaxIdle:     redisCfg.MaxIdle,
		MaxActive:   redisCfg.MaxActive,
		IdleTimeout: time.Duration(redisCfg.IdleTimeout) * time.Second,
		Dial: func() (redis.Conn, error) {
			// 连接数据库
			opt := []redis.DialOption{redis.DialDatabase(redisCfg.DbIndex)}
			if redisCfg.Password != "" {
				opt = append(opt, redis.DialPassword(redisCfg.Password))
			}
			c, err := redis.Dial("tcp", redisServer, opt...)
			if err != nil {
				log.Error("Connect redis fail reason:%v", err)
				return nil, err
			}

			return c, err
		},

		TestOnBorrow: func(c redis.Conn, t time.Time) error {
			if time.Since(t) < time.Minute {
				return nil
			}
			_, err := c.Do("PING")
			if err != nil {
				log.Error("Do PING fail reason:%v", err)
				return err
			}
			return err
		},
	}
}

func (m *RedisModule) getConn() (redis.Conn, error) {
	if m.redisPool == nil {
		log.Error("Not Init RedisModule")
		return nil, fmt.Errorf("Not Init RedisModule")
	}
	conn := m.redisPool.Get()
	if conn == nil {
		log.Error("Cannot get connection")
		return nil, fmt.Errorf("Cannot get connection")
	}

	if conn.Err() != nil {
		err := conn.Err()
		if err != nil {
			log.Error("Get Conn have error,reason:%v", err)
		}
		conn.Close()
		return nil, err
	}
	return conn, nil
}

func (m *RedisModule) TestPingRedis() error {
	conn, err := m.getConn()
	if err != nil {
		return err
	}
	defer conn.Close()

	err = m.redisPool.TestOnBorrow(conn, time.Now())
	if err != nil {
		log.Error("TestOnBorrow fail,reason:%v", err)
		return err
	}

	return nil
}

func (m *RedisModule) SetString(key, value interface{}) (err error) {
	err = m.setStringByExpire(key, value, "-1")

	return err
}

func (m *RedisModule) SetStringExpire(key, value, expire string) (err error) {
	err = m.setStringByExpire(key, value, expire)

	return err
}

func (m *RedisModule) SetStringJSON(key interface{}, val interface{}) (err error) {
	err = m.SetStringJSONExpire(key, val, "-1")

	return err
}

func (m *RedisModule) SetStringJSONExpire(key interface{}, val interface{}, expire string) (err error) {
	if temp, err := json.Marshal(val); err == nil {
		err = m.setStringByExpire(key, string(temp), expire)
	}

	return err
}

func (m *RedisModule) setStringByExpire(key, value, expire interface{}) error {
	if key == "" {
		return errors.New("Key Is Empty")
	}

	conn, err := m.getConn()
	if err != nil {
		return err
	}
	defer conn.Close()

	var ret interface{}
	var retErr error
	if expire == "-1" {
		ret, retErr = conn.Do("SET", key, value)
	} else {
		ret, retErr = conn.Do("SET", key, value, "EX", expire)
	}

	if retErr != nil {
		log.Error("setStringByExpire fail,reason:%v", retErr)
		return retErr
	}

	_, ok := ret.(string)
	if !ok {
		retErr = errors.New("setStringByExpire redis data is error")
		log.Error("setStringByExpire redis data is error")
		return retErr
	}

	return nil
}

func (m *RedisModule) HSetStruct(key string, val interface{}) error {
	conn, err := m.getConn()
	if err != nil {
		return err
	}
	defer conn.Close()

	_, err = conn.Do("HSET", redis.Args{}.Add(key).AddFlat(val)...)
	if err != nil {
		return err
	}
	return nil
}

func (m *RedisModule) HGetStruct(key string, out_val interface{}) error {
	conn, err := m.getConn()
	if err != nil {
		return err
	}
	defer conn.Close()

	v, err := redis.Values(conn.Do("HGETALL", key))
	if err != nil {
		return err
	}
	err = redis.ScanStruct(v, out_val)
	if err != nil {
		return err
	}

	return nil
}
func (m *RedisModule) SetStringMap(mapInfo map[interface{}]interface{}) (err error) {
	err = m.setMuchStringByExpire(mapInfo, "-1")
	return
}

func (m *RedisModule) SetMuchStringExpire(mapInfo map[interface{}]interface{}, ex string) (err error) {
	err = m.setMuchStringByExpire(mapInfo, ex)
	return
}

func (m *RedisModule) setMuchStringByExpire(mapInfo map[interface{}]interface{}, expire string) error {
	if len(mapInfo) <= 0 {
		log.Error("setMuchStringByExpire  Info Is Empty")
		return errors.New("setMuchStringByExpire  Info Is Empty")
	}

	conn, err := m.getConn()
	if err != nil {
		return err
	}
	defer conn.Close()

	// 开始Send数据
	var serr error
	conn.Send("MULTI")
	for key, val := range mapInfo {
		if expire == "-1" {
			serr = conn.Send("SET", key, val)
		} else {
			serr = conn.Send("SET", key, val, "EX", expire)
		}
		if serr != nil {
			break
		}
	}

	if serr != nil {
		log.Error("setMuchStringByExpire fail,reason:%v", serr)
		conn.Do("DISCARD")
		return serr
	} else {
		_, err = conn.Do("EXEC")
	}

	if err != nil {
		log.Error("setMuchStringByExpire fail,reason:%v", err)
	}

	return err
}

func (m *RedisModule) GetString(key interface{}) (string, error) {
	conn, err := m.getConn()
	if err != nil {
		return "", err
	}
	defer conn.Close()

	ret, err := conn.Do("GET", key)
	if err != nil {
		log.Error("GetString fail,reason:%v", err)
		return "", err
	}

	if ret == nil {
		err = errors.New("GetString key is not exist!")
		return "", err
	}

	return redis.String(ret, nil)
}

func (m *RedisModule) GetStringJSON(key string, st interface{}) error {
	conn, err := m.getConn()
	if err != nil {
		return err
	}
	defer conn.Close()

	ret, err := conn.Do("GET", key)
	if err != nil {
		log.Error("GetStringJSON fail,reason:%v", err)
		return err
	}

	if ret == nil {
		err = errors.New("GetStringJSON Key is not exist")
		return err
	}

	str, ok := ret.([]byte)
	if !ok {
		err = errors.New("GetStringJSON redis data is error!")
		log.Error("GetStringJSON redis data is error!")
		return err
	}

	if err = json.Unmarshal(str, st); err != nil {
		log.Error("GetStringJSON fail json.Unmarshal is error:%s,%s,reason:%v", key, string(str), err)
		return err
	}

	return nil
}

func (m *RedisModule) GetStringMap(keys []string) (retMap map[string]string, err error) {
	if len(keys) <= 0 {
		err = errors.New("Func[GetMuchRedisString] Keys Is Empty")
		return
	}
	conn, err := m.getConn()
	if err != nil {
		return nil, err
	}
	defer conn.Close()

	// 开始Send数据
	err = conn.Send("MULTI")
	if err != nil {
		log.Error("GetMuchString fail %v", err)
		return nil, err
	}
	for _, val := range keys {
		err = conn.Send("GET", val)
		if err != nil {
			log.Error("GetMuchString fail,reason:%v", err)
			conn.Do("DISCARD")
			return nil, err
		}
	}

	// 执行命令
	ret, err := conn.Do("EXEC")
	if err != nil {
		log.Error("GetMuchString fail %v", err)
		return
	}

	retList, ok := ret.([]interface{})
	if !ok {
		err = errors.New("Func[GetMuchRedisString] Redis Data Error")
		return
	}

	retMap = make(map[string]string)
	for index, val := range retList {
		strVal, ok := val.([]byte)
		if !ok {
			retMap[keys[index]] = ""
			continue
		}
		retMap[keys[index]] = string(strVal)
	}
	err = nil
	return
}

func (m *RedisModule) ExistsKey(key interface{}) (bool, error) {
	conn, err := m.getConn()
	if err != nil {
		return false, err
	}
	defer conn.Close()

	ret, err := conn.Do("EXISTS", key)
	if err != nil {
		log.Error("ExistsKey fail, reason:%v", err)
		return false, err
	}
	retValue, ok := ret.(int64)
	if !ok {
		err = errors.New("Func[ExistsKey] Redis Data Error")
		return false, err
	}

	return retValue != 0, nil
}

func (m *RedisModule) DelString(key interface{}) error {
	conn, err := m.getConn()
	if err != nil {
		return err
	}
	defer conn.Close()

	ret, err := conn.Do("DEL", key)
	if err != nil {
		log.Error("DelString fail, reason:%v", err)
		return err
	}

	retValue, ok := ret.(int64)
	if !ok {
		err = errors.New("Func[DelRedisString] Redis Data Error")
		return err
	}

	if retValue == 0 {
		err = fmt.Errorf("Func[DelRedisString] Delete Key(%s) not exists", key)
		return err
	}

	return nil
}

func (m *RedisModule) DelStringKeyList(keys []interface{}) (map[interface{}]bool, error) {
	if len(keys) <= 0 {
		err := errors.New("Func[DelMuchRedisString] Keys Is Empty")
		return nil, err
	}

	conn, err := m.getConn()
	if err != nil {
		return nil, err
	}
	defer conn.Close()

	// 开始Send数据
	conn.Send("MULTI")
	for _, val := range keys {
		err = conn.Send("DEL", val)
		if err != nil {
			log.Error("DelMuchString fail,reason:%v", err)
			conn.Do("DISCARD")
			return nil, err
		}
	}
	// 执行命令
	ret, err := conn.Do("EXEC")

	if err != nil {
		log.Error("DelMuchString fail,reason:%v", err)
		return nil, err
	}

	retList, ok := ret.([]interface{})
	if !ok {
		err = errors.New("Func[DelMuchRedisString] Redis Data Error")
		return nil, err
	}

	retMap := map[interface{}]bool{}
	for index, val := range retList {
		iVal, ok := val.(int64)
		if !ok || iVal == 0 {
			retMap[keys[index]] = false
			continue
		}

		retMap[keys[index]] = true
	}

	return retMap, nil
}

func (m *RedisModule) SetHash(redisKey, hashKey, value interface{}) error {
	if redisKey == "" || hashKey == "" {
		return errors.New("Key Is Empty")
	}
	conn, err := m.getConn()
	if err != nil {
		return err
	}
	defer conn.Close()

	_, retErr := conn.Do("HSET", redisKey, hashKey, value)
	if retErr != nil {
		log.Error("SetHash fail,reason:%v", retErr)
	}

	return retErr
}

// GetRedisAllHashJSON ...
func (m *RedisModule) GetAllHashJSON(redisKey string) (map[string]string, error) {
	if redisKey == "" {
		return nil, errors.New("Key Is Empty")
	}
	conn, err := m.getConn()
	if err != nil {
		return nil, err
	}
	defer conn.Close()

	value, err := conn.Do("HGETALL", redisKey)
	if err != nil {
		log.Error("GetAllHashJSON fail,reason:%v", err)
		return nil, err
	}

	return redis.StringMap(value, err)
}

func (m *RedisModule) GetHash(redisKey interface{}, fieldKey interface{}) (string, error) {
	if redisKey == "" || fieldKey == "" {
		log.Error("GetHashValueByKey key is empty!")
		return "", errors.New("Key Is Empty")
	}
	conn, err := m.getConn()
	if err != nil {
		return "", err
	}
	defer conn.Close()

	value, err := conn.Do("HGET", redisKey, fieldKey)
	if err != nil {
		log.Error("GetHashValueByKey fail,reason:%v", err)
		return "", err
	}
	if value == nil {
		return "", errors.New("Reids Get Hash nil")
	}

	return redis.String(value, nil)
}

func (m *RedisModule) GetMuchHash(args ...interface{}) ([]string, error) {
	if len(args) < 2 {
		log.Error("GetHashValueByHashKeyList key len less than two!")
		return nil, errors.New("Key Is Empty")
	}
	conn, err := m.getConn()
	if err != nil {
		return nil, err
	}
	defer conn.Close()

	value, err := conn.Do("HMGET", args...)
	if err != nil {
		log.Error("GetHashValueByKey fail,reason:%v", err)
		return nil, err
	}
	if value == nil {
		return nil, errors.New("Reids Get Hash nil")
	}

	valueList := value.([]interface{})
	retList := []string{}
	for _, valueItem := range valueList {
		valueByte, ok := valueItem.([]byte)
		if !ok {
			retList = append(retList, "")
		} else {
			retList = append(retList, string(valueByte))
		}
	}

	return retList, nil
}

func (m *RedisModule) ScanMatchKeys(cursorValue int, redisKey string, count int) (int, []string, error) {
	retKeys := []string{}
	nextCursorValue := 0
	if redisKey == "" {
		log.Error("ScanMatchKeys key is empty!")
		return nextCursorValue, nil, errors.New("Key Is Empty")
	}

	conn, err := m.getConn()
	if err != nil {
		return nextCursorValue, nil, err
	}
	defer conn.Close()

	value, err := conn.Do("SCAN", cursorValue, "match", redisKey, "count", count)
	if err != nil {
		log.Error("GetHashValueByKey fail,reason:%v", err)
		return nextCursorValue, nil, err
	}
	if value == nil {
		return nextCursorValue, nil, errors.New("Reids Get Hash nil")
	}

	valueList := value.([]interface{})
	nextCursorValue, _ = strconv.Atoi(string(valueList[0].([]byte)))
	keysList := valueList[1].([]interface{})
	for _, keysItem := range keysList {
		retKeys = append(retKeys, string(keysItem.([]byte)))
	}

	return nextCursorValue, retKeys, nil
}

func (m *RedisModule) SetHashMapJSON(redisKey string, mapFieldValue map[interface{}]interface{}) error {
	if len(mapFieldValue) <= 0 {
		err := errors.New("Func[SetMuchRedisHashJSON] value Is Empty")
		return err
	}

	conn, err := m.getConn()
	if err != nil {
		return err
	}
	defer conn.Close()

	// 开始Send数据
	conn.Send("MULTI")
	for symbol, val := range mapFieldValue {
		temp, err := json.Marshal(val)
		if err == nil {
			_, err = conn.Do("HSET", redisKey, symbol, temp)
			if err != nil {
				log.Error("SetMuchHashJSON fail,reason:%v", err)
				conn.Send("DISCARD")
				return err
			}
		}
	}
	// 执行命令
	_, err = conn.Do("EXEC")
	if err != nil {
		log.Error("SetMuchHashJSON fail,reason:%v", err)
		conn.Send("DISCARD")
	}
	return err
}

func (m *RedisModule) DelHash(args ...interface{}) error {
	conn, err := m.getConn()
	if err != nil {
		return err
	}
	defer conn.Close()

	_, retErr := conn.Do("HDEL", args...)
	if retErr != nil {
		log.Error("DelMuchHash fail,reason:%v", retErr)
	}
	return retErr
}

func (m *RedisModule) LPushList(args ...interface{}) error {
	err := m.setListPush("LPUSH", args...)
	return err
}

func (m *RedisModule) LPushListJSON(key interface{}, value ...interface{}) error {
	return m.setListJSONPush("LPUSH", key, value...)
}

func (m *RedisModule) RPushList(args ...interface{}) error {
	err := m.setListPush("RPUSH", args...)
	return err
}

func (m *RedisModule) RPushListJSON(key interface{}, value ...interface{}) error {
	return m.setListJSONPush("RPUSH", key, value...)
}

// LPUSH和RPUSH
func (m *RedisModule) setListPush(setType string, args ...interface{}) error {
	if setType != "LPUSH" && setType != "RPUSH" {
		return errors.New("Redis List Push Type Error,Must Be LPUSH or RPUSH")
	}
	conn, err := m.getConn()
	if err != nil {
		return err
	}
	defer conn.Close()

	_, retErr := conn.Do(setType, args...)
	if retErr != nil {
		log.Error("setList fail,reason:%v", retErr)
	}
	return retErr
}

func (m *RedisModule) setListJSONPush(setType string, key interface{}, value ...interface{}) error {
	args := []interface{}{key}
	for _, v := range value {
		jData, err := json.Marshal(v)
		if err != nil {
			return err
		}
		args = append(args, string(jData))
	}

	return m.setListPush(setType, args...)
}

// Lrange ...
func (m *RedisModule) LRangeList(key string, start, end int) ([]string, error) {
	conn, err := m.getConn()
	if err != nil {
		return nil, err
	}
	defer conn.Close()

	reply, err := conn.Do("lrange", key, start, end)
	if err != nil {
		log.Error("SetListJSONRpush fail,reason:%v", err)
		return nil, err
	}

	return redis.Strings(reply, err)
}

// 获取List的长度
func (m *RedisModule) GetListLen(key string) (int, error) {
	conn, err := m.getConn()
	if err != nil {
		return -1, err
	}
	defer conn.Close()

	reply, err := conn.Do("LLEN", key)
	if err != nil {
		log.Error("GetListLen fail,reason:%v", err)
		return -1, err
	}
	return redis.Int(reply, err)
}

// 弹出List最后条记录
func (m *RedisModule) RPOPListValue(key string) (string, error) {
	conn, err := m.getConn()
	if err != nil {
		return "", err
	}
	defer conn.Close()

	return redis.String(conn.Do("RPOP", key))
}

func (m *RedisModule) LTrimList(key string, start, end int) error {
	conn, err := m.getConn()
	if err != nil {
		return err
	}
	defer conn.Close()

	_, err = conn.Do("LTRIM", key, start, end)
	if err != nil {
		log.Error("LtrimListValue fail,reason:%v", err)
		return err
	}
	return nil
}

func (m *RedisModule) LRangeJSON(key string, start, stop int, data interface{}) error {
	b, err := m.LRange(key, start, stop)
	if err != nil {
		return err
	}
	err = json.Unmarshal(b, data)
	if err != nil {
		return err
	}
	return nil
}

func (m *RedisModule) LRange(key string, start, stop int) ([]byte, error) {
	conn, err := m.getConn()
	if err != nil {
		return nil, err
	}
	defer conn.Close()

	reply, err := conn.Do("LRANGE", key, start, stop)
	if err != nil {
		return nil, err
	}
	return makeListJson(reply.([]interface{}), false), nil
}

// 弹出list(消息队列)数据,数据放入out fromLeft表示是否从左侧弹出 block表示是否阻塞 timeout表示阻塞超时
func (m *RedisModule) ListPopJson(key string, fromLeft, block bool, timeout int, out interface{}) error {
	b, err := m.ListPop(key, fromLeft, block, timeout)
	if err != nil {
		return err
	}
	err = json.Unmarshal(b, out)
	if err != nil {
		return err
	}
	return nil
}

// 弹出list(消息队列)数据 fromLeft表示是否从左侧弹出 block表示是否阻塞 timeout表示阻塞超时
func (m *RedisModule) ListPop(key string, fromLeft, block bool, timeout int) ([]byte, error) {
	cmd := ""
	if fromLeft {
		if block {
			cmd = "BLPOP"
		} else {
			cmd = "LPOP"
		}
	} else {
		if block {
			cmd = "BRPOP"
		} else {
			cmd = "RPOP"
		}
	}

	conn, err := m.getConn()
	if err != nil {
		return nil, err
	}
	defer conn.Close()

	reply, err := conn.Do(cmd, key, timeout)
	if err != nil {
		return nil, err
	}

	if reply == nil {
		err = errors.New("ListPop key is not exist!")
		return nil, err
	}

	b, ok := reply.([]byte)
	if !ok {
		err = errors.New("ListPop redis data is error")
		//service.GetLogger().Printf(service.LEVER_ERROR, "GetString redis data is error")
		return nil, err
	}
	return b, nil
}

// 有序集合插入Json
func (m *RedisModule) ZADDInsertJson(key string, score float64, value interface{}) error {

	conn, err := m.getConn()
	if err != nil {
		return err
	}
	defer conn.Close()
	JsonValue, err := json.Marshal(value)
	if err != nil {
		return err
	}
	_, err = conn.Do("ZADD", key, score, JsonValue)
	if err != nil {
		log.Error("ZADDInsertJson fail,reason:%v", err)
		return err
	}
	return nil
}

// 有序集合插入
func (m *RedisModule) ZADDInsert(key string, score float64, Data interface{}) error {
	conn, err := m.getConn()
	if err != nil {
		return err
	}
	defer conn.Close()

	_, err = conn.Do("ZADD", key, score, Data)
	if err != nil {
		log.Error("ZADDInsert fail,reason:%v", err)
		return err
	}
	return nil
}

type ZSetDataWithScore struct {
	Data  json.RawMessage `json:"data"`
	Score float64         `json:"score"`
}

func (m *RedisModule) ZRangeJSON(key string, start, stop int, ascend bool, withScores bool, data interface{}) error {
	if withScores {
		if _, ok := data.(*[]ZSetDataWithScore); !ok {
			return errors.New("withScores must decode by []ZSetDataWithScore")
		}
	}

	b, err := m.ZRange(key, start, stop, ascend, withScores)
	if err != nil {
		return err
	}

	err = json.Unmarshal(b, data)
	if err != nil {
		return err
	}
	return nil
}

// 取有序set指定排名 ascend=true表示按升序遍历 否则按降序遍历
func (m *RedisModule) ZRange(key string, start, stop int, ascend bool, withScores bool) ([]byte, error) {
	conn, err := m.getConn()
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	cmd := "ZREVRANGE"
	if ascend {
		cmd = "ZRANGE"
	}
	var reply interface{}
	if withScores {
		reply, err = conn.Do(cmd, key, start, stop, "WITHSCORES")
	} else {
		reply, err = conn.Do(cmd, key, start, stop)
	}

	if err != nil {
		return nil, err
	}
	return makeListJson(reply.([]interface{}), withScores), nil
}

// 获取有序集合长度
func (m *RedisModule) Zcard(key string) (int, error) {
	conn, err := m.getConn()
	if err != nil {
		return 0, err
	}
	defer conn.Close()

	reply, err := conn.Do("ZCARD", key)
	if err != nil {
		return 0, err
	}
	return int(reply.(int64)), nil
}

// ["123","234"]
func makeListJson(redisReply []interface{}, withScores bool) []byte {
	var buf bytes.Buffer
	buf.WriteString("[")
	data := true
	for i, v := range redisReply {
		if i > 0 {
			buf.WriteString(",")
		}
		if !withScores {
			buf.WriteString(fmt.Sprintf("%s", v))
		} else {
			if data {
				buf.WriteString("{")
				buf.WriteString(`"data":`)
				buf.WriteString(fmt.Sprintf("%s", v))
			} else {
				buf.WriteString(`"score":`)
				buf.WriteString(fmt.Sprintf("%s", v))
				buf.WriteString("}")
			}
			data = !data
		}
	}
	buf.WriteString("]")
	return buf.Bytes()
}

func (m *RedisModule) ZRangeByScoreJSON(key string, start, stop float64, ascend bool, withScores bool, data interface{}) error {
	if withScores {
		if _, ok := data.(*[]ZSetDataWithScore); !ok {
			return errors.New("withScores must decode by []ZSetDataWithScore")
		}
	}

	b, err := m.ZRangeByScore(key, start, stop, ascend, withScores)
	if err != nil {
		return err
	}

	err = json.Unmarshal(b, data)
	if err != nil {
		return err
	}
	return nil
}

func (m *RedisModule) ZRangeByScore(key string, start, stop float64, ascend bool, withScores bool) ([]byte, error) {
	conn, err := m.getConn()
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	cmd := "ZREVRANGEBYSCORE"
	if ascend {
		cmd = "ZRANGEBYSCORE"
	}
	var reply interface{}
	if withScores {
		reply, err = conn.Do(cmd, key, start, stop, "WITHSCORES")
	} else {
		reply, err = conn.Do(cmd, key, start, stop)
	}
	if err != nil {
		return nil, err
	}
	return makeListJson(reply.([]interface{}), withScores), nil
}

// 获取指定member的排名
func (m *RedisModule) ZScore(key string, member interface{}) (float64, error) {
	conn, err := m.getConn()
	if err != nil {
		return -1, err
	}
	defer conn.Close()
	cmd := "ZSCORE"
	var reply interface{}
	reply, err = conn.Do(cmd, key, member)
	if err != nil {
		return -1, err
	}
	return redis.Float64(reply, err)
}

// 获取指定member的排名
func (m *RedisModule) ZRank(key string, member interface{}, ascend bool) (int, error) {
	conn, err := m.getConn()
	if err != nil {
		return -1, err
	}
	defer conn.Close()
	cmd := "ZREVRANK"
	if ascend {
		cmd = "ZRANK"
	}
	var reply interface{}
	reply, err = conn.Do(cmd, key, member)
	if err != nil {
		return -1, err
	}
	return redis.Int(reply, err)
}

func (m *RedisModule) ZREMRANGEBYSCORE(key string, start, stop interface{}) error {
	conn, err := m.getConn()
	if err != nil {
		return err
	}
	defer conn.Close()

	_, err = conn.Do("ZREMRANGEBYSCORE", key, start, stop)
	if err != nil {
		return err
	}
	return err
}

func (m *RedisModule) ZREM(key string, member interface{}) (int, error) {
	conn, err := m.getConn()
	if err != nil {
		return 0, err
	}
	defer conn.Close()

	reply, err := conn.Do("ZREM", key, member)
	return redis.Int(reply, err)
}

func (m *RedisModule) ZREMMulti(key string, member ...interface{}) (int, error) {
	conn, err := m.getConn()
	if err != nil {
		return 0, err
	}
	defer conn.Close()

	args := []interface{}{key}
	args = append(args, member...)
	reply, err := conn.Do("ZREM", args...)
	return redis.Int(reply, err)
}

func (m *RedisModule) HincrbyHashInt(redisKey, hashKey string, value int) error {
	if redisKey == "" || hashKey == "" {
		return errors.New("Key Is Empty")
	}
	conn, err := m.getConn()
	if err != nil {
		return err
	}
	defer conn.Close()

	_, retErr := conn.Do("HINCRBY", redisKey, hashKey, value)
	if retErr != nil {
		log.Error("HincrbyHashInt fail,reason:%v", retErr)
	}

	return retErr
}

func (m *RedisModule) EXPlREInsert(key string, TTl int) error {
	conn, err := m.getConn()
	if err != nil {
		return err
	}
	defer conn.Close()

	_, err = conn.Do("expire", key, TTl)
	if err != nil {
		log.Error("expire fail,reason:%v", err)
		return err
	}
	return nil
}

func (m *RedisModule) Zremrangebyrank(redisKey string, start, end interface{}) (int, error) {
	conn, err := m.getConn()
	if err != nil {
		return 0, err
	}
	defer conn.Close()

	reply, err := conn.Do("ZREMRANGEBYRANK", redisKey, start, end)
	return redis.Int(reply, err)
}

func (m *RedisModule) Keys(key string) ([]string, error) {
	conn, err := m.getConn()
	if err != nil {
		return nil, err
	}
	defer conn.Close()

	ret, err := conn.Do("KEYS", key)
	if err != nil {
		log.Error("KEYS fail, reason:%v", err)
		return nil, err
	}
	retList, ok := ret.([]interface{})
	if !ok {
		err = errors.New("Func[KEYS] Redis Data Error")
		return nil, err
	}

	strs := []string{}
	for _, val := range retList {
		strVal, ok := val.([]byte)
		if !ok {
			return nil, fmt.Errorf("value not string")
		}
		strs = append(strs, string(strVal))
	}
	return strs, nil
}

func (m *RedisModule) OnRelease() {
	if m.redisPool != nil {
		m.redisPool.Close()
	}
}
