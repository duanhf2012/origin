# Origin v3 定时器系统设计

## 1. 文档状态与范围

- 状态：已确认
- 确认日期：2026-07-23
- 最后更新：2026-07-24
- 适用版本：Origin v3

本文只设计 Origin v3 的定时器系统，范围包括：

- Node 级 `TimerEngine`；
- Service 对外的 `ITimer` 接口；
- `TimerID`、一次性 Timer、周期 Timer 和 Cron；
- 到期任务的投递、取消、生命周期和过载规则；
- RPC、连接管理和服务发现使用定时器的接入边界；
- 百万级活跃定时任务的容量与性能验证要求。

本文不展开以下相对独立的系统：

- RPC 接口生成、`Future`、`Await` 和远端取消协议；
- Service 的串行、协作式调度和重入策略；
- 固定步长、补帧和游戏世界 Tick 调度；
- 持久化 Reminder 和跨进程恢复。

Timer 回调最终如何与其他 Service 任务交错，由后续 Service 执行模型设计决定。本文只规定 TimerEngine 不直接执行用户代码，以及同一个周期 Timer 不得与自己重叠。

## 2. 设计目标

1. 一个 Node 对外只有一套定时器能力，Service、RPC 和系统组件不各自维护互不一致的定时器实现。
2. 每个 Node 独立持有一个 `TimerEngine`，不建立跨 Node 的 Application 全局定时器。
3. Service 通过组合 `ITimer` 直接调用定时器方法，不需要先取得 `.Timers()` 对象。
4. 业务层只持有 `TimerID`，不持有内部 Timer 对象。
5. 时间轮协程只负责到期判断和投递，任何用户回调都在所属执行器中运行。
6. 一次性 Timer 不因 Service 繁忙而静默丢失；周期 Timer 在过载时合并错过的触发。
7. 单 Node 按最多一百万个活跃定时任务进行容量和基准验证。
8. 保持实现边界清晰。时间轮层级、桶数量等纯内部参数不进入公共接口，由实现阶段的基准测试决定。
9. Service 进入退休状态时不自动暂停 Timer，由业务通过统一接口决定需要暂停和恢复的 Timer。

## 3. Origin v2 现状与取舍

Origin v2 实际存在两套定时机制。

### 3.1 业务定时器

`util/timer` 使用一个进程级全局最小堆：

- 所有 Service 和 Node 共享同一个堆和互斥锁；
- Node 启动时以 `10ms` 最小间隔轮询；
- Timer 到期后，由全局定时器协程向所属 Service 的 Channel 同步投递；
- 取消主要设置原子状态，已取消条目可能一直保留到原到期时间；
- Service 在自己的事件循环中执行回调。

“回调回到所属 Service 执行”值得保留。全局单例、固定轮询、同步阻塞投递和延迟清理不适用于 v3。

### 3.2 RPC 超时

v2 RPC 为 pending call 单独维护带索引的最小堆：

- 请求完成时可以按序号删除超时项；
- 默认每 `1s` 轮询一次超时；
- 单次最多处理固定数量的超时调用；
- 与业务定时器形成重复实现和不同的精度语义。

v3 保留“RPC 完成后立即取消对应超时项”的能力，但不保留独立轮询协程和一秒扫描周期。

## 4. 总体结构

### 4.1 每个 Node 一个 TimerEngine

每个 Node 创建并持有唯一的 `TimerEngine`。同一 Application 中的多个 Node 不共享 TimerEngine、时间轮、TimerID 空间和到期队列。

Node 启动时启动 TimerEngine。Node 停止时：

1. 禁止注册新的 Timer；
2. 关闭 Service、RPC、连接管理和服务发现等所有者作用域；
3. 取消尚未执行的 Timer；
4. 等待正在执行的回调按照 Service 停止规则退出；
5. 最后停止 TimerEngine。

### 4.2 统一引擎，隔离所有者

Service、RPC、连接管理和服务发现共用当前 Node 的 TimerEngine，但拥有独立的所有者作用域和 Ready 列表。

统一不表示所有到期任务进入同一个阻塞队列。一个所有者繁忙时，不能阻塞时间轮和其他所有者。

