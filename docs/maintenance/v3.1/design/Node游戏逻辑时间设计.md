# Node 游戏逻辑时间设计

> 状态：已确认，允许实施  
> 基线：v3.0  
> 目标：v3.1.0  
> 兼容性：新增接口；不改变 v3.0 Timer、RPC、发现和线协议的既有方法签名  
> 确认日期：2026-08-08

## 1. 目标与边界

游戏服务器经常需要在开发、测试、活动管理和故障演练中把业务时间设置到指定时刻，或将
业务时间向前、向后调整，以验证跨天刷新、活动开启、周期结算等定时逻辑。直接修改操作
系统时钟会同时影响 TLS、日志、数据库、etcd、NATS、同机其他进程和 Go Runtime 的基础
设施 Deadline，因此 Origin 只提供 Node 级游戏逻辑时间，不修改操作系统时间。

本次能力满足以下边界：

1. 每个 Node 拥有一套独立游戏逻辑时间，默认与真实时间相同；
2. 时间调整影响该 Node 中全部 Service 和 Module 的 `AfterFunc`、`NewTicker`、`CronFunc`；
3. 同一 Application 中其他 Node 不受影响；
4. RPC、Await、Context、发现 TTL、心跳、重连、启停 Deadline 和日志仍使用真实单调时间；
5. 时间偏移不自动持久化或跨 Node 广播，进程重启后恢复为真实时间；
6. 不恢复 v2 的包级可变 `timeOffset`，也不增加 Application 级共享 TimerEngine。

本设计是 v3.0 [定时器系统设计](../../../baseline/v3.0/design/details/2026-07-23-定时器系统设计.md)
的兼容增量。v3.0 关于“相对基础设施 Deadline 使用单调时间”的结论保持不变；v3.1 只把
业务 Timer 的名义时间与 Node 游戏逻辑时间关联。

## 2. 所有权

所有权固定为：

```text
Application：创建和管理 Node，提供每 Node Timer 默认策略
└─ Node：拥有独立 TimerEngine、游戏逻辑时间和 Timer 总额度
   ├─ Service：在所属 Node 中登记业务 Timer
   └─ Module：通过所属 Service 登记业务 Timer
```

Application 的 `TimerOptions` 仍只是所有 Node 使用的默认策略，不表示 Application 拥有一套
共享 TimerEngine。每个 Node 继续创建、启动和关闭自己的 `timerwheel.Engine`。游戏逻辑
时间与 TimerEngine 使用相同的 Node 生命周期，但基础设施 Deadline 继续读取 TimerEngine
的真实单调时钟。

## 3. 使用接口

Service 和 Module 不接受 NodeID，也不返回完整的 `*node.Node`。它们通过当前运行实例已经
绑定的 Runtime 返回最小 Node 外观：

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

业务代码使用当前所属 Node，不与部署名称绑定：

```go
func (service *PlayerService) OpenNextDay() error {
    currentNode := service.GetNode()
    service.Logger().Info("advance game time",
        originlog.String("node_id", currentNode.ID()),
        originlog.String("before", currentNode.Now().Format(time.RFC3339)),
    )
    return currentNode.AddTime(24 * time.Hour)
}
```

Module 使用完全相同的外观：

```go
func (module *ActivityModule) ResetTo(value time.Time) error {
    return module.GetNode().SetTime(value)
}
```

进程管理代码已经持有明确 Node 时，直接使用具体 Node：

```go
currentNode, ok := app.Node("game-1")
if ok {
    _ = currentNode.AddTime(24 * time.Hour)
}
```

`GetNode()` 遵循项目确认的代码风格。未绑定的 Service、尚未归属 Service 的 Module 返回
`nil`；完成绑定后返回值在整个实例生命周期内稳定。返回对象是最小运行外观，不暴露 Node
的启动、停止、服务集合或内部资源。

## 4. 时间语义

### 4.1 `Now`

