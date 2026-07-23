# Origin v3 Service 协作式调度设计

## 1. 文档状态与范围

- 状态：已确认
- 确认日期：2026-07-23
- 适用版本：Origin v3

本文记录 Service 顺序编程、单执行槽协作式调度、`Await`、任务恢复、超时和取消方面已经确认的设计。

本文不展开以下独立系统：

- RPC 契约、序列化和传输协议；
- TimerEngine 的时间轮内部实现；
- Service 发现、连接筛选和路由；
- CPU 密集任务与 cgo 的工作池实现；
- 固定步长游戏世界 Tick。

RPC 的契约与默认 Deadline 规则见 [Origin v3 RPC 数据类型与序列化设计](./2026-07-23-rpc-data-and-serialization-design.md)，统一定时机制见 [Origin v3 定时器系统设计](./2026-07-23-timer-system-design.md)。

## 2. 设计目标

1. Service 业务逻辑可以按顺序方式编写，不要求把每一次异步调用拆成回调。
2. 同一个 Service 任意时刻只有一个任务拥有执行权并访问 Service 状态，避免普通业务状态产生并发读写。
3. 任务等待 RPC、Redis、数据库或 Timer 时释放 Service 执行权，使其他任务能够继续处理。
4. 挂起与恢复必须显式可见，避免普通函数在开发者不知情时让出 Service 执行权。
5. 不为每一次普通 I/O 等待额外创建一个辅助 goroutine。
6. 通过有界队列、默认超时、指标和过载保护，避免等待任务无限积累。

## 3. 统一执行模型

v3 不要求开发者为 Service 配置 `Serial` 或 `Cooperative` 两种模式，而采用一套统一模型：

- 每个 Service 具有一个逻辑执行槽；
- 同一时刻最多一个任务处于 `Running` 状态并拥有执行槽；
- 普通同步业务代码持续持有执行槽；
- 发起异步 RPC 后立即返回，不会自动让出当前执行槽；
- 只有显式调用 `Await` 等 Origin 感知的等待点，当前任务才释放执行槽；
- 等待完成不代表立即恢复，原任务必须重新进入就绪队列并再次取得执行槽；
- 不允许通过普通 `time.Sleep`、阻塞 Channel 或无法被 Origin 感知的阻塞操作占着执行槽等待。

因此，该模型在 Go 运行时层面可以同时存在多个 Service 任务 goroutine，但在 Service 状态层面始终保持单执行槽串行访问。它不是“一个 Service 永远只有一个物理 goroutine”，而是“一个 Service 永远只有一个拥有状态访问权的任务”。

## 4. 任务状态与调度

Service 任务至少具有以下状态：

- `Ready`：已具备运行条件，等待执行槽；
- `Running`：当前持有执行槽，可以访问 Service 状态；
- `Waiting`：已经释放执行槽，等待外部操作、Timer 或取消；
- `Completed`：任务已经返回并完成清理。

状态转换遵循：

1. 新事件、Timer 回调和可恢复任务统一进入 FIFO Ready 队列；
2. Dispatcher 从队首选择一个任务并授予执行槽；
3. 任务正常返回时进入 `Completed`；
4. 任务调用 `Await` 时从 `Running` 进入 `Waiting`，并原子地释放执行槽；
5. 等待条件完成后，任务追加到 Ready 队尾；
6. 只有 Dispatcher 再次选中该任务并授予执行槽后，`Await` 才能返回到后续业务代码。

恢复任务不能插入队首、不能抢占当前 Running 任务。新任务、Timer 回调和 Await 恢复任务使用同一个 FIFO 顺序，保证规则简单、可理解并避免恢复风暴长期压制新事件。

引擎关闭、取消传播等控制事件可以使用独立内部通道，但只能在任务边界改变业务任务状态，不能在任意一行用户代码中抢占执行槽。

## 5. Await 语义

`Await` 在概念上属于 Service 执行系统，而不是 RPC 模块，因为它还要支持 Redis、数据库、Timer 和其他外部等待。

其语义签名为：

```go
Await[T any](
    ctx context.Context,
    fn func(context.Context) (T, error),
) (T, error)
```

这段代码表示语义，不提前约束最终采用泛型包函数、生成代码或其他 Go API 外观。最终 API 必须保持强类型，并让调用点清楚显示这里会挂起当前 Service 任务。

普通 `Await` 的执行过程为：