### 4.3 TimerEngine 的职责边界

TimerEngine 只负责：

- 注册、取消和定位 Timer；
- 推进时间轮；
- 判断 Timer 到期；
- 将到期条目移动到所属所有者的 Ready 列表；
- 发出合并唤醒信号；
- 维护状态、指标和生命周期。

TimerEngine 不负责：

- 直接执行用户回调；
- 决定 Service 是否允许任务重入；
- 执行业务错误恢复；
- 运行固定步长游戏循环；
- 持久化 Timer。

## 5. 分层时间轮

### 5.1 核心结构

TimerEngine 使用分层时间轮，不使用 v2 的全局最小堆轮询。

设计约束如下：

- 基础时间精度为 `1ms`；
- 小于一个时间刻度的正延迟向上取整，Timer 不得提前触发；
- 注册和取消以平均 `O(1)` 为设计目标；
- 不按一百万容量一次性预分配全部 Timer 对象；
- 时间轮使用一个可复用的底层唤醒源，不为每个 Timer 创建 `time.Timer` 或 goroutine；
- 时间轮层级与桶数量属于内部实现参数，不进入公共 API，必须通过容量和延迟基准确定。

### 5.2 时钟规则

以下能力使用单调时间：

- `AfterFunc`；
- `TickerFunc`；
- RPC deadline；
- `Await` deadline；
- 心跳、重连、空闲检测和服务发现刷新等相对时间任务。

单调时间任务不受系统时间、时区和 NTP 校时影响。

`CronFunc` 使用墙上时钟，并按 Node 的 Cron 时区计算日历时间。测试使用可注入时钟，不保留 v2 的进程级可变时间偏移全局状态。

## 6. Service 对外接口

### 6.1 类型定义

```go
type TimerID uint64

const InvalidTimerID TimerID = 0

type TimerFunc func(ctx context.Context, timerID TimerID)

type ITimer interface {
    AfterFunc(delay time.Duration, fn TimerFunc) TimerID
    TickerFunc(interval time.Duration, fn TimerFunc) TimerID
    CronFunc(expr string, fn TimerFunc) (TimerID, error)

    PauseTimer(timerID TimerID) bool
    ResumeTimer(timerID TimerID) bool
    CancelTimer(timerID *TimerID) bool
}
```

`IService` 直接组合 `ITimer`：

```go
type IService interface {
    ITimer

    // 其他 Service 能力
}
```

业务 Service 可以直接调用：

```go
timerID := s.AfterFunc(3*time.Second, func(
    ctx context.Context,
    timerID TimerID,
) {
    // Service 逻辑
})

s.CancelTimer(&timerID)
```

### 6.2 不暴露内部 Timer 对象

业务层只持有 `TimerID`，不能获得时间轮节点、回调容器或内部 Timer 指针。这样可以减少外部与 TimerEngine 的引用关系，并防止业务代码直接修改内部状态。

只暴露 ID 不能单独消除内存泄漏。TimerEngine 仍必须在 Timer 执行、取消和所有者关闭时：

- 从时间轮或 Ready 列表中移除条目；
- 清除回调闭包；
- 清除 Context 和所有者引用；
- 释放内部条目。

### 6.3 TimerID 规则

- `0` 永远表示无效 ID；
- TimerID 在当前 Node 生命周期内单调生成并且不复用；
- ID 耗尽时拒绝创建新 Timer，不能从头复用旧 ID；
- Service 获得的是绑定当前 Service 所有者的 `ITimer` 实现；
- `PauseTimer`、`ResumeTimer` 和 `CancelTimer` 必须校验 Timer 所有者，不能操作其他 Service 的 Timer；
- TimerID 只用于标识，不作为判断 Timer 当前是否仍活跃的唯一依据。

### 6.4 参数失败

- `AfterFunc` 的负数延迟无效，返回 `InvalidTimerID`；
- `AfterFunc(0, fn)` 在后续调度轮次执行，不能在调用栈内同步调用 `fn`；
- `TickerFunc` 的周期必须大于零，否则返回 `InvalidTimerID`；
- 空回调不能创建 Timer，返回 `InvalidTimerID`；
- `CronFunc` 的表达式非法时返回错误和 `InvalidTimerID`；
- TimerEngine 已停止或所有者正在关闭时，创建操作失败；
- 失败必须增加指标；配置或表达式错误应返回明确错误，不能静默修正。

