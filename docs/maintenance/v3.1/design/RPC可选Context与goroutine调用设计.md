# RPC 可选 Context 与 goroutine 调用设计

> 状态：已实施
> 基线：v3.0
> 目标：v3.1.0
> 兼容性：保留既有 RPC 契约、线协议和生成方法；新增 `CallXxx`，并放宽既有方法的 Context 入参
> 确认日期：2026-08-03

## 1. 目标与边界

本设计在不改变服务端 RPC 接口、ContractID、MethodID、Fingerprint 和 TCP/NATS 线协议的
前提下完成以下调整：

1. `AwaitXxx`、`AsyncXxx`、`NotifyXxx` 和 `BroadcastXxx` 接受 `nil`、
   `context.Background()`、`context.TODO()` 及普通自定义 Context；
2. Await 的 Service 执行身份由 owner 的当前执行帧关联，不再要求业务 Context 携带私有令牌；
3. 为有响应 RPC 生成 `CallXxx`，供普通 goroutine 阻塞等待且不操作 Service 调度器；
4. 每次公开调用只冻结一次绝对 Deadline，发现、连接、编码、发送、远端执行、响应和恢复
   不能分阶段重置默认超时；
5. Async 回调始终进入 Client owner Service 的串行 FIFO，不尝试回到任意来源 goroutine。

本次不增加 Future、Result Channel、Callback Executor、自动重试或 goroutine ID 检测，也不
新增里程碑编号。

## 2. 生成客户端外观

有响应方法生成：

```go
AwaitXxx(ctx context.Context, args...) (..., error)
CallXxx(ctx context.Context, args...) (..., error)
AsyncXxx(ctx context.Context, args..., callback func(context.Context, ..., error)) error
NotifyXxx(ctx context.Context, args...) error
BroadcastXxx(ctx context.Context, args...) error
```

完全没有返回值的方法继续只生成 `NotifyXxx` 和 `BroadcastXxx`。Go 不支持可选位置参数，
所以调用方仍需写出第一个参数；“Context 可选”表示允许传 `nil`。

使用规则固定为：

| 场景 | 方法 | 完成位置 |
| --- | --- | --- |
| Service Task、Timer、Event、RPC Handler | `AwaitXxx` | 恢复原 Service Task 调用栈 |
| `OnStart`、`OnStop` 中等待已经可调用的外部目标 | `AwaitXxx` | 原生命周期调用栈 |
| 用户创建的普通 goroutine | `CallXxx` | 原调用 goroutine |
| 结果稍后修改 owner Service 状态 | `AsyncXxx` | owner Service 的新串行任务 |
| 不需要业务响应 | `NotifyXxx` / `BroadcastXxx` | 本地提交完成即返回 |

## 3. Context 与 Deadline

Context 只控制取消、Deadline 和普通 Value；Await 执行身份由框架内部关联。所有 `nil` 都
在框架入口规范化，绝不传入下游库。同 Node 本地调用保留 Value，TCP/NATS 不序列化任意
Go Value；跨 Node 业务数据必须进入 RPC 契约参数。

Await、Call 和 Async 每次公开调用先确定候选 Deadline：

```text
调用方显式 Deadline
否则 Service.SetDefaultAwaitTimeout
否则 Node scheduler.default_await_timeout
否则 Origin 内置 15s
```

随后叠加不可绕过的 Service、Node 和 Application 生命周期取消。显式 Deadline 可以短于或
长于默认 15 秒；默认值不是最大上限。Notify 和 Broadcast 没有响应等待，只规范化可选
Context 并观察准备、本地提交前取消，不额外登记默认 15 秒计时项。

### 3.1 Await 的调用链选择

- `AwaitXxx(ctx)` 使用所传 Context；上游只剩 3 秒时，本次最多 3 秒；
- `AwaitXxx(nil)`、`AwaitXxx(context.Background())` 和 `AwaitXxx(context.TODO())`
  不继承上游业务 Deadline，每次重新使用本次默认预算；
- `context.WithTimeout(ctx, 5*time.Minute)` 不能延长一个更早到期的父 Context；
- 需要保留 Value 但主动脱离上游取消时，可以从 `context.WithoutCancel(ctx)` 派生新 Deadline；
- 上述脱离只针对业务调用链，不能绕过 Service/Application 停止。

因此连续三次 `AwaitXxx(nil)` 各自最多等待一次默认时间；若三个步骤需要共享总预算，业务
必须建立一个工作流 Context 并传给三次调用。

### 3.2 一次调用只使用一个绝对 Deadline

一个 `AwaitXxx` 内部即使先等待路由或连接、恢复后编码、再等待响应，也必须复用入口冻结的
同一个绝对 Deadline。不能把一次调用拆成“发现 15 秒 + 连接 15 秒 + 响应 15 秒”。

没有显式 Deadline 时只登记一个框架 Deadline；已有显式 Deadline 时复用调用方 Context，
不重复创建物理 Timer。远端只接收当前绝对 Deadline 的剩余时长。

## 4. Await

Await 从绑定 Client 的 owner Scheduler 捕获当前普通 Task 或生命周期执行帧：

1. 校验 owner、当前执行帧、阶段、代次和执行槽；
2. 冻结本次调用 Context 和 Deadline；
3. 普通 Task 等待时释放执行槽并启动必要的替补 Runner；
4. 等待完成后把原任务追加到统一 FIFO；
5. 原 goroutine 重新取得执行槽后才向业务返回。

