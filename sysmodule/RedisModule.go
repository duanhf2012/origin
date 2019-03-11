package sysmodule

import (
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/duanhf2012/origin/service"
	"github.com/gomodule/redigo/redis"
)

//最大redis信道任务量
const (
	MAX_TASK_CHANNEL = 10240
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

type Func func()
type RedisModule struct {
	service.BaseModule
	redispool *redis.Pool
	redisTask chan Func
}

// ConfigRedis 服务器配置
type ConfigRedis struct {
	IP          string
	Port        string
	Password    string
	DbIndex     int
	MaxIdle     int //最大的空闲连接数，表示即使没有redis连接时依然可以保持N个空闲的连接，而不被清除，随时处于待命状态。
	MaxActive   int //最大的激活连接数，表示同时最多有N个连接
	IdleTimeout int //最大的空闲连接等待时间，超过此时间后，空闲连接将被关闭
}

func (slf *RedisModule) Init(redisCfg *ConfigRedis) {
	redisServer := redisCfg.IP + ":" + redisCfg.Port
	slf.redispool = &redis.Pool{
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
				service.GetLogger().Printf(service.LEVER_ERROR, "Connect redis fail reason:%v", err)
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
				service.GetLogger().Printf(service.LEVER_WARN, "Do PING fail reason:%v", err)
				return err
			}
			return err
		},
	}

	slf.redisTask = make(chan Func, MAX_TASK_CHANNEL)
	go slf.RunAnsyTask()
}

func (slf *RedisModule) RunAnsyTask() {
	for {
		task := <-slf.redisTask
		task()
	}
}

func (slf *RedisModule) GoTask(fc Func) error {
	if len(slf.redisTask) >= MAX_TASK_CHANNEL {
		service.GetLogger().Printf(service.LEVER_ERROR, "Redis task channel recover max.")
		return fmt.Errorf("Redis task channel recover max.")
	}

	slf.redisTask <- fc
	return nil
}

// GetConn ...
func (slf *RedisModule) getConn() (redis.Conn, error) {
	if slf.redispool == nil {
		service.GetLogger().Printf(service.LEVER_FATAL, "Not Init RedisModule")
		return nil, fmt.Errorf("Not Init RedisModule")
	}
	conn := slf.redispool.Get()
	if conn == nil {
		service.GetLogger().Printf(service.LEVER_ERROR, "Cannot get connection")
		return nil, fmt.Errorf("Cannot get connection")
	}

	if conn.Err() != nil {
		err := conn.Err()
		if err != nil {
			service.GetLogger().Printf(service.LEVER_WARN, "Get Conn have error,reason:%v", err)
		}
		conn.Close()
		return nil, err
	}
	return conn, nil
}

//TestPingRedis 测试连接Redis
func (slf *RedisModule) TestPingRedis() error {
	conn, err := slf.getConn()
	if err != nil {
		return err
	}
	defer conn.Close()

	err = slf.redispool.TestOnBorrow(conn, time.Now())
	if err != nil {
		service.GetLogger().Printf(service.LEVER_ERROR, "TestOnBorrow fail,reason:%v", err)
		return err
	}

	return nil
}

//SetRedisString redis添加string类型数据 无过期时间
//示例:SetRedisString("TestKey", "Hell World!")
func (slf *RedisModule) SetString(key, value string) (err error) {
	err = slf.setStringByExpire(key, value, "-1")

	return err
}

func (slf *RedisModule) GoSetString(key, value string, err *RetError) {
	slf.GoSetStringExpire(key, value, "-1", err)
}

//SetRedisExString redis添加string类型数据 有过期时间 ex过期时间,单位秒,必须是整数
//示例:SetRedisExString("TestKey", "Hell World!","60")
func (slf *RedisModule) SetStringExpire(key, value, expire string) (err error) {
	err = slf.setStringByExpire(key, value, expire)

	return err
}

func (slf *RedisModule) GoSetStringExpire(key, value string, expire string, err *RetError) error {
	if err != nil {
		err.resultChan = make(chan error, 1)
	}

	fun := func() {
		if err != nil {
			ret := slf.setStringByExpire(key, value, expire)
			err.resultChan <- ret
		} else {
			slf.setStringByExpire(key, value, expire)
		}
	}

	slf.GoTask(fun)

	return nil
}

