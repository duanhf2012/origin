// 本示例展示 Module 如何读取所属 Service 的完整配置、相对路径和根配置。
package main

import (
	"fmt"

	"github.com/duanhf2012/origin/v3/application"
	"github.com/duanhf2012/origin/v3/service"
)

// app 是当前示例唯一的 Application。
var app = application.New()

// settings 对应 ConfigService 的完整业务配置。
type settings struct {
	Region string `json:"region"`
}

// configuredNode 只声明当前示例希望从根配置读取的 Node 字段。
// 宽松解码会忽略 YAML 中其他不需要的字段。
type configuredNode struct {
	ID string `json:"id"`
}

// ConfigModule 通过嵌入 service.Module 获得所属 Service 的配置外观。
type ConfigModule struct{ service.Module }

// OnInit 在配置已经冻结、业务任务尚未开始时读取并保存配置。
func (target *ConfigModule) OnInit() error {
	// ParseServiceConfig 解析当前 Service 的完整有效配置。
	var complete settings
	if err := target.ParseServiceConfig(&complete); err != nil {
		return err
	}

	// GetServiceConfig 使用相对路径只读取一个业务配置字段。
	var region string
	if err := target.GetServiceConfig("region", &region); err != nil {
		return err
	}

	// GetConfig 从 Application 根配置读取显式路径；这里读取全部 Node 的精简视图。
	var nodes []configuredNode
	if err := target.GetConfig("nodes", &nodes); err != nil {
		return err
	}

	fmt.Printf(
		"module config: parsed_region=%q path_region=%q first_node=%q\n",
		complete.Region,
		region,
		nodes[0].ID,
	)
	return nil
}

// ConfigService 只负责把 ConfigModule 纳入自己的生命周期树。
type ConfigService struct{ service.Service }

// OnInit 中添加 Module，使其可以在同一初始化阶段安全读取配置。
func (target *ConfigService) OnInit() error {
	return target.AddModule(&ConfigModule{})
}

// init 登记 Service 模板；Module 由 Service 自己添加，不写入 YAML services。
func init() { app.Setup(&ConfigService{}) }

// main 启动 Application。
func main() { app.Start() }
