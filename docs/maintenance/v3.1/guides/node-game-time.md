# Node 游戏逻辑时间

游戏开发中经常需要验证跨天刷新、活动开启、周期结算和时间回退。Origin 不修改操作系统时钟，而是让每个 Node 拥有一套独立的游戏逻辑时间。

可直接运行 [04-node-game-time](../../../../examples/05-timer-event-and-execution/04-node-game-time/README.md)，观察同 Node 两个 Service 的完整行为。

## 先会用

### 在 Service 中读取时间

Service 已经与实际 Node 实例绑定，不要在业务代码中写死 `game-1` 之类的部署 ID：

```go
func (target *ActivityService) CurrentGameTime() time.Time {
    // GetNode 返回当前 Service 所属 Node 的最小运行外观。
    return target.GetNode().Now()
}
```

Module 也可以直接调用 `module.GetNode()`，它会委托给所属 Service。只有尚未绑定的零值 Service，或尚未归属 Service 的 Module，`GetNode()` 才返回 `nil`。

### 设置或增减时间

```go
func (target *ActivityAdminService) OpenTomorrow() error {
    currentNode := target.GetNode()

    // 快进 24 小时；负数可以向后调整。
    return currentNode.AddTime(24 * time.Hour)
}

func (target *ActivityAdminService) ResetForTest() error {
    // SetTime 使当前逻辑时间等于指定值，之后仍按真实时间 1:1 前进。
    return target.GetNode().SetTime(
        time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC),
    )
}
```

`SetTime`/`AddTime` 返回成功时，该 Node 全部已登记业务 Timer 已完成重排；已过期回调仍会异步进入所属 Service 的串行 Ready 队列，不会在管理调用栈中直接执行。

## 深入一点

### Timer 为什么属于 Node

Application 只在创建时提供 `TimerOptions` 默认值；每个 Node 都会创建、运行和关闭自己的 TimerEngine，并维护自己的时间偏移与 Timer 总额度。Service 和 Module 只把业务 Timer 登记到所属 Node，所以修改 `game-1` 不会影响同进程的 `game-2`。

```text
Application（创建和管理 Node，提供 Timer 默认选项）
├─ game-1（独立 TimerEngine + 独立逻辑时间）
│  ├─ ActivityService
│  └─ PlayerService
└─ game-2（独立 TimerEngine + 独立逻辑时间）
```

### 影响范围

| 项目 | 是否受逻辑时间影响 |
| --- | --- |
| 当前 Node 全部 Service/Module 的 `AfterFunc`、`NewTicker`、`CronFunc` | 是 |
| 同一 Application 中的其他 Node | 否 |
| `time.Now()` 和日志时间戳 | 否 |
| RPC、Await、Context Deadline | 否 |
| 发现 TTL、心跳、重连、启动与停止超时 | 否 |

因此日志应把业务时间作为独立字段输出，不要把日志自带时间戳当成游戏时间。

### 向前调整

- `AfterFunc`：已经跨过目标时刻时只触发一次。
- `NewTicker`：跨过多个周期时只提交一次当前回调，错过数记入 `TimerStats().CoalescedTotal`，不补跑历史。
- `CronFunc`：跨过多个日历点时也只提交一次，回调完成后从新逻辑时间寻找下一个未来匹配点。

这种“合并而不补执行”的规则可以避免快进一年时突然连续执行数千次结算回调。需要补算历史的业务，应在一次回调中根据业务数据显式决定补算范围。

### 向后调整与暂停

- 仍在 Scheduled 的 Timer 保留原绝对逻辑目标，因此需要等待更长的真实时间，不会提前执行。
- 已经进入 DuePending、Ready 或 Running 的回调不撤回、不倒放、不重复。
- Paused Timer 不参与重排。After/Ticker 恢复后继续等待暂停时保存的剩余时间；Cron 恢复后从当前逻辑时间寻找未来点。

### 生命周期、持久化和安全

Created、OnInit、OnStart 和 Running 阶段可以修改时间；Node 进入 Stopping 后，`SetTime`/`AddTime` 返回生命周期错误。时间偏移不自动持久化、不通过发现广播，进程重启后恢复为真实时间。

在生产环境开放时间修改前，应由业务管理层限制环境、身份和操作权限，并记录操作人、原因、NodeID、修改前后时间。Origin 只提供并发安全的 Node 时钟与 Timer 重排，不替业务决定谁可以修改。