//SetRedisStringJSON redis添加JSON数据 无过期时间
//示例:SetRedisStringJSON("AAAABTEST1", eagleconfig.Cfg)
func (slf *RedisModule) SetStringJSON(key string, val interface{}) (err error) {
	err = slf.SetStringJSONExpire(key, val, "-1")

	return err
}

func (slf *RedisModule) GoSetStringJSON(key string, val interface{}, err *RetError) {
	slf.GoSetStringJSONExpire(key, val, "-1", err)
}

//SetRedisExStringJSON redis添加JSON数据 有过期时间 ex过期时间,单位秒,必须是整数
//示例:SetRedisStringJSON("AAAABTEST1", eagleconfig.Cfg,"60")
func (slf *RedisModule) SetStringJSONExpire(key string, val interface{}, expire string) (err error) {
	if temp, err := json.Marshal(val); err == nil {
		err = slf.setStringByExpire(key, string(temp), expire)
	}

	return err
}

func (slf *RedisModule) GoSetStringJSONExpire(key string, val interface{}, expire string, retErr *RetError) error {
	temp, err := json.Marshal(val)
	if err == nil {
		slf.GoSetStringExpire(key, string(temp), expire, retErr)
		return nil
	} else {
		service.GetLogger().Printf(service.LEVER_ERROR, "GoSetStringJSONExpire fail,reason:%v", err)
		retErr.resultChan <- err
	}
	return err
}

func (slf *RedisModule) setStringByExpire(key, value, expire string) error {
	if key == "" {
		return errors.New("Key Is Empty")
	}

	conn, err := slf.getConn()
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
		service.GetLogger().Printf(service.LEVER_ERROR, "setStringByExpire fail,reason:%v", retErr)
		return retErr
	}

	_, ok := ret.(string)
	if !ok {
		retErr = errors.New("setStringByExpire redis data is error")
		service.GetLogger().Printf(service.LEVER_ERROR, "setStringByExpire redis data is error")
		return retErr
	}

	return nil
}

//SetMuchRedisString redis添加多条string类型数据
//示例:SetMuchRedisString(map[string]string{"Test1": "C语言", "Test2": "Go语言", "Test3": "Python", "Test4": "C++"})
func (slf *RedisModule) SetMuchString(mapInfo map[string]string) (err error) {
	err = slf.setMuchStringByExpire(mapInfo, "-1")

	return
}

func (slf *RedisModule) GoSetMuchString(mapInfo map[string]string, retErr *RetError) {
	slf.GoSetMuchStringExpire(mapInfo, "-1", retErr)
}

//SetMuchRedisStringSameEx redis添加多条string类型数据 具有相同的过期时间 ex过期时间 整数
//示例:SetMuchRedisStringSameEx(map[string]string{"Test1": "C语言", "Test2": "Go语言", "Test3": "Python", "Test4": "C++"},"300")
func (slf *RedisModule) SetMuchStringExpire(mapInfo map[string]string, ex string) (err error) {
	err = slf.setMuchStringByExpire(mapInfo, ex)
	return
}

func (slf *RedisModule) GoSetMuchStringExpire(mapInfo map[string]string, expire string, err *RetError) {

	if err != nil {
		err.resultChan = make(chan error, 1)
	}

	fun := func() {
		if err != nil {
			ret := slf.setMuchStringByExpire(mapInfo, expire)
			err.resultChan <- ret
		} else {
			slf.setMuchStringByExpire(mapInfo, expire)
		}
	}

	slf.GoTask(fun)
}

func (slf *RedisModule) setMuchStringByExpire(mapInfo map[string]string, expire string) error {
	if len(mapInfo) <= 0 {
		service.GetLogger().Printf(service.LEVER_ERROR, "setMuchStringByExpire  Info Is Empty")
		return errors.New("setMuchStringByExpire  Info Is Empty")
	}

	conn, err := slf.getConn()
	if err != nil {
		return err
	}
	defer conn.Close()

	// 开始Send数据
	conn.Send("MULTI")
	for key, val := range mapInfo {
		if expire == "-1" {
			conn.Send("SET", key, val)
		} else {
			conn.Send("SET", key, val, "EX", expire)
		}
	}
	// 执行命令
	_, err = conn.Do("EXEC")

	if err != nil {
		service.GetLogger().Printf(service.LEVER_ERROR, "setMuchStringByExpire fail,reason:%v", err)
	}

	return err
}

