# 04：Timer、Event 与安全执行

## 我想稍后或按周期执行任务

运行：[examples/04-timer-event-and-execution/01-delay-and-cron](../../../../examples/04-timer-event-and-execution/01-delay-and-cron)。

```go
s.AfterFunc(300*time.Millisecond, func(ctx context.Context, id service.TimerID) {
    // 到期后在当前 Service 的串行执行上下文中只执行一次。
})

s.NewTicker(250*time.Millisecond, func(ctx context.Context, id service.TimerID) {
    // 固定节拍重复执行；可用 id 暂停、恢复或取消该 Timer。
})

s.CronFunc("*/1 * * * * *", func(ctx context.Context, id service.TimerID) {
    // 每秒一次；支持 5 或 6 段数字 Cron 表达式。
})
```

`TimerStats` 返回当前数量与触发、暂停、恢复、取消等累计统计。Timer 回调已经处于所属 Service 的执行上下文；不要再为同一份 Service 状态额外启动 goroutine。

## 我想在同一 Service 内通知事件

运行：[examples/04-timer-event-and-execution/02-local-event](../../../../examples/04-timer-event-and-execution/02-local-event)。先在 `OnInit` 注册监听器，再从业务任务中通知：

```go
// 在 OnInit 注册本 Service 内的事件处理器。
err := s.SubscribeEvent(playerJoinedEvent, handler)
// 同步通知：处理器完成后才返回。
err = s.NotifyEventSync(ctx, PlayerJoined{PlayerID: 1001})
// 异步通知：仅保证事件已进入当前 Service 队列。
err = s.NotifyEventAsync(PlayerJoined{PlayerID: 1002})
// 读取本地事件计数与队列统计。
stats := s.EventStats()
```

## 我想等待操作或安全地执行独立工作

运行：[examples/04-timer-event-and-execution/03-await-and-safe](../../../../examples/04-timer-event-and-execution/03-await-and-safe)。

- `Await`：等待 I/O、RPC 等操作，并让 Service 在等待时继续处理其他任务。
- `SetDefaultAwaitTimeout`：为没有显式 Deadline 的 Await 设置统一默认超时。
- `DispatchAsync`：提交一个后续串行 Service 任务。
- `RunSafe`：当前 goroutine 的独立、已隔离工作；不授予并发修改 Service 状态的权限。
- `GoSafe`：业务自行管理生命周期的后台 goroutine 的 panic 保底；仍要自行使用 Context、CancelFunc、WaitGroup 在 `OnStop` 清理。

需要观察调度器时读取 `ExecutionStats`，不要直接访问内部队列。

## 深入一点

同步事件处理器可以嵌套同步事件，但不能在其中 `Await`。异步事件只保证已进入队列，不能在提交后修改事件 payload。Timer、事件和 RPC 回调都遵循 Service 的单执行语义；要并发处理纯计算或外部 I/O 时，应明确隔离状态并选择 `Await` 或受控后台任务。
