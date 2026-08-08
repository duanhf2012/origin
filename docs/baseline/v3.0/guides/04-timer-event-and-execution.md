# 04：Timer、Event 与安全执行

## 我想稍后或按周期执行任务

运行：[examples/05-timer-event-and-execution/01-delay-and-cron](../../../../examples/05-timer-event-and-execution/01-delay-and-cron)。

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

运行：[examples/05-timer-event-and-execution/02-local-event](../../../../examples/05-timer-event-and-execution/02-local-event)。先在 `OnInit` 注册监听器，再从业务任务中通知：

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

运行：[examples/05-timer-event-and-execution/03-await-and-safe](../../../../examples/05-timer-event-and-execution/03-await-and-safe)。

- `Await`：等待 I/O、RPC 等操作，并让 Service 在等待时继续处理其他任务。
- `SetDefaultAwaitTimeout`：为没有显式 Deadline 的 Await 设置统一默认超时。
- `DispatchAsync`：提交一个后续串行 Service 任务。
- `RunSafe`：当前 goroutine 的独立、已隔离工作；不授予并发修改 Service 状态的权限。
- `GoSafe`：业务自行管理生命周期的后台 goroutine 的 panic 保底；仍要自行使用 Context、CancelFunc、WaitGroup 在 `OnStop` 清理。

需要观察调度器时读取 `ExecutionStats`，不要直接访问内部队列。

## 深入一点

同步事件处理器可以嵌套同步事件，但不能在其中 `Await`。异步事件只保证已进入队列，不能在提交后修改事件 payload。Timer、事件和 RPC 回调都遵循 Service 的单执行语义；要并发处理纯计算或外部 I/O 时，应明确隔离状态并选择 `Await` 或受控后台任务。

`SetDefaultAwaitTimeout` 只能由当前真实 Service 在 `OnInit` 调用，并且必须传入正时长；它只覆盖
该 Service 未带 Deadline 的 `Await`/有响应 RPC 默认预算，不能在运行期动态调整。传入显式
`context.WithTimeout` 时仍以显式 Deadline 为准。`Module` 调用该方法设置的是所属 Service 的同一项
冻结配置，因此团队应在 Service 的 `OnInit` 集中决定，避免多个 Module 争夺默认值。

三个统计快照都适合低频诊断或业务自检，不要每个请求上报一次：

| 方法 | 重点字段 | 排查场景 |
| --- | --- | --- |
| `ExecutionStats()` | `Accepted`、`Ready`、`Awaiting`、`RejectedTotal`、`PanicTotal` | 判断 Service 是否积压、长期等待或发生任务 panic。 |
| `TimerStats()` | `Active`、`Ready`、`Paused`、`RejectedTotal`、`CoalescedTotal`、`MaxReadyDelay` | 判断 Timer 额度、回调积压、暂停状态与固定节拍跳过。 |
| `EventStats()` | `SyncNotifiedTotal`、`AsyncNotifiedTotal`、`HandlerFailureTotal` | 判断本地事件实际通知量与监听器失败。 |

`TimerID` 只在所属 Node 的当前生命周期中唯一，且只能控制创建它的同一 Service/Module 的 Timer。
`CancelTimer(&id)` 成功后会把调用方持有的 ID 清零，建议保存为字段并传指针；`PauseTimer`/`ResumeTimer`
对不存在或不属于自己的 ID 返回 false。Timer 创建失败时返回 `InvalidTimerID`，`CronFunc` 还会返回
表达式错误。事件 `EventID` 必须是稳定非零值，同一 ID 首次通知后绑定具体 Go payload 类型；后续使用
不同类型会返回错误，而不是隐式转换。
