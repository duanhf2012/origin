# Node 游戏逻辑时间变更摘要

> 状态：已完成
> 基线：v3.0
> 目标版本：v3.1.0
> 兼容性：新增公开外观；既有 Timer、RPC、发现和线协议方法签名不变
> 完成日期：2026-08-08

## 关联提交

- 设计与实施计划：`a4876a2` `设计v3.1 Node游戏逻辑时间`
- 基线生命周期与入门教程收尾：`1de46b8` `完善服务模块生命周期与入门教程`
- 功能、测试、示例与教程：`707c9f0` `实现Node游戏逻辑时间与定时器重排`

## 使用者可见变更

Service 和 Module 新增当前所属 Node 的最小运行外观：

```go
type NodeRuntime interface {
    ID() string
    Now() time.Time
    SetTime(value time.Time) error
    AddTime(delta time.Duration) error
}

func (service *Service) GetNode() NodeRuntime
func (module *Module) GetNode() NodeRuntime
```

`GetNode()` 不要求业务代码写死 NodeID，也不暴露 Node 启停、Service 集合或网络内部资源。未绑定 Service 和未归属 Module 返回 `nil`。

## 时间与 Timer 语义

- 每个 Node 保持独立逻辑时间，默认与真实时间一致；
- `SetTime` 设置目标时刻，`AddTime` 支持正数、负数与幂等零增量；
- 影响当前 Node 全部 Service/Module 的 `AfterFunc`、`NewTicker`、`CronFunc`；
- 向前跳跃只提交一次当前回调，Ticker 把错过周期累计到 `CoalescedTotal`，Cron 不补跑历史；
- 向后跳跃保留 Scheduled Timer 的原逻辑目标，DuePending、Ready、Running 不撤回；
- Paused Timer 不参与重排；
- RPC、Await、Context、发现 TTL、心跳、重连和启停 Deadline 继续使用真实单调时间；
- 偏移不持久化、不跨 Node 广播，进程重启后恢复真实时间。

## 内部实现收尾

- Node 用原子偏移保持 `Now()` 无锁读取，用冷路径锁串行化 Set/Add、全 Service 重排和 Stopping 边界。
- ServiceScheduler 统一使用 Node 逻辑时间计算业务名义点；ReadyDelay 等基础诊断仍使用 TimerEngine 真实时钟。
- TimerEngine 新增内部 `RescheduleAfter`，原地移动 Tick 并保留 DeadlineID；到期竞争时回退到新 ID 和新代次。
- Scheduler 激活时会把 OnStart 阶段已经到期的 DuePending Timer 提升到 Ready，避免零延迟启动 Timer 沉默。

## 文档与示例

- 设计：[Node 游戏逻辑时间设计](../design/Node游戏逻辑时间设计.md)
- 使用教程：[Node 游戏逻辑时间](../guides/node-game-time.md)
- 可运行示例：[04-node-game-time](../../../../examples/05-timer-event-and-execution/04-node-game-time/README.md)
- 验收数据：[Node 游戏逻辑时间验收报告](../reports/Node游戏逻辑时间验收报告.md)
