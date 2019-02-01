package sysmodule

import (
	"fmt"
	"testing"
)

func TestRedisModule(t *testing.T) {

	var cfg ConfigRedis
	var module RedisModule

	cfg.IP = "192.168.0.5"
	cfg.Password = ""
	cfg.Port = "6379"
	cfg.IdleTimeout = 2
	cfg.MaxActive = 3
	cfg.MaxIdle = 3
	cfg.DbIndex = 15
	module.Init(&cfg)

	var retErr RetError
	module.GoSetString("testkey", "testvalue", &retErr)
	ret1, err1 := module.GetRedisString("testkey")
	fmt.Print(ret1, err1, retErr.Get())

	module.SetRedisHash("rediskey", "hashkey", "1111")
	var mapTest map[string]string
	mapTest = make(map[string]string)
	ret, err := module.GetRedisHashValueByKey("rediskey", "hashkey")

	fmt.Print(mapTest)
	fmt.Print(ret, err)
}
