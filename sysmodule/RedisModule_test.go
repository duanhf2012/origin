package sysmodule

import (
	"fmt"
	"testing"
	"time"
)

type CTestJson struct {
	A string
	B string
}

func TestRedisModule(t *testing.T) {

	var cfg ConfigRedis
	var redsmodule RedisModule

	cfg.IP = "127.0.0.1"
	cfg.Password = ""
	cfg.Port = "6379"
	cfg.IdleTimeout = 2
	cfg.MaxActive = 3
	cfg.MaxIdle = 3
	cfg.DbIndex = 15
	redsmodule.Init(&cfg)

	//GoSetString
	var retErr RetError
	redsmodule.GoSetString("testkey", "testvalue", &retErr)
	ret1, err1 := redsmodule.GetString("testkey")

	//GoSetStringExpire
	redsmodule.GoSetStringExpire("setstring_key", "value11", "1", &retErr)
	time.Sleep(time.Second * 2)
	ret2, err2 := redsmodule.GetString("setstring_key")
	fmt.Print(ret1, err1, retErr.Get(), "\n")
	fmt.Print(ret2, err2, "\n")

	//GoSetStringJSON
	var testjson CTestJson
	testjson.A = "A value"
	testjson.B = "B value"

	var retErr2 RetError
	redsmodule.GoSetStringJSON("setstring_key_json", &testjson, &retErr)

	//GoSetStringJSONExpire
	redsmodule.GoSetStringJSONExpire("setstring_key_json_expire", &testjson, "10", &retErr2)
	fmt.Print(retErr.Get(), "\n")
	fmt.Print(retErr2.Get(), "\n")
	var testjson2 CTestJson
	err := redsmodule.GetStringJSON("setstring_key_json", &testjson2)
	fmt.Print(err, "\n", testjson2, "\n")

	//GoSetMuchString/GoSetMuchStringExpire
	mapInfo := make(map[string]string)
	mapInfo["A"] = "A_value"
	mapInfo["B"] = "B_value"
	mapInfo["C"] = "C_value"
	redsmodule.GoSetMuchString(mapInfo, &retErr)
	redsmodule.GoSetMuchStringExpire(mapInfo, "100", &retErr)
	var keylist []string
	keylist = append(keylist, "A", "C")
	mapInfo, _ = redsmodule.GetMuchString(keylist)
	fmt.Print(retErr.Get(), "\n", mapInfo, "\n")

	//GoDelString
	redsmodule.GoDelString("setstring_key_json", &retErr)
	fmt.Print(retErr.Get(), "\n")

	var retMapStr RetMapString
	redsmodule.GoDelMuchString(keylist, &retMapStr)
	err2, mapstr := retMapStr.Get()
	fmt.Print(err2, "\n", mapstr, "\n")

	//GoSetHash
	redsmodule.GoSetHash("rediskey", "hashkey", "1111", &retErr)
	ret, err := redsmodule.GetHashValueByKey("rediskey", "hashkey")
	fmt.Print(retErr.Get(), ret, err)

	//GoSetHashJSON
	redsmodule.GoSetHashJSON("rediskey", "hashkey2", &testjson, &retErr)
	fmt.Print(retErr.Get(), "\n")

	//SetMuchHashJSON
	var mapHashJson map[string][]interface{}
	var listInterface []interface{}
	mapHashJson = make(map[string][]interface{})
	listInterface = append(listInterface, testjson, testjson)

	mapHashJson["key3"] = listInterface
	mapHashJson["key4"] = listInterface

	redsmodule.GoSetMuchHashJSON("rediskey", mapHashJson, &retErr)
	fmt.Print(retErr.Get(), "\n")

	//GoDelHash
	redsmodule.GoDelHash("rediskey", "hashkey", &retErr)
	fmt.Print(retErr.Get(), "\n")

	//GoDelMuchHash
	var keys []string
	keys = append(keys, "hashkey", "hashkey2")

	redsmodule.GoDelMuchHash("rediskey", keys, &retErr)
	fmt.Print(retErr.Get(), "\n")

	//GoSetListLpush
	redsmodule.GoSetListLpush("setlistKey", "list1", &retErr)
	fmt.Print(retErr.Get(), "\n")
	redsmodule.GoSetListLpush("setlistKey", "list2", &retErr)
	fmt.Print(retErr.Get(), "\n")
	redsmodule.GoSetListLpush("setlistKey", "list3", &retErr)
	fmt.Print(retErr.Get(), "\n")
}