1. 根据传入 Context 计算有效 Deadline；
2. 当前任务原子地释放 Service 执行槽并进入 `Waiting`；
3. 当前任务所在的同一个 goroutine 调用 `fn(ctx)`；
4. Go 网络轮询器在 RPC、Redis、数据库等 I/O 等待期间挂起该 goroutine；
5. 其他 Ready 任务取得 Service 执行槽；
6. `fn` 返回后，原任务进入 Ready 队尾；
7. Dispatcher 再次授予原任务执行槽；
8. `Await` 向业务代码返回结果或错误。

普通 I/O `Await` 不额外创建一个 goroutine 执行 `fn`。等待中的 goroutine 仍然存在，但不占用操作系统线程，也不占用 Service 执行槽。

## 6. Service 状态访问规则

调用 `Await` 前，业务代码仍持有 Service 执行槽，可以读取状态并复制调用参数。

从释放执行槽开始，到重新取得执行槽之前：

- `fn` 不得读取或修改非并发安全的 Service 状态；
- `fn` 可以使用调用前复制出的局部值；
- `fn` 可以使用并发安全的 RPC、Redis、数据库等客户端；
- 其他 Service 任务可能修改原有业务状态。

`Await` 返回后，任务重新拥有 Service 执行槽，可以继续访问状态，但必须根据业务一致性要求重新校验版本、对象是否仍存在、玩家是否已经离线等前置条件。

该模型消除了同一时刻并发访问 Service 状态的数据竞争，但仍存在逻辑重入：一次业务流程在等待期间，其他事件可以改变状态。框架不能替业务代码自动解决跨等待点的业务一致性。

## 7. 不同等待类型

### 7.1 RPC、Redis 与数据库

支持 Context 的同步客户端调用可以直接放在 `Await` 的 `fn` 中。当前任务 goroutine 执行该调用，I/O 等待交给 Go 运行时，不需要每个请求再创建辅助 goroutine。

异步 RPC 不会自动让出执行槽。开发者可以：

- 发起异步调用并立即返回，后续通过事件或回调处理；
- 在需要顺序编程时显式 `Await` 该结果。

### 7.2 Sleep

调度式 Sleep 使用当前 Node 的 TimerEngine：

1. 注册一次性等待项；
2. 当前任务释放执行槽并进入 `Waiting`；
3. Timer 到期后把任务追加到 Ready 队列；
4. 任务重新取得执行槽后继续运行。

不得为每个 Sleep 创建一个专用 goroutine，也不得在持有 Service 执行槽时直接调用 `time.Sleep`。

### 7.3 CPU 密集、cgo 和不支持 Context 的阻塞调用

这些操作不能直接使用普通 I/O `Await` 路径，否则可能长期占用运行线程，并且无法可靠响应取消。后续通过独立、有界的工作池设计处理，不能静默退化为无限制创建 goroutine。

## 8. Deadline、默认超时与取消

所有 `Await` 都必须具有有效 Deadline。优先级固定为：

`调用方显式 Deadline > Service 默认值 > Node 默认值 > Origin 内置 15s`

具体规则：

1. 传入 Context 已有 Deadline 时原样继承，不再应用默认值；
2. 显式 Deadline 可以比 Service、Node 或内置默认值更短或更长；
3. 没有显式 Deadline 时依次使用 Service 默认值、Node 默认值；
4. 均未配置时使用 Origin 内置 `15s`；
5. 超时时间从进入 `Await` 开始计算，覆盖外部操作等待和操作完成后的 FIFO 恢复排队；
6. `15s` 只是防止无限等待的最终兜底，不代表业务目标延迟，延迟敏感操作应显式设置更短的 Deadline。

取消是协作式的：

- `Await` 把有效 Context 传给 `fn`；
- `fn` 必须主动监听 Context，框架不能强制终止忽略 Context 的 Go 函数；
- 外部操作完成、Context 取消或 Deadline 到达只能使任务具备恢复条件，不能让等待任务越过 Dispatcher 直接访问 Service 状态；
- 已经开始的任务在取消后仍要恢复一次，重新取得执行槽并返回 `context.Canceled` 或 `context.DeadlineExceeded`，以便正常执行 `defer` 和业务清理；
- Service 停止时取消所有 Waiting 任务的 Context，并按相同规则完成受控恢复和退出，不能只从队列中删除任务。

如果 `Await` 内调用生成的 RPC，RPC 直接继承该 Context 的有效 Deadline。RPC 不重复附加另一套 `15s` 默认超时。