//GetRedisString redis获取string类型数据
//示例:GetRedisString("TestKey")
func (slf *RedisModule) GetString(key string) (string, error) {
	conn, err := slf.getConn()
	if err != nil {
		return "", err
	}
	defer conn.Close()

	ret, err := conn.Do("GET", key)
	if err != nil {
		service.GetLogger().Printf(service.LEVER_ERROR, "GetString fail,reason:%v", err)
		return "", err
	}

	if ret == nil {
		err = errors.New("GetString key is not exist!")
		return "", err
	}

	str, ok := ret.([]byte)
	if !ok {
		err = errors.New("GetString redis data is error")
		service.GetLogger().Printf(service.LEVER_ERROR, "GetString redis data is error")
		return "", err
	}

	return string(str), nil
}

//GetRedisStringJSON redis获取string类型数据的Json
//示例:GetRedisString("TestKey")
func (slf *RedisModule) GetStringJSON(key string, st interface{}) error {
	conn, err := slf.getConn()
	if err != nil {
		return err
	}
	defer conn.Close()

	ret, err := conn.Do("GET", key)
	if err != nil {
		service.GetLogger().Printf(service.LEVER_ERROR, "GetStringJSON fail,reason:%v", err)
		return err
	}

	if ret == nil {
		err = errors.New("GetStringJSON Key is not exist")
		return err
	}

	str, ok := ret.([]byte)
	if !ok {
		err = errors.New("GetStringJSON redis data is error!")
		service.GetLogger().Printf(service.LEVER_ERROR, "GetStringJSON redis data is error!")
		return err
	}

	if err = json.Unmarshal(str, st); err != nil {
		service.GetLogger().Printf(service.LEVER_ERROR, "GetStringJSON fail json.Unmarshal is error,reason:%v", err)
		return err
	}

	return nil
}