## 7. Timer 控制语义

### 7.1 CancelTimer

接口接收 `*TimerID`，由框架负责清零：

```go
CancelTimer(timerID *TimerID) bool
```

规则如下：

1. 参数为 `nil` 时返回 `false`；
2. `*timerID == InvalidTimerID` 时返回 `false`；
3. 读取原 ID 后立即把 `*timerID` 设置为 `InvalidTimerID`；
4. Timer 仍在时间轮或 Ready 列表时，取消成功并返回 `true`，回调不得开始执行；
5. Timer 已经开始、已经完成、已被取消、ID 不存在或所有者不匹配时，返回 `false`；
6. 对任何非零 ID 调用结束后，外部变量都保持为零；
7. `CancelTimer` 不强行终止已经运行或挂起的业务回调；
8. Service 停止时通过回调 Context 和 Service 执行模型处理正在运行的任务。

同一个 `TimerID` 变量不能被多个业务 goroutine 无序读写。业务变量仍遵循所属 Service 的状态访问规则；TimerEngine 内部状态转换必须并发安全。

### 7.2 PauseTimer 与 ResumeTimer

暂停和恢复接收 `TimerID` 值，不接收指针，也不修改调用方变量。暂停后的 Timer 仍然存在，ID 仍然有效；只有取消才通过 `CancelTimer(*TimerID)` 把外部变量清零。

```go
PauseTimer(timerID TimerID) bool
ResumeTimer(timerID TimerID) bool
```

通用规则如下：

1. `timerID == InvalidTimerID`、ID 不存在、所有者不匹配、Timer 已取消或已经完成时返回 `false`；
2. 活跃 Timer 成功转为暂停状态时，`PauseTimer` 返回 `true`；
3. 暂停 Timer 成功恢复为活跃状态时，`ResumeTimer` 返回 `true`；
4. 重复暂停或重复恢复没有发生状态转换，返回 `false`；
5. 暂停状态的 Timer 仍可以取消，取消后立即清理条目和回调引用；
6. 暂停只影响后续触发，不取消、终止或回滚已经开始执行的回调。

### 7.3 恢复后的时间语义

采用“保留剩余时间”方案：

- `AfterFunc` 暂停时保存距离到期的剩余时长，恢复后继续等待该剩余时长；
- `TickerFunc` 暂停时保存距离下一节拍的剩余时长；恢复后的第一次触发等待该剩余时长，此后继续使用原始周期；
- `CronFunc` 暂停期间的触发全部跳过；恢复时按当前墙上时间计算下一个未来匹配点，不补执行暂停期间错过的 Cron。

如果 Timer 已经到期并进入 Ready 列表，但回调尚未开始，暂停成功并记为剩余时长 `0`。恢复后把该回调放到所属 Service 的后续调度轮次执行，不能在 `ResumeTimer` 调用栈内同步执行。

### 7.4 与正在执行回调的竞争

- 一次性 Timer 的回调已经开始时已不存在可暂停的未来触发，`PauseTimer` 返回 `false`；
- 周期 Timer 或 Cron 的回调正在运行或因 `Await` 挂起时，`PauseTimer` 可以标记“当前回调结束后保持暂停”，当前回调继续执行；
- 已标记暂停的周期 Timer 或 Cron 在当前回调结束后不再安排下一次触发；
- `ResumeTimer` 只能恢复已经进入暂停状态的 Timer，不能使同一 Timer 的两个回调重叠；
- 状态转换必须由 TimerEngine 原子裁决，业务代码不依赖并发调用的先后猜测结果。

## 8. Timer 类型与触发语义

### 8.1 AfterFunc

`AfterFunc` 是一次性 Timer：

- 到期后只进入一次 Ready 列表；
- Service 繁忙时允许延迟，但不能静默丢失；
- 回调开始前可以取消；
- 回调完成后立即清理内部条目和引用。

