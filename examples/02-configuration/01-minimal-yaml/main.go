// 本示例验证只包含 nodes 的最小 Application YAML。
package main

import (
	"context"

	"github.com/duanhf2012/origin/v3/application"
	"github.com/duanhf2012/origin/v3/service"
)

// app 会从命令行 --config 指定的目录加载并冻结配置。
var app = application.New()

// ConfigService 的默认实际名称就是 Go 类型名 ConfigService。
type ConfigService struct{ service.Service }

// OnStart 只有在 YAML 成功解析且 ServiceName 已匹配时才会执行。
func (target *ConfigService) OnStart(context.Context) error {
	target.Logger().Info("minimal YAML loaded")
	return nil
}

// init 登记 YAML 中 services 列表允许使用的类型。
func init() { app.Setup(&ConfigService{}) }

// main 启动 Application。
func main() { app.Start() }
