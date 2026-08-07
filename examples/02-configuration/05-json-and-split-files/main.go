// 本示例展示 JSON/YAML 混用、递归目录扫描，以及一个 Node 一个配置文件。
package main

import (
	"context"
	"fmt"

	"github.com/duanhf2012/origin/v3/application"
	"github.com/duanhf2012/origin/v3/service"
)

// app 只接收配置目录；目录内文件数量和子目录结构不进入业务代码。
var app = application.New()

// splitConfig 是两个 Node 共享的业务配置结构。
type splitConfig struct {
	Welcome string `json:"welcome"`
}

// SplitConfigService 在每个 Node 中得到独立实例，但读取合并后的同一公共配置块。
type SplitConfigService struct {
	service.Service
	config splitConfig
}

// OnInit 在启动热路径开放前完成强类型解析。
func (target *SplitConfigService) OnInit() error {
	// 先设置 Go 默认值；JSON/YAML 中缺失字段会保留该值。
	target.config = splitConfig{Welcome: "hello from Go default"}
	return target.ParseServiceConfig(&target.config)
}

// OnStart 输出最终 Node、环境变量标签和业务配置，证明多个文件已经合并。
func (target *SplitConfigService) OnStart(context.Context) error {
	target.Logger().Info(fmt.Sprintf(
		"split config loaded: node=%s welcome=%s",
		target.NodeID(),
		target.config.Welcome,
	))
	return nil
}

// init 登记两个 Node 配置都会引用的类型模板。
func init() { app.Setup(&SplitConfigService{}) }

// main 通过同一个 --config 目录加载全部 JSON/YAML 片段。
func main() { app.Start() }
