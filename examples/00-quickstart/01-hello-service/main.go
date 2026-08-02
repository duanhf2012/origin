package main

import (
	"context"

	"github.com/duanhf2012/origin/v3/application"
	"github.com/duanhf2012/origin/v3/service"
)

var app = application.New()

// HelloService 是最小业务 Service：匿名嵌入 service.Service，只覆盖需要的回调。
type HelloService struct {
	service.Service
}

func (target *HelloService) OnInit() error {
	target.Logger().Info("hello service initialized")
	return nil
}

func (target *HelloService) OnStart(context.Context) error {
	target.Logger().Info("hello, Origin v3")
	return nil
}

func (target *HelloService) OnStop(context.Context) error {
	target.Logger().Info("hello service stopped")
	return nil
}

func init() {
	app.Setup(&HelloService{})
}

func main() {
	app.Start()
}