`Now()` 返回当前 Node 的游戏逻辑时间，并使用该 Node 已冻结的 `TimerLocation`。默认偏移为
零，因此初始值与真实当前时间一致。读取属于业务热路径，必须保持并发安全、无锁读取和零
堆分配。

### 4.2 `SetTime`

`SetTime(value)` 使调用线性化时刻的 Node 游戏时间等于 `value`，之后继续按真实时间 1:1
前进。零 `time.Time`、无法用 `time.Duration` 表达的偏移以及已经进入 Stopping、Stopped、
Failed 的 Node 返回稳定参数或生命周期错误，不修改已有偏移。

`SetTime` 可以在 Node 的 Created、Starting 和 Ready 阶段使用，因此 Service 可以在
`OnInit`、`OnStart` 或普通业务任务中设置时间。多个并发设置按 Node 内唯一修改锁串行化。

### 4.3 `AddTime`

`AddTime(delta)` 在当前偏移上原子增加 `delta`。零值是成功的幂等操作，负数表示向后调整；
偏移加法溢出时返回 `CodeInvalidArgument` 并保持原值。与 `SetTime` 一样，它只调整调用方
所属 Node。

### 4.4 调整完成边界

`SetTime`、`AddTime` 返回前完成以下工作：

1. 提交新的 Node 时间偏移；
2. 遍历该 Node 已经准备好的 ServiceScheduler；
3. 把仍处于 Scheduled 的业务 Deadline 原地移到新 Tick，保留 DeadlineID；
4. 若旧 Deadline 正好已到期，删除旧 Binding 并回退为登记新 Deadline；
5. 唤醒 Node TimerEngine 处理已经到期的项目。

返回成功只表示重排已经提交，不等待业务回调执行完成。回调仍必须进入所属 Service 的有界
Ready 队列并取得唯一执行权。

## 5. Timer 调整规则

### 5.1 向前调整

- `AfterFunc` 的名义时刻已经越过时，只触发一次；
- `NewTicker` 越过一个或多个周期时，只产生一次当前回调，错过次数计入既有
  `CoalescedTotal`，下一次安排到新逻辑时间之后；
- `CronFunc` 越过一个或多个日历点时，只产生一次当前回调，不遍历或补执行全部历史点，
  随后从新逻辑时间计算下一个未来点；
- 零延迟重排仍经过 TimerEngine 和 Service Ready 队列，不在 `AddTime` 调用栈同步执行用户
  代码。

### 5.2 向后调整

- Scheduled Timer 保留原绝对逻辑目标，因此重新登记更长的真实等待；
- Deadline 已到但尚未提交业务任务时，再次检查逻辑时间；逻辑时间尚未到达目标就重新登记，
  防止向后调整后提前执行；
- 已经进入 DuePending、Ready 或 Running 的回调不撤回、不倒放，也不重复执行；
- Paused Timer 不参与时间重排。After/Ticker 恢复时使用暂停时保存的逻辑剩余时间，Cron
  恢复时从当前逻辑时间重新计算未来点，暂停期间不补触发。

### 5.3 新建和周期续订

时间调整后的新 `AfterFunc`、`NewTicker`、`CronFunc` 均从当前 Node 逻辑时间计算。Ticker 和
Cron 回调完成后的下一次名义时间也读取 Node 逻辑时间；真实 TimerEngine 只负责等待由名义
时间换算出的相对时长。

## 6. 并发与生命周期

Node 使用独立冷路径锁串行化 `SetTime`、`AddTime` 与进入 Stopping 的状态边界；`Now` 只
原子读取偏移，不获取该锁。ServiceScheduler 仍使用自己的既有互斥锁保护 Timer Map、状态、
代次和 Deadline Binding。

