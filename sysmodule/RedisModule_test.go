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

	module.SetRedisHash("rediskey", "hashkey", "1111")
	ret, err := module.GetRedisHashValueByKey("rediskey", "hashkey")
	fmt.Print(ret, err)
}