生命周期 Await 保持既有边界：`OnStart` 不提前开放普通业务 Runner，`OnStop` 只排空既有
工作。Await 期间其他普通任务可能修改 Service 状态，恢复后业务必须按需重新校验版本和
前置条件。

生命周期执行权不改变 RPC 目标状态。同 Node 的 Service 在全部 `OnStart` 成功后才统一
进入 Running，停止阶段也会关闭新 RPC 准入；`OnStart`/`OnStop` 不得调用同 Node 业务 RPC。
启动后的本地工作必须登记为 Timer 或其他后续任务。生命周期 Await 仍可等待已经可调用的
外部目标或执行不依赖本地业务 Runner 的等待函数。

普通 goroutine 不得调用 Await。owner 没有活动执行帧时返回带明确消息的
`CodeInvalidArgument`；框架不使用 `runtime.Stack`、goroutine ID 或 unsafe 猜测调用位置。
纯 Go 无法在 owner 恰好忙于另一个 Task 时百分之百识别错误 goroutine，因此正确代码必须
使用 `CallXxx`。

## 5. Call

`CallXxx` 与 Await 复用相同的目标选择、编码、传输、Pending、错误和 Deadline 内核，但：

- 不读取、释放或恢复 Service 执行槽；
- 由当前 goroutine 直接等待响应或 Context 终态；
- 不为等待创建辅助 goroutine；
- 返回后的代码仍运行在调用它的原 goroutine。

Call 可以从任意普通 goroutine 并发调用。若在 Service Task 中调用，它会占住执行槽并可能
导致同 Service 或环形 RPC 死锁，因此生成注释和教程必须明确要求 Service Task 使用 Await。
普通 goroutine 取得结果后不得直接修改 Service 非并发安全状态，应通过现有投递接口重新
进入 owner Service。

## 6. Async、Notify 与 Broadcast

owner Service 处于 Running 时，Async 可以由任意 goroutine 提交，不依赖来源 Task 身份：

- 返回非 nil 表示提交失败，callback 永不执行；
- 返回 nil 后，响应、取消、超时和停止只竞争出一个终态，callback 严格执行一次；
- callback 始终是 owner Service 的新串行任务，不回到来源 goroutine；
- `AsyncXxx(nil/Background/TODO)` 使用独立默认预算，不读取 owner 碰巧正在执行的 Task；
- 需要继承当前调用链时必须显式传入当前 `ctx`。

`OnStart` 等待已可调用的外部目标时使用生命周期 Await；同 Node 工作登记为启动后任务。
`OnStop` 已进入停止边界，不接受新的 Async 工作。Draining 期间仅允许已经接受的当前 Task
使用有效 Task Context 派生完成延续，防止外部 goroutine 在停止后继续增加排空工作。

Notify 和 Broadcast 同样允许任意 goroutine 调用。Context 只约束准备和本地提交阶段；目标
已经接受后不可撤回，不创建响应 Pending。Broadcast 继续保持现有 Retired、部分成功、全部
失败及编码一次规则。

## 7. 生命周期、取消与错误

- Service/Application 停止是所有调用不可突破的硬取消边界；
- Context 取消只表示调用方不再等待，不承诺远端业务回滚；
- 超时后的迟到响应必须释放并丢弃；
- 响应、取消、超时和停止竞争必须严格完成一次；
- 有副作用的业务 RPC 仍需由业务实现幂等和结果查询；
- Await 无活动执行帧时继续使用 `CodeInvalidArgument`，但补充可执行的错误消息，不新增
  仅用于误用诊断的稳定错误码。

## 8. 性能约束

- Await、Call 和 Async 不为每个请求创建辅助 goroutine；
- Context 规范化不使用反射、字符串查找、goroutine ID 或 unsafe；
- 默认 Deadline 只建立一个物理计时项；
- Call 复用现有最小完成状态，不复制 Transport 与 Pending 实现；
- 生成方法保持薄包装；
- Benchmark 必须分别记录 Await、Call、Async 的 `ns/op`、`B/op` 和 `allocs/op`。

## 9. 验收范围

实现必须覆盖：

1. Await 的 nil、Background、TODO、自定义取消、短/长显式 Deadline；
2. 每次 Await 重新默认预算、共享工作流预算和嵌套 RPC Deadline 传播；
3. 一次 Await 的准备与响应阶段共用唯一 Deadline；
4. Call 的同 Node、TCP、NATS、超时、取消、断线、错误、panic 和并发调用；
5. Async 从普通 goroutine 提交、来源 goroutine 退出、owner 串行回调和严格一次完成；
6. Notify/Broadcast 的可选 Context、取消边界和任意 goroutine 提交；
7. OnStart/OnStop 长 Deadline、停止取消和不开放错误业务并发；
8. `go test -race`、全仓测试、生成一致性和相关性能 Benchmark。

## 10. 实施落点

- Service 调用级 Context 在入口冻结唯一绝对 Deadline；默认预算复用 Scheduler M8
  `DeadlineQueue`，无每请求等待辅助 goroutine；
- origingen 为所有有响应方法生成 `CallXxx`，并保证 Prepare、编码、提交与完成共享同一
  调用预算且错误路径幂等清理；
- Async 普通 goroutine 路径在返回成功前预留 owner Service FIFO 完成任务；
- Notify/Broadcast 保持无响应准备热路径，不因可选 Context 引入默认 Timer；
- 同 Node、TCP、NATS、显式长 Deadline、nil Context 和普通 goroutine Async 均由集成
  测试覆盖；详细使用规则见 `../guides/README.md`。
