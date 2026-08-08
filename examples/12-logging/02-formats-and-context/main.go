// 本示例用同一条 Service 日志对比控制台 text 与文件 JSON 的输出策略。
package main

import (
	"context"

	"github.com/duanhf2012/origin/v3/application"
	originlog "github.com/duanhf2012/origin/v3/log"
	"github.com/duanhf2012/origin/v3/service"
)

// app 从 config 目录加载两个输出端的独立格式和归属字段开关。
var app = application.New()

// FormatService 产生一条包含多种字段类型的结构化日志。
type FormatService struct{ service.Service }

// OnStart 写入控制台和文件共同接收的 Info 日志。
func (target *FormatService) OnStart(context.Context) error {
	target.Logger().Info(
		"player loaded",
		originlog.Int64("player_id", 10001),
		originlog.String("player_name", "Boyce Duan"),
		originlog.Bool("online", true),
		// Any 在文本中编码为紧凑 JSON，在 JSON 输出中保留结构。
		originlog.Any("position", map[string]int{"x": 10, "y": 20}),
	)
	return nil
}

// init 登记配置中引用的 FormatService 模板。
func init() { app.Setup(&FormatService{}) }

// main 启动 Application。
func main() { app.Start() }
