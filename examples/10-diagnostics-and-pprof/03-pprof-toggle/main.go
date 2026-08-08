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

// OnStart 观察 --pprof 的初始监听，然后演示运行期关闭、重开和再次关闭。
func (target *PprofService) OnStart(context.Context) error {
	// 真实运行实例从 OnInit 起即可取得受限 Application 外观；无需保存全局 app 指针。
	runtime := target.Application()
	if runtime == nil {
		return fmt.Errorf("application runtime is unavailable")
	}
	// run 脚本的 --pprof 在任何 Service.OnStart 前完成绑定，因此这里必须已经可见。
	address, running := runtime.PprofAddress()
	if !running {
		return fmt.Errorf("--pprof did not start the listener")
	}
	target.Logger().Info(fmt.Sprintf("pprof started: http://%s/debug/pprof/", address))

	// StopPprof 可能等待正在采集的 HTTP 请求退出。Timer 回调属于 Service Task，因此用
	// Await 暂时释放串行执行权；普通独立 goroutine 可以直接同步调用 StopPprof。
	target.AfterFunc(2*time.Second, func(ctx context.Context, _ service.TimerID) {
		err := target.Await(ctx, func(waitCtx context.Context) error {
			return runtime.StopPprof(waitCtx)
		})
		if err == nil {
			target.Logger().Info("pprof stopped by application code")
		} else {
			target.Logger().Error("stop pprof failed")
		}
	})

	// StartPprof 只做状态校验和 Listen，是可并发调用的短操作，可在 Service Task 中直接调用。
	target.AfterFunc(4*time.Second, func(context.Context, service.TimerID) {
		if err := runtime.StartPprof("127.0.0.1:6060"); err != nil {
			target.Logger().Error("restart pprof failed")
			return
		}
		target.Logger().Info("pprof restarted by application code")
	})
	// 再次关闭，表明命令行只决定初始状态，不会阻止运行期控制。
	target.AfterFunc(6*time.Second, func(ctx context.Context, _ service.TimerID) {
		err := target.Await(ctx, func(waitCtx context.Context) error {
			return runtime.StopPprof(waitCtx)
		})
		if err == nil {
			target.Logger().Info("pprof stopped again")
		}
	})
	return nil
}

// init 登记 pprof 控制 Service。
func init() { app.Setup(&PprofService{}) }

// main 启动 Application。
func main() { app.Start() }
