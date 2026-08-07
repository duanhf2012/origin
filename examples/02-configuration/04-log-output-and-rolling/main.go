// 本示例展示控制台与文件使用不同级别、格式，以及文件滚动配置的实际效果。
package main

import (
	"context"
	"errors"

	"github.com/duanhf2012/origin/v3/application"
	originlog "github.com/duanhf2012/origin/v3/log"
	"github.com/duanhf2012/origin/v3/service"
)

// app 从示例 config 目录加载完整日志配置，并统一拥有日志 Runtime 的关闭责任。
var app = application.New()

// LogService 使用框架自动绑定 NodeID 和 ServiceName 的 Logger。
type LogService struct{ service.Service }

// OnStart 写入不同级别和字段类型，便于对比控制台 text 与文件 JSON 输出。
func (target *LogService) OnStart(context.Context) error {
	// 控制台最低级别为 info，因此这条 debug 只进入最低级别为 debug 的文件。
	target.Logger().Debug("debug message for file output",
		originlog.Int64("player_id", 1001),
	)
	// 结构化字段会保留类型；不要先用 fmt.Sprintf 把数值拼成不可检索的文本。
	target.Logger().Info("player entered",
		originlog.Int64("player_id", 1001),
		originlog.String("region", "cn-east"),
	)
	// ErrorStack 会附带有界调用栈，并尽力在一秒内可靠写出；只用于重要异常。
	target.Logger().ErrorStack("example failure",
		originlog.Err(errors.New("tutorial error")),
	)
	return nil
}

// init 登记配置中引用的 LogService 类型模板。
func init() { app.Setup(&LogService{}) }

// main 交给 Application 处理命令、日志创建、信号和最终 Flush/Close。
func main() { app.Start() }