### 8.2 TickerFunc

`TickerFunc` 使用固定节拍并跳过错过次数的语义：

- 节拍以原始时间轴计算，不以回调结束时间重新起算；
- Service 繁忙时不补执行历史次数；
- 多次错过的触发合并；
- 同一个周期 Timer 最多保留一个待执行回调；
- 前一次回调正在运行或因调度式等待而挂起时，不启动同一 Timer 的第二个回调；
- 前一次回调完整结束后，按当前单调时间寻找下一个未来节拍。

该规则用于业务周期任务，也用于系统内部的心跳、连接检测和服务发现周期任务。

固定步长和补帧不属于 `TickerFunc`。v3 首版不提供固定步长 Timer。

### 8.3 CronFunc

Cron 兼容 v2 的数字表达式：

- 支持 5 段：`分 时 日 月 周`，秒默认为 `0`；
- 支持 6 段：`秒 分 时 日 月 周`；
- 支持 `*`、`,`、`-` 和 `/`；
- 星期使用 `0` 到 `6`；
- 首版不支持月份或星期英文名、`@every`、`@daily` 和表达式内时区。

Cron 时区规则：

- Node 未配置 `timer.timezone` 时使用操作系统 `time.Local`；
- Node 可以显式配置 IANA 时区，例如 `Asia/Shanghai` 或 `UTC`；
- 配置的时区不存在时，Node 启动失败；
- Node 启动日志记录最终时区名称和当前 UTC 偏移；
- 生产环境建议显式配置时区，避免机器环境差异。

Cron 到期时重新检查墙上时钟：

- 系统时间向后调整且尚未达到目标时间时，重新调度，不提前执行；
- 系统时间向前跳跃时，不补执行错过的 Cron，只计算当前应执行的一次和下一个未来时间；
- Cron 回调使用与一次性 Timer 相同的投递和所有者规则。

## 9. 到期投递与过载

### 9.1 每个所有者独立 Ready 列表

Timer 到期后：

1. TimerEngine 将原 Timer 条目移动到所属所有者的 Ready 列表；
2. 不为每个到期 Timer 再创建一个 Service 事件对象；
3. 向所有者的专用唤醒通道发送一个合并信号；
4. 唤醒通道容量为 `1`，已有信号时不重复发送；
5. 所有者执行器被唤醒后，批量获取到期条目。

某个 Service 的 Ready 列表积压不能阻塞时间轮，也不能阻塞其他 Service、RPC 和系统所有者。

### 9.2 公平性

Service 每次唤醒只处理有限批次的到期条目。批次结束后仍有任务时保留唤醒状态，让 RPC、普通事件和其他 Timer 获得调度机会。

批次大小是 Service 执行器的内部参数，不属于 `ITimer`。默认值必须根据延迟、吞吐和公平性基准确定。

### 9.3 不丢失与合并

- 一次性 Timer 不丢失；
- Cron 的单次到期不丢失；
- 周期 Timer 按第 8.2 节合并；
- 不允许时间轮协程为降低积压而直接执行用户回调；
- 过载通过排队延迟、指标、日志和告警暴露，不能静默改变一次性业务语义。

Ready 列表保存的是原 Timer 条目，不为到期事件复制回调和业务数据。Service 进入 `Stopping/Draining` 前已经进入 Ready 的条目按排空规则处理；所有者进入最终清理后，任何未完成条目必须取消并清理。

## 10. 与 Service 执行模型的边界

TimerEngine 面向 Service 执行器投递，不依赖某一种具体执行模式。

必须满足以下共同约束：

- Timer 回调只在所属 Service 的执行上下文中运行；
- Service 的状态访问规则同样适用于 Timer 回调；
- 同一个周期 Timer 的回调不能与自己重叠；
- Timer 回调完整返回前，Timer 状态保持为运行中；
- 如果 Service 执行器支持调度式挂起，挂起期间 Timer 仍视为运行中；
- `CancelTimer` 不能强制展开或终止正在执行的回调；
- Service 关闭 Deadline 到期时取消尚未完成回调的 Context，并由 Service 执行器完成恢复和清理。

