// 本示例展示 Service、根 Module 与子 Module 的生命周期树。
package main

import (
	"context"
	"fmt"

	"github.com/duanhf2012/origin/v3/application"
	"github.com/duanhf2012/origin/v3/service"
)

// app 是当前示例唯一的 Application。
var app = application.New()

// ChildModule 依赖 RootModule，因此后启动、先停止。
type ChildModule struct{ service.Module }

func (target *ChildModule) OnStart(context.Context) error {
	fmt.Println("child module started")
	return nil
}

func (target *ChildModule) OnStop(context.Context) error {
	fmt.Println("child module stopped")
	return nil
}

// RootModule 在自己的 OnInit 中添加子 Module。
type RootModule struct{ service.Module }

// OnInit 构造父子 Module 层级；运行后不能再动态修改这棵树。
func (target *RootModule) OnInit() error {
	return target.AddModule(&ChildModule{})
}

func (target *RootModule) OnStart(context.Context) error {
	fmt.Println("root module started")
	return nil
}

func (target *RootModule) OnStop(context.Context) error {
	fmt.Println("root module stopped")
	return nil
}

// GameService 是整棵 Module 树的唯一所有者和调度边界。
type GameService struct{ service.Service }

// OnInit 把根 Module 接入 Service 生命周期。
func (target *GameService) OnInit() error { return target.AddModule(&RootModule{}) }

// init 只登记 Service；Module 由所属 Service 自己添加。
func init() { app.Setup(&GameService{}) }

// main 启动 Application。
func main() { app.Start() }