//GetMuchRedisString redis获取string类型数据
//Pipeline实现的原理是队列，而队列的原理是先进先出
//示例:GetMuchRedisString(&[]string{"AAAABTEST1", "AAAABTEST2"})
func (slf *RedisModule) GetMuchString(keys []string) (retMap map[string]string, err error) {

	if len(keys) <= 0 {
		err = errors.New("Func[GetMuchRedisString] Keys Is Empty")
		return
	}
	conn, err := slf.getConn()
	if err != nil {
		return nil, err
	}
	defer conn.Close()

	// 开始Send数据
	err = conn.Send("MULTI")
	if err != nil {
		service.GetLogger().Printf(service.LEVER_ERROR, "GetMuchString fail %v", err)
		return nil, err
	}
	for _, val := range keys {
		conn.Send("GET", val)
	}
	// 执行命令
	ret, err := conn.Do("EXEC")

	if err != nil {
		service.GetLogger().Printf(service.LEVER_ERROR, "GetMuchString fail %v", err)
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

//GetMuchRedisStringJSON redis获取string类型数据Json
//Pipeline实现的原理是队列，而队列的原理是先进先出
//示例:temp := make(map[string]interface{})
//temp["AAAABTEST1"] = &eagleconfig.ServerConfig{}
//temp["AAAABTEST2"] = &eagleconfig.ServerConfig{}
//GetMuchRedisStringJSON(&temp)
func (slf *RedisModule) GetMuchStringJSON(keys map[string]interface{}) error {
	if len(keys) <= 0 {
		err := errors.New("GetMuchStringJSON fail key is empty")
		service.GetLogger().Printf(service.LEVER_ERROR, "GetMuchStringJSON fail key is empty")
		return err
	}
	conn, err := slf.getConn()
	if err != nil {
		return err
	}
	defer conn.Close()

	// 开始Send数据
	conn.Send("MULTI")

	var tempKeys []string
	for key := range keys {
		tempKeys = append(tempKeys, key)
		conn.Send("GET", key)
	}
	// 执行命令
	ret, err := conn.Do("EXEC")

	if err != nil {
		service.GetLogger().Printf(service.LEVER_ERROR, "GetMuchStringJSON fail, reason:%v", err)
		return err
	}

	retList, ok := ret.([]interface{})
	if !ok {
		err = errors.New("Func[GetMuchRedisStringJSON] Redis Data Error")
		return err
	}

	fmt.Println(tempKeys)
	for index, val := range retList {
		strVal, ok := val.([]byte)

		if !ok {
			continue
		}

		err = json.Unmarshal(strVal, keys[tempKeys[index]])
		if err != nil {
			service.GetLogger().Printf(service.LEVER_ERROR, "GetMuchStringJSON Unmarshal fail, reason:%v", err)
			return err
		}
	}

	return nil
}

//DelRedisString redis删除string类型数据
//示例:DelRedisString("AAAABTEST1")
func (slf *RedisModule) DelString(key string) error {
	conn, err := slf.getConn()
	if err != nil {
		return err
	}
	defer conn.Close()

	ret, err := conn.Do("DEL", key)
	if err != nil {
		service.GetLogger().Printf(service.LEVER_ERROR, "DelString fail, reason:%v", err)
		return err
	}

	retValue, ok := ret.(int64)
	if !ok {
		err = errors.New("Func[DelRedisString] Redis Data Error")
		return err
	}

	if retValue == 0 {
		err = errors.New("Func[DelRedisString] Delete Key Fail")
		return err
	}

	return nil
}

func (slf *RedisModule) GoDelString(key string, err *RetError) {
	if err != nil {
		err.resultChan = make(chan error, 1)
	}

	fun := func() {
		if err != nil {
			ret := slf.DelString(key)
			err.resultChan <- ret
		} else {
			slf.DelString(key)
		}
	}

	slf.GoTask(fun)
}

func (slf *RedisModule) GoDelMuchString(keys []string, retMapString *RetMapString) {
	if retMapString != nil {
		retMapString.resultChan = make(chan ErrorMapStringBool, 1)
	}

	fun := func() {
		if retMapString != nil {
			var retchan ErrorMapStringBool
			retchan.resultMapStringBool, retchan.resultError = slf.DelMuchString(keys)
			retMapString.resultChan <- retchan
		} else {
			slf.DelMuchString(keys)
		}
	}

	slf.GoTask(fun)
}

//DelMuchRedisString redis删除string类型数据
//示例:DelMuchRedisString([]string{"AAAABTEST1",""AAAABTEST2})
func (slf *RedisModule) DelMuchString(keys []string) (map[string]bool, error) {
	if len(keys) <= 0 {
		err := errors.New("Func[DelMuchRedisString] Keys Is Empty")
		return nil, err
	}

	conn, err := slf.getConn()
	if err != nil {
		return nil, err
	}
	defer conn.Close()

	// 开始Send数据
	conn.Send("MULTI")
	for _, val := range keys {
		conn.Send("DEL", val)
	}
	// 执行命令
	ret, err := conn.Do("EXEC")

	if err != nil {
		service.GetLogger().Printf(service.LEVER_ERROR, "DelMuchString fail,reason:%v", err)
		return nil, err
	}

	retList, ok := ret.([]interface{})
	if !ok {
		err = errors.New("Func[DelMuchRedisString] Redis Data Error")
		return nil, err
	}

	retMap := map[string]bool{}
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

//SetRedisHash ...
//如果 hsahKey 是哈希表中的一个新建域，并且值设置成功，返回 1
//如果哈希表中域 hsahKey 已经存在且旧值已被新值覆盖，返回 0
func (slf *RedisModule) SetHash(redisKey, hashKey, value string) error {
	if redisKey == "" || hashKey == "" {
		return errors.New("Key Is Empty")
	}
	conn, err := slf.getConn()
	if err != nil {
		return err
	}
	defer conn.Close()

	_, retErr := conn.Do("HSET", redisKey, hashKey, value)
	if retErr != nil {
		service.GetLogger().Printf(service.LEVER_ERROR, "SetHash fail,reason:%v", retErr)
	}

	return retErr
}

func (slf *RedisModule) GoSetHash(redisKey, hashKey, value string, err *RetError) {
	if err != nil {
		err.resultChan = make(chan error, 1)
	}
	fun := func() {
		if err != nil {
			err.resultChan <- slf.SetHash(redisKey, hashKey, value)
		} else {
			slf.SetHash(redisKey, hashKey, value)
		}
	}

	slf.GoTask(fun)
}

//GetRedisAllHashJSON ...
func (slf *RedisModule) GetAllHashJSON(redisKey string) (map[string]string, error) {
	if redisKey == "" {
		return nil, errors.New("Key Is Empty")
	}
	conn, err := slf.getConn()
	if err != nil {
		return nil, err
	}
	defer conn.Close()

	value, err := redis.Values(conn.Do("HGETALL", redisKey))
	if err != nil {
		service.GetLogger().Printf(service.LEVER_ERROR, "GetAllHashJSON fail,reason:%v", err)
		return nil, err
	}

	return redis.StringMap(value, err)
}

//GetRedisHashValueByKey ...
func (slf *RedisModule) GetHashValueByKey(redisKey string, fieldKey string) (string, error) {

	if redisKey == "" || fieldKey == "" {
		service.GetLogger().Printf(service.LEVER_ERROR, "GetHashValueByKey key is empty!")
		return "", errors.New("Key Is Empty")
	}
	conn, err := slf.getConn()
	if err != nil {
		return "", err
	}
	defer conn.Close()

	value, err := conn.Do("HGET", redisKey, fieldKey)
	if err != nil {
		service.GetLogger().Printf(service.LEVER_ERROR, "GetHashValueByKey fail,reason:%v", err)
		return "", err
	}
	if value == nil {
		return "", errors.New("Reids Get Hash nil")
	}

	str, ok := value.([]byte)
	if !ok {
		err = errors.New("Func[GetRedisHashValueByKey] Redis Data Error")
		return "", err
	}

	return string(str), nil
}

//SetRedisHashJSON ...
func (slf *RedisModule) SetHashJSON(redisKey, hsahKey string, value interface{}) error {
	temp, err := json.Marshal(value)
	if err == nil {
		err = slf.SetHash(redisKey, hsahKey, string(temp))
	}

	return err
}

func (slf *RedisModule) GoSetHashJSON(redisKey, hsahKey string, value interface{}, err *RetError) {
	temp, retErr := json.Marshal(value)
	if retErr == nil {
		slf.GoSetHash(redisKey, hsahKey, string(temp), err)
	} else {
		if err != nil {
			err.resultChan = make(chan error, 1)
			err.resultChan <- retErr
		}
	}
}

//SetMuchRedisHashJSON ... value : hashkey -> value
func (slf *RedisModule) SetMuchHashJSON(redisKey string, value map[string][]interface{}) error {
	if len(value) <= 0 {
		err := errors.New("Func[SetMuchRedisHashJSON] value Is Empty")
		return err
	}

	conn, err := slf.getConn()
	if err != nil {
		return err
	}
	defer conn.Close()

	// 开始Send数据
	conn.Send("MULTI")
	for symbol, val := range value {
		temp, err := json.Marshal(val)
		if err == nil {
			conn.Do("HSET", redisKey, symbol, temp)
		}
	}
	// 执行命令
	_, err = conn.Do("EXEC")
	if err != nil {
		service.GetLogger().Printf(service.LEVER_ERROR, "SetMuchHashJSON fail,reason:%v", err)
	}
	return err
}

func (slf *RedisModule) GoSetMuchHashJSON(redisKey string, value map[string][]interface{}, err *RetError) {
	if err != nil {
		err.resultChan = make(chan error, 1)
	}

	fun := func() {
		if err != nil {
			err.resultChan <- slf.SetMuchHashJSON(redisKey, value)
		} else {
			slf.SetMuchHashJSON(redisKey, value)
		}
	}

	slf.GoTask(fun)
}

//GoDelRedisHash
func (slf *RedisModule) GoDelHash(redisKey string, hsahKey string, err *RetError) {
	tempHashKey := []string{hsahKey}
	slf.GoDelMuchHash(redisKey, tempHashKey, err)
}

//DelRedisHash ...
func (slf *RedisModule) DelHash(redisKey string, hsahKey string) error {
	tempHashKey := []string{hsahKey}

	err := slf.DelMuchHash(redisKey, tempHashKey)

	return err
}

//GoDelMuchRedisHash
func (slf *RedisModule) GoDelMuchHash(redisKey string, hsahKey []string, err *RetError) {
	if err != nil {
		err.resultChan = make(chan error, 1)
	}

	fun := func() {
		if err != nil {
			err.resultChan <- slf.DelMuchHash(redisKey, hsahKey)
		} else {
			slf.DelMuchHash(redisKey, hsahKey)
		}
	}

	slf.GoTask(fun)
}

//DelMuchRedisHash ...
func (slf *RedisModule) DelMuchHash(redisKey string, hsahKey []string) error {
	if redisKey == "" || len(hsahKey) <= 0 {
		return errors.New("Key Is Empty")
	}
	conn, err := slf.getConn()
	if err != nil {
		return err
	}
	defer conn.Close()

	arg := []interface{}{redisKey}
	for _, k := range hsahKey {
		arg = append(arg, k)
	}

	_, retErr := conn.Do("HDEL", arg...)
	if retErr != nil {
		service.GetLogger().Printf(service.LEVER_ERROR, "DelMuchHash fail,reason:%v", retErr)
	}
	return retErr
}

//gosetRedisList
func (slf *RedisModule) gosetList(key string, value []string, setType string, err *RetError) {
	if err != nil {
		err.resultChan = make(chan error, 1)
	}

	fun := func() {
		if err != nil {
			err.resultChan <- slf.setList(key, value, setType)
		} else {
			slf.setList(key, value, setType)
		}
	}

	slf.GoTask(fun)
}

//LPUSH和RPUSH
func (slf *RedisModule) setList(key string, value []string, setType string) error {
	if key == "" {
		return errors.New("Key Is Empty")
	}
	if setType != "LPUSH" && setType != "RPUSH" {
		return errors.New("Redis List Push Type Error,Must Be LPUSH or RPUSH")
	}
	conn, err := slf.getConn()
	if err != nil {
		return err
	}
	defer conn.Close()

	arg := []interface{}{key}
	for _, k := range value {
		arg = append(arg, k)
	}
	_, retErr := conn.Do(setType, arg...)
	if retErr != nil {
		service.GetLogger().Printf(service.LEVER_ERROR, "setList fail,reason:%v", retErr)
	}
	return retErr
}

//SetRedisListLpush ...
func (slf *RedisModule) SetListLpush(key, value string) error {
	tempVal := []string{value}
	err := slf.setList(key, tempVal, "LPUSH")
	return err
}

//GoSetRedisListLpush...
func (slf *RedisModule) GoSetListLpush(key, value string, err *RetError) {
	tempVal := []string{value}
	slf.gosetList(key, tempVal, "LPUSH", err)
}

//SetMuchRedisListLpush ...
func (slf *RedisModule) SetMuchListLpush(key string, value []string) error {
	return slf.setList(key, value, "LPUSH")
}

//GoSetMuchListLpush ...
func (slf *RedisModule) GoSetMuchListLpush(key string, value []string, err *RetError) {
	slf.gosetList(key, value, "LPUSH", err)
}

//SetRedisListJSONLpush ...
func (slf *RedisModule) SetListJSONLpush(key string, value interface{}) error {
	temp, err := json.Marshal(value)
	if err == nil {
		tempVal := []string{string(temp)}
		err = slf.setList(key, tempVal, "LPUSH")
	} else {
		service.GetLogger().Printf(service.LEVER_ERROR, "SetListJSONLpush fail,reason:%v", err)
	}
	return err
}

//GoSetRedisListJSONLpush ...
func (slf *RedisModule) GoSetListJSONLpush(key string, value interface{}, retErr *RetError) {
	temp, err := json.Marshal(value)
	if err == nil {
		tempVal := []string{string(temp)}
		slf.gosetList(key, tempVal, "LPUSH", retErr)
	} else {
		if err != nil {
			retErr.resultChan = make(chan error, 1)
			retErr.resultChan <- err
		}
	}
}

//GoSetMuchListJSONLpush ...
func (slf *RedisModule) GoSetMuchListJSONLpush(key string, value []interface{}, retErr *RetError) {
	tempVal := []string{}
	for _, val := range value {
		if temp, err := json.Marshal(val); err == nil {
			tempVal = append(tempVal, string(temp))
		}
	}

	slf.gosetList(key, tempVal, "LPUSH", retErr)
}

//SetMuchListJSONLpush ...
func (slf *RedisModule) SetMuchListJSONLpush(key string, value []interface{}) error {
	tempVal := []string{}
	for _, val := range value {
		temp, err := json.Marshal(val)
		if err != nil {
			service.GetLogger().Printf(service.LEVER_ERROR, "SetMuchListJSONLpush fail,reason:%v", err)
			return err
		}

		if err == nil {
			tempVal = append(tempVal, string(temp))
		}
	}

	return slf.setList(key, tempVal, "LPUSH")
}

//GoSetListRpush ...
func (slf *RedisModule) GoSetListRpush(key, value string, retErr *RetError) {
	tempVal := []string{value}
	slf.gosetList(key, tempVal, "RPUSH", retErr)
}

//SetListRpush ...
func (slf *RedisModule) SetListRpush(key, value string) error {
	tempVal := []string{value}
	err := slf.setList(key, tempVal, "RPUSH")

	return err
}

//SetMuchRedisListRpush ...
func (slf *RedisModule) SetMuchListRpush(key string, value []string) error {
	return slf.setList(key, value, "RPUSH")
}

//GoSetMuchRedisListRpush ...
func (slf *RedisModule) GoSetMuchListRpush(key string, value []string, retErr *RetError) {
	slf.gosetList(key, value, "RPUSH", retErr)
}

//SetRedisListJSONRpush ...
func (slf *RedisModule) SetListJSONRpush(key string, value interface{}) error {
	temp, err := json.Marshal(value)
	if err == nil {
		tempVal := []string{string(temp)}
		err = slf.setList(key, tempVal, "RPUSH")
	} else {
		service.GetLogger().Printf(service.LEVER_ERROR, "SetListJSONRpush fail,reason:%v", err)
	}

	return err
}

//GoSetRedisListJSONRpush ...
func (slf *RedisModule) GoSetListJSONRpush(key string, value interface{}, retErr *RetError) {
	temp, err := json.Marshal(value)
	if err == nil {
		tempVal := []string{string(temp)}
		slf.gosetList(key, tempVal, "RPUSH", retErr)
	} else {
		if err != nil {
			retErr.resultChan = make(chan error, 1)
			retErr.resultChan <- err
		}
	}
}

//SetMuchRedisListJSONRpush ...
func (slf *RedisModule) SetMuchListJSONRpush(key string, value []interface{}) error {
	tempVal := []string{}
	for _, val := range value {
		if temp, err := json.Marshal(val); err == nil {
			tempVal = append(tempVal, string(temp))
		}
	}

	return slf.setList(key, tempVal, "RPUSH")
}

//GoSetMuchRedisListJSONRpush ...
func (slf *RedisModule) GoSetMuchListJSONRpush(key string, value []interface{}, retErr *RetError) {
	tempVal := []string{}
	for _, val := range value {
		if temp, err := json.Marshal(val); err == nil {
			tempVal = append(tempVal, string(temp))
		}
	}

	slf.gosetList(key, tempVal, "RPUSH", retErr)
}

// Lrange ...
func (slf *RedisModule) Lrange(key string, start, end int) ([]string, error) {
	conn, err := slf.getConn()
	if err != nil {
		return nil, err
	}
	defer conn.Close()

	reply, err := conn.Do("lrange", key, start, end)
	if err != nil {
		service.GetLogger().Printf(service.LEVER_ERROR, "SetListJSONRpush fail,reason:%v", err)
		return nil, err
	}

	return redis.Strings(reply, err)
}
