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

// OnStart 在所属 Service 和父 Module 启动成功后启动当前子 Module。
func (target *ChildModule) OnStart(context.Context) error {
	fmt.Println("child module started")
	return nil
}

// OnStop 在父 Module 和所属 Service 停止前释放当前子 Module 的资源。
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

// OnStart 在 GameService 启动成功后、ChildModule 启动前建立根 Module 资源。
func (target *RootModule) OnStart(context.Context) error {
	fmt.Println("root module started")
	return nil
}

// OnStop 在 ChildModule 停止后释放根 Module 资源；此时 GameService 仍可使用。
func (target *RootModule) OnStop(context.Context) error {
	fmt.Println("root module stopped")
	return nil
}

// GameService 是整棵 Module 树的唯一所有者和调度边界。
type GameService struct{ service.Service }

// OnInit 把根 Module 接入 Service 生命周期。
func (target *GameService) OnInit() error { return target.AddModule(&RootModule{}) }

// OnStart 先建立 Service 级共享资源，然后框架才会依次启动根 Module 和子 Module。
func (target *GameService) OnStart(context.Context) error {
	fmt.Println("game service started")
	return nil
}

// OnStop 在全部 Module 停止后最后执行，适合关闭 Service 级共享资源。
func (target *GameService) OnStop(context.Context) error {
	fmt.Println("game service stopped")
	return nil
}

// init 只登记 Service；Module 由所属 Service 自己添加。
func init() { app.Setup(&GameService{}) }

// main 启动 Application。
func main() { app.Start() }
