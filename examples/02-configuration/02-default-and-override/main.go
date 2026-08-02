// 本示例展示 Go 默认值、公共 Service 配置与 Node 专属配置的优先级。
package main

import (
	"context"
	"fmt"

	"github.com/duanhf2012/origin/v3/application"
	"github.com/duanhf2012/origin/v3/service"
)

// app 是当前示例唯一的 Application。
var app = application.New()

// serviceConfig 使用 JSON Tag 同时映射 YAML 解码后的字段名。
type serviceConfig struct {
	Welcome    string `json:"welcome"`
	MaxPlayers int    `json:"max_players"`
}

// ConfigService 在 OnInit 中把最终有效配置保存为强类型值。
type ConfigService struct {
	service.Service
	config serviceConfig
}

func (target *ConfigService) OnInit() error {
	// 先写 Go 业务默认值；YAML 只覆盖实际出现的字段。
	target.config = serviceConfig{Welcome: "default welcome", MaxPlayers: 10}
	// Node 专属块存在时整体取代公共块，但缺失字段仍保留上面的 Go 默认值。
	if err := target.ParseServiceConfig(&target.config); err != nil {
		return err
	}
	target.Logger().Info(fmt.Sprintf("welcome=%q max_players=%d", target.config.Welcome, target.config.MaxPlayers))
	return nil
}

// OnStart 不再读取配置，避免把解析工作放入业务热路径。
func (target *ConfigService) OnStart(context.Context) error { return nil }

// init 登记 ConfigService 类型模板。
func init() { app.Setup(&ConfigService{}) }

// main 启动 Application。
func main() { app.Start() }
