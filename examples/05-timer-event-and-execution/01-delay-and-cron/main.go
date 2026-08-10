// 本示例集中展示 Service 的一次性 Timer、Ticker、Cron 以及 Timer 控制 API。
package main

import (
	"context"
	"fmt"
	"time"

	"github.com/duanhf2012/origin/v3/application"
	"github.com/duanhf2012/origin/v3/service"
)

// app 是当前示例唯一的 Application，所有 Service 类型都登记到这里。
var app = application.New()

// TimerService 保存需要后续控制的 Ticker ID 和已触发次数。
// Timer 回调由所属 Service 串行执行，因此 tickerFires 不需要额外加锁。
type TimerService struct {
	service.Service
	tickerID    service.TimerID
	tickerFires int
}

// OnStart 在 Service 已完成初始化后登记全部业务 Timer。
func (target *TimerService) OnStart(context.Context) error {
	// AfterFunc 只触发一次；即使延迟为零，也不会在当前调用栈同步执行。
	if id := target.AfterFunc(300*time.Millisecond, func(context.Context, service.TimerID) {
		target.Logger().Info("after timer fired once")
	}); id == service.InvalidTimerID {
		return fmt.Errorf("create after timer failed")
	}

	// NewTicker 使用固定节拍重复触发，同一个 Ticker 不会并发执行两个回调。
	target.tickerID = target.NewTicker(250*time.Millisecond, func(_ context.Context, _ service.TimerID) {
		target.tickerFires++
		target.Logger().Info(fmt.Sprintf("ticker fired: count=%d", target.tickerFires))

		switch target.tickerFires {
		case 2:
			// 在正在执行的周期回调中暂停，暂停会在本次回调返回后生效。
			if target.PauseTimer(target.tickerID) {
				target.Logger().Info("ticker paused")
			}
			// 使用另一个一次性 Timer 恢复刚才暂停的 Ticker。
			if id := target.AfterFunc(400*time.Millisecond, func(context.Context, service.TimerID) {
				if target.ResumeTimer(target.tickerID) {
					target.Logger().Info("ticker resumed")
				}
			}); id == service.InvalidTimerID {
				target.Logger().Error("create ticker resume timer failed")
			}
		case 4:
			// CancelTimer 会取消后续触发，并把调用方保存的 TimerID 清零。
			if target.CancelTimer(&target.tickerID) {
				stats := target.TimerStats()
				target.Logger().Info(fmt.Sprintf(
					"ticker canceled: triggered=%d paused=%d resumed=%d",
					stats.TriggeredTotal,
					stats.PausedTotal,
					stats.ResumedTotal,
				))
			}
		}
	})
	if target.tickerID == service.InvalidTimerID {
		return fmt.Errorf("create ticker failed")
	}

	// CronFunc 使用当前 Node 逻辑时间的日历表达式；六段表达式的第一段表示秒。
	if _, err := target.CronFunc("*/1 * * * * *", func(context.Context, service.TimerID) {
		target.Logger().Info(fmt.Sprintf("cron fired at %s", time.Now().Format(time.RFC3339)))
	}); err != nil {
		return err
	}
	return nil
}

// init 登记零值模板，框架会为 YAML 中的每个实例创建独立对象。
func init() { app.Setup(&TimerService{}) }

// main 交给 Application 处理 start/stop 命令与优雅退出。
func main() { app.Start() }
