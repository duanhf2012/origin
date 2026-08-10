// 本示例展示 Node 级游戏逻辑时间，以及时间跳跃如何统一影响同 Node 的全部业务 Timer。
package main

import (
	"context"
	"fmt"
	"time"

	"github.com/duanhf2012/origin/v3/application"
	"github.com/duanhf2012/origin/v3/service"
)

// app 是当前示例唯一的 Application，YAML 决定两个 Service 实例的实际创建顺序。
var app = application.New()

// ClockControlService 模拟受权的游戏时间管理入口。
// 实际项目应在 RPC/HTTP 管理层补充身份认证、权限校验和审计日志。
type ClockControlService struct{ service.Service }

// OnStart 先设置一个可重现的游戏时间，然后在一秒后模拟“快进一天”。
func (target *ClockControlService) OnStart(context.Context) error {
	currentNode := target.GetNode()
	if currentNode == nil {
		return fmt.Errorf("ClockControlService has no bound node")
	}
	initial := time.Date(2030, 1, 1, 11, 59, 55, 0, time.UTC)
	if err := currentNode.SetTime(initial); err != nil {
		return fmt.Errorf("set initial game time: %w", err)
	}
	target.printTimes("game time initialized")

	// AfterFunc 也是 Node 业务 Timer；本回调执行时还处于 Running，因此 AddTime
	// 只重排其他 Scheduled Timer，不会重复执行当前回调。
	if id := target.AfterFunc(time.Second, func(context.Context, service.TimerID) {
		before := currentNode.Now()
		if err := currentNode.AddTime(24 * time.Hour); err != nil {
			target.Logger().Error(fmt.Sprintf("advance game time failed: %v", err))
			return
		}
		target.Logger().Info(fmt.Sprintf(
			"advanced node=%s before=%s after=%s",
			currentNode.ID(),
			before.Format(time.RFC3339),
			currentNode.Now().Format(time.RFC3339),
		))
	}); id == service.InvalidTimerID {
		return fmt.Errorf("create game time control timer failed")
	}
	return nil
}

// printTimes 把真实系统时间与 Node 逻辑时间放在同一条日志中，便于观察二者边界。
func (target *ClockControlService) printTimes(message string) {
	target.Logger().Info(fmt.Sprintf(
		"%s real=%s logical=%s",
		message,
		time.Now().Format(time.RFC3339),
		target.GetNode().Now().Format(time.RFC3339),
	))
}

// TimedService 模拟活动、结算等依赖游戏时间的业务服务。
type TimedService struct {
	service.Service
	tickerID service.TimerID
}

// OnStart 注册三种业务 Timer。ClockControlService 快进后，它们都使用同一 Node 逻辑时间重排。
func (target *TimedService) OnStart(context.Context) error {
	// 快进 24 小时会跨过这个一次性目标，但仍只执行一次。
	if id := target.AfterFunc(12*time.Hour, func(context.Context, service.TimerID) {
		target.printLogical("AfterFunc fired once")
	}); id == service.InvalidTimerID {
		return fmt.Errorf("create AfterFunc failed")
	}

	// 快进跨过多个 6 小时节拍时只合并执行一次，不会补跑全部历史周期。
	target.tickerID = target.NewTicker(6*time.Hour, func(context.Context, service.TimerID) {
		target.Logger().Info(fmt.Sprintf(
			"Ticker fired logical=%s",
			target.GetNode().Now().Format(time.RFC3339),
		))
		// Ticker 的合并统计在本回调返回、框架计算下一名义点时提交。用后续 Service
		// Timer 读取，避免在当前回调内看到提交前的旧值。
		if id := target.AfterFunc(0, func(context.Context, service.TimerID) {
			stats := target.TimerStats()
			target.Logger().Info(fmt.Sprintf(
				"Ticker coalesced=%d",
				stats.CoalescedTotal,
			))
		}); id == service.InvalidTimerID {
			target.Logger().Error("create ticker stats timer failed")
		}
	})
	if target.tickerID == service.InvalidTimerID {
		return fmt.Errorf("create ticker failed")
	}

	// 每天零点触发。快进跨过历史零点时只执行一次，之后从新逻辑时间计算下一个零点。
	if _, err := target.CronFunc("0 0 0 * * *", func(context.Context, service.TimerID) {
		target.printLogical("Cron midnight fired")
	}); err != nil {
		return fmt.Errorf("create cron: %w", err)
	}
	return nil
}

// printLogical 演示普通 Service 和管理 Service 一样，通过 GetNode 读取当前所属 Node，不需要写死 NodeID。
func (target *TimedService) printLogical(message string) {
	target.Logger().Info(fmt.Sprintf(
		"%s node=%s logical=%s",
		message,
		target.GetNode().ID(),
		target.GetNode().Now().Format(time.RFC3339),
	))
}

// init 只登记类型模板；框架会按 YAML 中的 services 顺序创建当前 Node 的独立实例。
func init() {
	app.Setup(&ClockControlService{})
	app.Setup(&TimedService{})
}

// main 交给 Application 处理 start/stop 命令、配置加载和优雅退出。
func main() { app.Start() }