## 9. Timer 回调

TimerEngine 不执行用户代码，只把到期 Timer 投递到所属 Service。Timer 回调作为普通 Service 任务进入 FIFO Ready 队列，并遵守同一执行槽规则。

Timer 回调可以调用 `Await`。挂起期间：

- 其他 Service 任务和其他 Timer 回调可以运行；
- 当前 Timer 回调仍被视为运行中；
- 同一个周期 Timer 不得启动第二个重叠回调；
- 回调重新取得执行槽并完整返回后，Timer 才完成本次执行状态。

## 10. 性能与适用边界

该模型适合 MMORPG 中玩家、背包、任务、公会和需要多次 I/O 的业务 Service，可以减少回调拆分，同时保持 Service 状态单点串行访问。

它不能消除单个热点 Service 的 CPU 上限。场景、世界、AOI 等高频计算 Service 必须按地图、区域、房间或实体集合分片，避免一个执行槽承载全部玩家。CPU 密集计算不应通过增加 Waiting 任务掩盖。

实现阶段必须基准验证：

- 无 `Await` 的普通事件调度开销；
- `Await` 挂起与恢复开销；
- Ready 队列长度和排队延迟；
- Waiting 任务数量及内存占用；
- RPC、Redis 和 Timer 混合负载下的 P50、P95、P99；
- 单热点 Service 与多分片 Service 的吞吐差异；
- 超时或恢复风暴下的公平性。

队列容量、每轮调度批次和 Waiting 任务上限不能凭假设写死，应在后续过载保护设计中根据基准结果确认。

## 11. 可观测性

每个 Service 至少记录：

- Ready、Running、Waiting 任务数量；
- 新建、完成、取消和超时任务数量；
- Ready 排队时间；
- `Await` 外部操作耗时；
- 外部操作完成到重新取得执行槽的恢复排队时间；
- 按显式 Deadline、Service 默认值、Node 默认值和内置 `15s` 区分的超时来源；
- Service 停止时取消和恢复清理的任务数量。

日志必须限频，不能在超时风暴或恢复风暴中为每个任务同步写日志。

## 12. 测试要求

至少覆盖：

1. 同一个 Service 任意时刻只有一个任务持有执行槽；
2. 普通代码和异步 RPC 不会隐式让出执行槽；
3. `Await` 原子释放执行槽，并在恢复后重新取得执行槽；
4. `fn` 在当前任务 goroutine 执行，不额外创建每请求辅助 goroutine；
5. 新事件、Timer 与恢复任务严格按 FIFO 入队；
6. 恢复任务不抢占 Running 任务；
7. RPC、Redis、数据库和 Sleep 的挂起恢复；
8. 显式 Deadline、Service 默认值、Node 默认值和内置 `15s` 的优先级；
9. 超时范围同时覆盖外部操作和恢复排队；
10. `fn` 忽略 Context 时框架不会伪装成已经强制终止；
11. 取消任务恢复一次并执行 `defer`；
12. Service 停止期间 Waiting 任务的取消、恢复与引用清理；
13. Timer 回调在 `Await` 期间不发生同 Timer 重叠执行；
14. 高并发挂起、完成、超时和停止之间的竞态；
15. Ready 与 Waiting 积压时的指标和过载行为。

## 13. 已确认结论

Origin v3 Service 执行模型最终采用：

- 一套统一的单执行槽协作式调度模型，不配置 Serial/Cooperative 模式；
- 同一 Service 任意时刻只允许一个任务访问 Service 状态；
- 多个任务可以同时处于 Waiting，但恢复后必须重新竞争唯一执行槽；
- `Await` 是显式挂起点，普通异步调用不会自动让出；
- 普通 I/O `Await` 由当前任务 goroutine 执行，不额外创建每请求辅助 goroutine；
- 新任务、Timer 回调和恢复任务共用 FIFO Ready 队列；
- `Await` 默认超时的最终兜底为 `15s`；
- 超时优先级为显式 Deadline、Service 默认值、Node 默认值、Origin 内置 `15s`；
- Sleep 使用 Node TimerEngine，不为每次等待创建 goroutine；
- Timer 回调允许 `Await`，但同一个周期 Timer 不与自身重叠；
- CPU 密集、cgo 和无法协作取消的阻塞调用留给独立有界工作池设计；
- 热点场景 Service 必须分片，并通过基准测试验证延迟和吞吐。
