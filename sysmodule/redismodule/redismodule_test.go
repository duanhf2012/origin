package redismodule

import (
	"fmt"
	"testing"
)

func Test_Example(t *testing.T) {
	//1.构建RedisModule对象
	var module RedisModule
	var redisCfg ConfigRedis
	redisCfg.DbIndex = 0         //数据库索引
	redisCfg.IdleTimeout = 1000 //最大的空闲连接等待时间，超过此时间后，空闲连接将被关闭
	redisCfg.MaxIdle = 100      //最大的空闲连接数，表示即使没有redis连接时依然可以保持N个空闲的连接，而不被清除，随时处于待命状态。
	redisCfg.IdleTimeout = 100  //最大的空闲连接等待时间，超过此时间后，空闲连接将被关闭
	redisCfg.IP = "127.0.0.1"   //redis服务器IP
	redisCfg.Port = 6379      //redis服务器端口
	//2.初始化redis配置
	module.Init(&redisCfg)

	//3.string操作
	//普通操作
	module.SetString("key1","value1")
	module.SetString(1000,2*1000)
	module.SetStringExpire("key2","value2","60")

	//支持直接存储struct数据(json string)
	jsonData := struct{
		A string
		B string
	}{"Aaaa","Bbbb"}
	module.SetStringJSON("key3",&jsonData)

	// struct 2 redis hash
	a := struct{
		A string `redis:"a"`
		B int	`redis:"b"`
	}{
		"ccc",
		1,
	}
	module.HSetStruct("skey", &a)
	a.A = "xx"
	module.HGetStruct("skey", &a)
	if a.A == "xx"{
		fmt.Println("struct 2 hash failed.")
	}


	//支持存储map数据结构，map中的key与value与redis的key,value对象
	mapData := map[interface{}]interface{}{}
	mapData["key4"] = "value4"
	mapData["key5"] = "value5"
	mapData[1] = 4
	module.SetStringMap(mapData)
	//读取字符串
	result,err := module.GetString(1)
	fmt.Print("result:",result,err)
	//读取json
	jsonData.A = ""
	jsonData.B = ""
	module.GetStringJSON("key3",&jsonData)
	//读取map
	mapDataNew,merr := module.GetStringMap([]string{"key1","key2","key3"})
	fmt.Print(mapDataNew,merr)
	//是否存在ExistsKey
	bhas,kerr := module.ExistsKey(1)
	fmt.Print(bhas,kerr)
	//删除key列表，返回删除结果信息
	mapResult,rerr := module.DelStringKeyList([]interface{}{"key3","key9"})
	fmt.Print(mapResult,rerr)

	//4.Hash
	//HSET
	err = module.SetHash("rediskey","field",33)
	fmt.Print(err)
	//向Hash的key中存储map结构，map中的key与value分别对应哈希字段与值
	module.SetHashMapJSON("rediskey",mapData)
	//HGETALL获取所有的字段值，并以map[string]string格式返回
	hashRet,herr := module.GetAllHashJSON("rediskey")
	fmt.Print(hashRet,herr)
	//获取HASH值
	hashVal,verr := module.GetHash("rediskey","field")
	fmt.Print(hashVal,verr)
	//支持获取多个字段值
	hashRetList,hasherr := module.GetMuchHash("rediskey","field1","field2")
	fmt.Print(hashRetList,hasherr)
	//删除hashkey，支持多个字段
	module.DelHash("rediskey","field1","field2")

	//5.List
	//LPush
	module.LPushList("listkey",1000,2000)
	lData := struct{
		A string
		B string
	}{"Aaaa","Bbbb"}
	//LPush 结构体
	module.LPushListJSON("listkey",&lData,&lData)
	//RPush同上
	//获取List长度
	listLen,_:=module.GetListLen("listkey")
	fmt.Print(listLen)
	//RPop
	popValue,_:=module.RPOPListValue("listkey")
	fmt.Print(popValue)
}