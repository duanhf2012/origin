// 本示例展示 pprof Listener 的运行期启动、地址查询和主动关闭。
package main

import (
	"context"
	"fmt"
	"time"

	"github.com/duanhf2012/origin/v3/application"
	"github.com/duanhf2012/origin/v3/service"
)

// app 是当前示例唯一的 Application。
var app = application.New()

// PprofService 把诊断端口控制集中在明确的运维生命周期中。
type PprofService struct{ service.Service }

// OnStart 只绑定回环地址，并在短时间后主动关闭 pprof。
func (target *PprofService) OnStart(context.Context) error {
	// StartPprof 可由业务受控入口按需调用，而不必作为永久启动参数。
	if err := app.StartPprof("127.0.0.1:6060"); err != nil {
		return err
	}
	// PprofAddress 返回当前实际监听地址。
	address, _ := app.PprofAddress()
	target.Logger().Info(fmt.Sprintf("pprof started: http://%s/debug/pprof/", address))
	// StopPprof 接收 Context，可参与统一超时和优雅停止。
	target.AfterFunc(2*time.Second, func(ctx context.Context, _ service.TimerID) {
		if err := app.StopPprof(ctx); err == nil {
			target.Logger().Info("pprof stopped by application code")
		}
	})
	return nil
}

// init 登记 pprof 控制 Service。
func init() { app.Setup(&PprofService{}) }

// main 启动 Application。
func main() { app.Start() }
