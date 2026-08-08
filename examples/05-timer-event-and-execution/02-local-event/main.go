// 本示例对比同一 Service 内的同步事件和异步事件通知。
package main

import (
	"context"
	"fmt"
	"time"

	"github.com/duanhf2012/origin/v3/application"
	"github.com/duanhf2012/origin/v3/service"
)

// EventID 必须稳定且非零；真实项目通常集中管理这些常量。
const playerJoinedEvent service.EventID = 1

// playerAuditEvent 演示同步监听器中嵌套通知另一个同步事件。
const playerAuditEvent service.EventID = 2

// PlayerJoined 是不可变的事件值。Mode 仅用于区分本示例的两种通知方式。
type PlayerJoined struct {
	PlayerID int64
	Mode     string
}

// EventID 把 payload 类型绑定到稳定事件 ID。
func (PlayerJoined) EventID() service.EventID { return playerJoinedEvent }

// PlayerAudit 是 PlayerJoined 监听器同步触发的嵌套事件。
type PlayerAudit struct {
	PlayerID int64
}

// EventID 将审计 payload 绑定到独立事件 ID。
func (PlayerAudit) EventID() service.EventID { return playerAuditEvent }

// app 是当前示例唯一的 Application。
var app = application.New()

// EventService 同时承担事件生产者和监听者，强调这是 Service 本地机制。
type EventService struct{ service.Service }

// OnInit 是唯一允许登记事件监听器的生命周期阶段。
func (target *EventService) OnInit() error {
	if err := target.SubscribeEvent(playerJoinedEvent, func(ctx context.Context, event service.Event) error {
		// 同一 EventID 首次通知后会绑定具体 Go 类型，因此这里可断言 PlayerJoined。
		joined := event.(PlayerJoined)
		target.Logger().Info(fmt.Sprintf(
			"player %d joined by %s event",
			joined.PlayerID,
			joined.Mode,
		))
		// 在同步监听器中嵌套同步通知：审计监听器完成后，本监听器才返回。
		return target.NotifyEventSync(ctx, PlayerAudit{PlayerID: joined.PlayerID})
	}); err != nil {
		return err
	}
	return target.SubscribeEvent(playerAuditEvent, func(_ context.Context, event service.Event) error {
		audit := event.(PlayerAudit)
		target.Logger().Info(fmt.Sprintf("player %d audit completed", audit.PlayerID))
		return nil
	})
}

// OnStart 通过一次性 Timer 进入正常的 Service 任务上下文后发送事件。
func (target *EventService) OnStart(context.Context) error {
	target.AfterFunc(100*time.Millisecond, func(ctx context.Context, _ service.TimerID) {
		// 同步通知会在当前任务中按订阅顺序执行全部监听器，并聚合返回错误。
		if err := target.NotifyEventSync(ctx, PlayerJoined{PlayerID: 1001, Mode: "sync"}); err != nil {
			target.Logger().Error("sync event failed")
		}

		// 异步通知只提交一个后续 Service 任务；提交后不要再修改 payload。
		if err := target.NotifyEventAsync(PlayerJoined{PlayerID: 1002, Mode: "async"}); err != nil {
			target.Logger().Error("async event submission failed")
		}

		// 稍后读取累计统计，确保异步事件已有机会完成。
		target.AfterFunc(200*time.Millisecond, func(context.Context, service.TimerID) {
			stats := target.EventStats()
			target.Logger().Info(fmt.Sprintf(
				"event stats: sync=%d async=%d failures=%d",
				stats.SyncNotifiedTotal,
				stats.AsyncNotifiedTotal,
				stats.HandlerFailureTotal,
			))
		})
	})
	return nil
}

// init 登记事件 Service 模板。
func init() { app.Setup(&EventService{}) }

// main 启动命令行 Application。
func main() { app.Start() }
