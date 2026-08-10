# Origin v3.1 使用变更

> 状态：已实施
> 基线：v3.0
> 目标版本：v3.1.0
> 兼容性：RPC 契约和线协议兼容；新增 `CallXxx`、`NodeRuntime`、`GetNode` 与日志便捷/控制 API，既有方法签名不变

## RPC 调用方式

origingen 对有响应方法生成以下三种外观：

```go
// Service Task、Timer、异步 Event 或 RPC Handler 中使用。
value, err := client.AwaitGetPlayer(ctx, playerID)

// 普通 goroutine 中使用；结果返回到当前 goroutine 的同一调用栈。
value, err = client.CallGetPlayer(ctx, playerID)

// owner Running 时任意 goroutine 都可提交；callback 始终进入 owner Service 串行 FIFO。
err = client.AsyncGetPlayer(ctx, playerID, func(
    _ context.Context,
    value string,
    callErr error,
) {
    // 可以在这里安全修改 owner Service 的状态。
})
```

没有返回值的方法只生成 `NotifyXxx` 和 `BroadcastXxx`。请求—响应方法也保留对应通知
外观，供调用方明确放弃业务结果。

## Context 和 Deadline 规则

`AwaitXxx`、`CallXxx`、`AsyncXxx`、`NotifyXxx` 和 `BroadcastXxx` 都接受 nil、
`context.Background()`、`context.TODO()` 和普通自定义 Context。框架会在入口规范化 nil，
不会把 nil 传入标准库或 Transport。

同 Node 本地调用会保留 Context Value；TCP/NATS 只传播契约参数和剩余 Deadline，不会
序列化任意 Go Value。跨 Node 必需的数据应定义为 RPC 参数。

Await、Call 和 Async 是有响应调用，其 Deadline 规则固定为：

1. 传入 Context 有显式 Deadline 时完整继承；
2. 否则使用 `Service.SetDefaultAwaitTimeout` 覆盖；
3. 否则使用 Node 的 `scheduler.default_await_timeout`；
4. 否则使用内置 15 秒；
5. Service、Node 和 Application 生命周期停止始终是不可绕过的硬边界。

因此：

- 上游只剩 3 秒，`AwaitXxx(ctx)` 或 `CallXxx(ctx)` 最多只剩 3 秒；
- `AwaitXxx(nil/Background/TODO)` 每次公开调用重新取得一份默认预算；
- 显式 `context.WithTimeout(parent, 5*time.Minute)` 不会被默认 15 秒截断；
- 子 Context 不能延长更早到期的 parent；
- `context.WithoutCancel(ctx)` 可以脱离业务 parent 的取消并保留 Value，但仍不能越过框架
  生命周期停止；
- 连续多个 nil 调用各自有默认预算；需要共享总预算时，应把同一个工作流 Context 传给
  所有调用；
- 同一次调用的发现、连接、编码、提交、远端执行、响应与恢复只使用一个绝对 Deadline，
  不会在内部阶段重置 15 秒。

Notify 和 Broadcast 没有响应 Pending。它们的 Context 只约束准备和本地提交前阶段；目标
接受后不可撤回。nil、Background 和 TODO 不会为通知额外创建 15 秒响应 Timer。

## Await 与 Call 的边界

Await 的执行身份来自绑定 owner 当前的 Service Task 或生命周期执行帧，而不是 Context
私有值。传 nil 或 Background 不会让普通 goroutine 获得执行权；普通 goroutine 使用 Call。

生命周期执行帧允许调用 Await，但本地 RPC 目标仍受自身状态约束。同 Node 的全部 Service
在所有 `OnStart` 成功后才统一进入 Running，停止阶段也会关闭新 RPC 准入；因此不要在
`OnStart`/`OnStop` 调用同 Node 业务 RPC。启动后工作应登记为 Timer 或其他后续任务。

Call 不释放 Service 执行槽。在 Service Task 中调用 Call 可能阻塞同 Service 或环形 RPC，
所以 Service 执行链必须使用 Await。Origin 不使用 goid、`runtime.Stack` 或 unsafe 猜测
goroutine 身份。

## Async 回调边界

- Async 返回非 nil：提交失败，业务 callback 永不执行；
- Async 返回 nil：响应、超时、取消和停止只产生一个终态，callback 严格执行一次；
- callback 总在 owner Service 的后续串行任务中，不回来源 goroutine；
- `OnStart` 等待已可调用的外部目标时使用 Await；同 Node 工作登记到启动后任务；
- `OnStop` 不再创建新的 Async 工作；
- 普通 goroutine 若需要结果回到自己的同一调用栈，使用 Call；
- Context 取消表示调用方不再等待，不承诺远端业务回滚。

## 同步本地事件中的 Await

`NotifyEventSync` 监听器可以直接调用通用 `Await` 和生成的 `AwaitXxx` RPC。
调用方仍等到所有监听器按注册顺序完成后才返回；但 Await 期间会释放
所属 Service 执行权，同 Service 的其他已就绪任务可以插入。恢复后应重新校验
Await 前观察的可变状态，不要在等待函数中读写 Service 普通成员。

完整代码、Await RPC 片段和可运行示例见
[Timer、Event 与执行](../../../baseline/v3.0/guides/05.timer-event-and-execution.md)。内部语义见
[同步本地事件 Await 语义设计](../design/同步本地事件Await语义设计.md)。

## 完整教程和示例

完整讲解、代码片段和可运行路径见
[RPC 基础示例](../../../../examples/06-rpc-basics/README.md)。确认后的内部设计与验收边界见
[RPC 可选 Context 与 goroutine 调用设计](../design/RPC可选Context与goroutine调用设计.md)。

## Node 游戏逻辑时间

v3.1 为每个 Node 新增独立的游戏逻辑时间。Service 和 Module 通过 `GetNode()`
读取当前所属 Node，并可以使用 `Now`、`SetTime` 和 `AddTime`。时间跳跃会统一
重排当前 Node 的 After、Ticker 和 Cron，但不影响 RPC/Await/Context 等基础设施 Deadline。

完整用法、跳跃规则和可运行示例见 [Node 游戏逻辑时间](./node-game-time.md)。

## 03 日志输出与管理

v3.1 新增包级 `log.Xxx`、Module Logger、Console/File 独立归属字段、可读文本格式、
Application 文件名前缀，以及运行时独立调整级别和暂停/恢复输出。完整配置、输出样例、
错误边界、自定义 Handler 与可运行程序见 [日志：调用、格式、滚动与运行时控制](./03.logging.md)。

## 10 Admin 管理 HTTP、Diagnostics 与 pprof

v3.1 用唯一的 `--admin` 入口承载 Diagnostics、固定生命周期控制，以及 Application/Service
自定义 GET/POST Endpoint；Service 回调会进入目标 Service 的串行执行槽。Diagnostics 默认返回
低基数 Summary schema v2，`detail=full` 返回兼容的 Full Snapshot v2；pprof 继续使用独立、
可运行期启停的 Listener。完整教程与七组示例见
[Admin 管理 HTTP、Diagnostics 与 pprof](./10.admin-diagnostics-and-pprof.md)。