时间更新先发布偏移，再依次取得各 Scheduler 锁重排 Timer。重排使用 TimerEngine
内部 `RescheduleAfter` 保留 DeadlineID 并原地移动时间轮节点，避免两张 ID Map 批量
换键；时间轮已经使旧 ID 到期时，Scheduler 删除旧 Binding、增加代次并登记
新 ID，晚到的旧到期通知因 Binding 或代次不匹配而失效。Timer 创建在 Scheduler 锁内
读取当前逻辑时间，因此并发创建只有两种合法结果：在更新前创建并被随后重排，或在更新后
直接按新时间登记，不会遗漏旧时间 Timer。

Node Stop 先关闭时间修改准入，再进入业务排空。时间修改不会创建 goroutine、不会执行用户
代码，也不会绕过 Service 任务上限。Node 完成停止后，已有 `NodeRuntime` 只允许读取时间，
修改返回生命周期错误。

## 7. 性能

游戏业务可能高频读取当前时间，因此 `Now()` 使用 `time.Now()` 加一次原子偏移读取，不使用
反射、Map、锁、Context、Channel、闭包或临时 goroutine；必须通过 Benchmark 验证
`allocs/op == 0`。

时间修改属于开发、测试和管理冷路径，允许以 `O(当前 Node 活跃业务 Timer 数)` 重排。该
选择避免每个 Node 增加第二套 TimerEngine 和 goroutine，也不在普通 Timer 到期热路径增加
多时钟查找。冷路径只为稳定 TimerID 排序分配一个线性 Slice，不为每个 Timer 生成
新 Deadline 对象或 Map Key。重排 Benchmark 记录 1、1,000 和 100,000 个 Scheduled Timer 的 `ns/op`、
`B/op` 与 `allocs/op`，用于识别意外退化，不承诺时间修改是常数时间。

## 8. 错误与可观测性

- `GetNode()` 在未绑定对象上返回 `nil`；
- `SetTime(time.Time{})`、不可表达偏移和偏移加法溢出返回 `CodeInvalidArgument`；
- Stopping、Stopped、Failed 分别返回既有生命周期错误，不新增错误码；
- 单个 Timer 重排只有在 Scheduler 同时停止时才会失败，此时停止路径拥有最终清理权，时间
  修改返回对应生命周期错误；
- Origin 不自动记录每次业务时间修改，调用方应按权限和审计需求记录操作者、原因、修改前后
  时间；日志时间戳始终是真实时间，业务逻辑时间必须作为独立字段输出。

## 9. 测试与验收

实现必须覆盖：

1. Service、Module `GetNode()` 返回当前 Node 最小外观，未绑定返回 `nil`；
2. `Now` 默认值、Set、Add、负数、零值、溢出和并发线性化；
3. 两个 Node 的时间和 Timer 严格隔离；
4. 向前调整后 After 触发一次、Ticker/Cron 合并历史且继续调度；
5. 向后调整后 Scheduled Timer 不提前触发；
6. DuePending、Ready、Running 不撤回或重复；
7. Paused Timer 不受调整，Resume 遵守既有规则；
8. RPC/Await Deadline 和 TimerEngine 基础 Deadline 不受游戏时间影响；
9. OnInit、OnStart、Running 可修改，Stopping 以后拒绝；
10. `go test -race ./node ./service`、全仓测试、`go vet ./...`、跨平台构建；
11. `Now` 零分配 Benchmark 和批量 Timer 重排 Benchmark。
12. TimerEngine 原地重排保留 DeadlineID，取消/到期竞争回退到新 ID。

## 10. 教程与示例

v3.1 使用者文档新增“Node 游戏逻辑时间”，先演示 `GetNode().Now/SetTime/AddTime`，再解释
Node 作用域、Timer 跳跃规则、暂停行为、真实基础设施时间隔离和不持久化边界。示例必须同时
包含 After、Ticker、Cron，并打印真实日志时间与逻辑时间的区别。

v3.0 基线教程只允许修复事实和表达问题，因此 `StartTimeout` 的简化属于基线说明修正；游戏
逻辑时间作为 v3.1 新功能只写入 `docs/maintenance/v3.1/`，不回填已冻结的 v3.0 基线。