Timer 是否能够与其他 Service 任务交错、哪些操作会形成挂起点以及恢复顺序，由独立的 Service 执行模型设计确定。

相关规则见 [Origin v3 Service 协作式调度设计](./2026-07-23-service-cooperative-scheduling-design.md)。

Service 进入退休状态时，TimerEngine 不自动暂停或取消该 Service 的 Timer。退休只关闭新入站 RPC；Timer 仍按正常规则触发。业务需要暂时停止某项定时逻辑时，显式调用 `PauseTimer`，详细边界见 [Origin v3 Service 退休设计](./2026-07-24-service-retirement-design.md)。

## 11. RPC 与系统组件接入边界

### 11.1 RPC

RPC deadline 使用同一个 Node TimerEngine，但使用内部紧凑条目：

- 所有生成的 RPC 调用在没有显式 Deadline、Service 默认值和 Node 默认值时，使用 Origin 内置 `15s` 超时；
- 不为每个 RPC timeout 保存业务回调闭包；
- pending call 完成、取消或断开连接时，立即取消对应定时项；
- 到期项进入 RPC 所有者自己的 Ready 列表；
- RPC pending 管理器完成超时状态和 Future；
- RPC 超时不经过某个业务 Service 的 Timer Ready 列表；
- RPC 是否发送远端取消、如何恢复等待任务，属于 RPC 和 Service 调度设计。

`Await` 的有效 Deadline 同样注册到当前 Node 的 TimerEngine。没有显式 Deadline、Service 默认值和 Node 默认值时，使用 Origin 内置 `15s` 超时。TimerEngine 只负责到期通知，不直接恢复任务或执行用户函数；任务恢复、Context 错误和 Service 执行权由独立的 Service 执行模型负责。

RPC 的 Context、Deadline 优先级和调用范围见 [Origin v3 RPC 数据类型与序列化设计](./2026-07-23-rpc-data-and-serialization-design.md)，生成调用外观见 [Origin v3 RPC 接口与调用语义设计](./2026-07-23-rpc-interface-and-call-semantics-design.md)。

### 11.2 连接管理与服务发现

心跳、重连、空闲检测和服务发现刷新使用同一 TimerEngine，并拥有独立所有者作用域。作用域关闭时批量取消全部内部 Timer。

系统周期任务默认采用 `TickerFunc` 的固定节拍、错过合并语义，不补执行过期次数。

## 12. 生命周期与引用清理

Service 停止时：

1. 标记 Timer 所有者为关闭中，拒绝新增；
2. 取消仍在时间轮中、暂停中或尚未到期的业务 Timer；
3. Stop 前已经到期并进入 Ready 队列的 Timer 回调属于排空集合，允许执行完成；
4. 正在运行或 Waiting 的 Timer 回调按 Service 优雅停止规则排空；
5. 关闭 Deadline 到期时取消尚未完成回调的 Context；
6. `OnStop` 期间业务 `ITimer` 继续拒绝新增，但 Node TimerEngine 仍为 RPC 和关闭 Deadline 提供系统定时能力；
7. finalizer 清除剩余回调、Context 和所有者引用；
8. 释放所有者索引。

完整停止顺序见 [Origin v3 Service 优雅停止设计](./2026-07-24-service-graceful-stop-design.md)。

一次性 Timer 执行完成、周期 Timer 取消和 Cron 取消时执行同样的引用清理。对象池只能在基准证明有收益且 Reset 完整可验证时使用，首版不因假设性能收益增加复杂池化实现。

## 13. 可观测性

每个 Node 和所有者至少记录：

- 当前活跃 Timer 数；
- 当前暂停 Timer 数；
- 注册、暂停、恢复、取消、触发和自然完成数量；
- 无效注册、无效暂停、无效恢复和无效取消数量；
- Ready 列表长度；
- 周期触发合并次数；
- 时间轮调度延迟；
- Ready 排队延迟；
- 回调执行时间；
- P50、P95 和 P99 到期延迟；
- RPC deadline 数量和超时数量；
- `Await` deadline 数量和超时数量；
- Service 或 Node 停止时批量清理数量。

