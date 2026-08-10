# Node 游戏逻辑时间

Origin 为每个 Node 提供一套独立游戏逻辑时间。本示例用一个控制 Service 设置时间并快进一天，让另一个 Service 中的 `AfterFunc`、`NewTicker` 和 `CronFunc` 同时响应。

## 直接运行

Windows 执行 `run.bat`，Linux 执行 `./run.sh`。程序会：

1. 把 `game-1` 的逻辑时间设为 `2030-01-01T11:59:55Z`；
2. 一秒后调用 `AddTime(24*time.Hour)`；
3. 观察另一 Service 的 After、Ticker 和每日 Cron 各进入一次串行回调；
4. 按 `Ctrl+C` 停止。

Ticker 不会补执行快进期间的全部历史周期；当前回调返回后，后续 `Ticker coalesced` 日志会输出合并数。Cron 也只触发一次，然后从新逻辑时间寻找下一个未来匹配点。

## 关键使用方式

Service 不需要知道配置中的 NodeID，直接获取自己所属 Node 的最小运行外观：

```go
currentNode := target.GetNode()

// 读取当前 Node 的游戏逻辑时间。
now := currentNode.Now()

// 设置到指定时刻，之后仍按真实时间 1:1 前进。
err := currentNode.SetTime(time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC))

// 在现有偏移上增加或减少时间；负数表示向后调整。
err = currentNode.AddTime(24 * time.Hour)
_ = now
_ = err
```

`SetTime` 和 `AddTime` 影响当前 Node 全部 Service/Module 的业务 Timer，但不修改系统时钟，也不会提前 RPC/Await/Context、发现 TTL、心跳或启停 Deadline。时间偏移不会持久化，进程重启后恢复为真实时间。

完整规则见 [Node 游戏逻辑时间教程](../../../docs/maintenance/v3.1/guides/node-game-time.md)。