日志必须限频，不能在超时风暴中为每个 Timer 同步写日志并放大故障。

## 14. 性能验证

实现阶段至少覆盖 `1万`、`10万` 和 `100万` 活跃 Timer，并测试：

- 单线程注册和取消；
- 多 goroutine 并发注册和取消；
- 多 goroutine 并发暂停、恢复和取消；
- RPC 正常响应导致的大量快速取消；
- 大量 Timer 同一毫秒到期；
- Timer 在不同时间轮层级之间迁移；
- 内存占用和每次操作分配次数；
- 到期延迟的 P50、P95 和 P99；
- 某个 Service 长时间繁忙时对其他所有者的影响；
- TCP 与 NATS RPC 压力下的 TimerEngine 延迟。

性能结论必须记录 Go 版本、操作系统、CPU、并发量和 Timer 分布。若时间轮复杂度与可维护性产生冲突，必须依据基准数据重新与开发者确认，不能自行更换成更复杂的混合时间轮或时间轮加最小堆方案。

## 15. 测试要求

测试使用可注入时钟，避免依赖真实等待。至少包括：

1. 1ms 边界、向上取整和不提前触发；
2. TimerID 唯一性和零值规则；
3. `CancelTimer` 清零、返回值和所有者校验；
4. `PauseTimer` 和 `ResumeTimer` 的返回值、状态转换和所有者校验；
5. `AfterFunc` 暂停后保留剩余时长；
6. `TickerFunc` 恢复后的第一次触发保留剩余时长，后续恢复原周期；
7. `CronFunc` 跳过暂停期间的触发，并从下一个未来匹配点恢复；
8. 暂停已到期但尚未执行的 Timer，恢复后在后续调度轮次执行；
9. 暂停正在运行或 `Await` 中的周期回调，不终止当前回调且不产生重叠；
10. 暂停状态仍可取消并完整清理引用；
11. 取消与到期、Ready、开始执行之间的竞争；
12. 一次性 Timer 在 Service 繁忙时不丢失；
13. 周期 Timer 错过合并且不自重入；
14. Service 退休不自动暂停或取消 Timer；
15. Service 停止时批量取消和引用清理；
16. 一个所有者积压不阻塞其他所有者；
17. 合并唤醒不会丢失 Ready 状态；
18. Cron 5 段和 6 段表达式兼容；
19. Cron 本地时区、显式时区和非法时区；
20. 墙上时钟向前、向后调整；
21. RPC pending 正常完成、取消、超时和断线清理；
22. RPC 与 `Await` 在没有上层 Deadline 时正确使用内置 `15s` 兜底；
23. Node 停止期间拒绝新增并完整释放；
24. TimerEngine goroutine不执行任何用户回调。

## 16. 已确认的取舍

Origin v3 定时器系统最终采用：

- 每个 Node 一个 TimerEngine；
- Service、RPC 和系统组件统一使用该引擎；
- 分层时间轮，基础精度 `1ms`；
- 单 Node 一百万活跃任务的设计和基准规模；
- `IService` 直接组合 `ITimer`；
- 对外只暴露 TimerID，不暴露内部 Timer 对象；
- `PauseTimer(TimerID)` 和 `ResumeTimer(TimerID)` 不改变 TimerID；
- `CancelTimer(*TimerID)` 负责把调用方变量清零；
- `AfterFunc` 和 `TickerFunc` 暂停后保留距离下一次触发的剩余时长；
- `CronFunc` 跳过暂停期间的触发，恢复后只计算下一个未来匹配点；
- Service 退休不自动暂停 Timer，由业务显式决定；
- Timer 回调接收 `context.Context` 和 TimerID；
- 周期 Timer 使用固定节拍、错过合并的语义；
- Cron 兼容 v2 的 5/6 段数字语法；
- Cron 默认使用操作系统本地时区，并支持 Node 显式配置；
- 一次性 Timer 不丢失，周期触发可以合并；
- 每个所有者独立 Ready 列表、容量为 1 的合并唤醒和分批执行；
- TimerEngine 不执行用户回调；
- 首版不支持固定步长 Timer。
