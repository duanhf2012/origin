# RPC 基础示例

本章先解决“怎样调用”，再解释 Await、Call、Async、Notify 和 Broadcast 在 Service
调度器中的真实行为。两个示例共用 `_support/tutorialrpc` 中的 RPC 契约和
`*.rpc.gen.go`，但各自在当前业务目录定义普通 `PlayerService`；业务实现目录不会生成
适配文件。

建议按顺序运行：

- [01-contract-generate-bind](./01-contract-generate-bind/README.md)：契约、代码生成、Bind、Await 与 Call。
- [02-async-and-notify](./02-async-and-notify/README.md)：Async 回调、Notify 与 Broadcast。

## 1. 先定义契约和绑定客户端

共享包只声明其他 Service 可以调用的 RPC 能力：

```go
//origin:rpc
// PlayerService 是公开 RPC 契约，不是业务 Service 结构体。
type PlayerService interface {
    // GetPlayer 是需要业务结果的请求—响应方法。
    GetPlayer(context.Context, int64) (string, error)
    // Refresh 没有返回值，适合 Notify 或 Broadcast。
    Refresh(context.Context, int64)
}
```

修改契约后运行 `origingen`。生成代码提供强类型客户端、静态编解码和 Dispatcher；业务
实现只需实现接口，并建议保留编译期断言：

```go
type PlayerService struct {
    // 嵌入框架 Service，获得生命周期和串行执行能力。
    service.Service
}

// 编译期检查业务方法是否完整满足共享契约，不产生运行时开销。
var _ tutorialrpc.PlayerService = (*PlayerService)(nil)
```

调用方通常在 `OnInit` 绑定一次轻量客户端。绑定不会发起 RPC，也不会建立专属连接：

```go
type CallerService struct {
    service.Service
    // 生成客户端是可长期复用的轻量值。
    players tutorialrpc.PlayerServiceClient
    // 以下字段用于展示 RPC 完成后安全更新调用方状态。
    lastPlayer           string
    lastSubmittedRefresh int64
}

func (s *CallerService) OnInit() error {
    // 默认绑定实际名 PlayerService；这里只保存逻辑目标。
    s.players = tutorialrpc.BindPlayerService(s)
    return nil
}
```

若配置使用 `player-primary:PlayerService`，右侧 `PlayerService` 是关联契约的模板名，左侧
`player-primary` 是发现、配置和路由使用的实际名。调用方改为：

```go
// 显式绑定模板改名后的实际 ServiceName。
s.players = tutorialrpc.BindPlayerServiceTo(s, "player-primary")
```

## 2. 先按调用位置选择 API

| 调用位置或目的 | 推荐 API | 结果在哪里继续 |
| --- | --- | --- |
| Service Task、Timer、异步 Event、RPC Handler | `AwaitXxx` | 原 Service Task 恢复执行权后直接返回 |
| `OnStart` / `OnStop` | `AwaitXxx` | 原生命周期调用栈 |
| `go func()`、测试 goroutine 或其他普通 goroutine | `CallXxx` | 调用它的同一个 goroutine |
| 当前代码先继续，结果稍后修改 owner Service 状态 | `AsyncXxx` | owner Service 的后续串行任务 |
| 不需要业务响应 | `NotifyXxx` / `BroadcastXxx` | 本地提交完成即返回 |

最容易记住的规则是：Service 执行链中用 Await，普通 goroutine 中用 Call。两者使用相同
契约、路由、传输、错误和 Deadline 内核，区别只是是否释放并恢复 Service 执行槽。

## 3. Await：Service 中顺序等待结果

```go
func (s *CallerService) Load(ctx context.Context, playerID int64) error {
    // 推荐传当前业务 ctx：它会继承上游取消、Deadline 和普通 Value。
    player, err := s.players.AwaitGetPlayer(ctx, playerID)
    if err != nil {
        // 超时、取消、路由、传输、远端 panic 和业务错误都从这里返回。
        return err
    }

    // Await 返回时 CallerService 已重新取得执行权，可安全修改自己的状态。
    s.lastPlayer = player
    return nil
}
```

Await 的执行身份来自所绑定 owner 当前的 Service Task 或生命周期帧，不再依赖业务
Context 携带框架私有令牌。因此在有效 Service 执行链中，以下写法都允许：

```go
// 使用当前调用链：通常最推荐，可随上游停止或超时。
player, err := s.players.AwaitGetPlayer(ctx, playerID)

// 不传控制 Context：本次调用获得一份新的默认预算。
player, err = s.players.AwaitGetPlayer(nil, playerID)

// Background 和 TODO 同样表示不继承上游业务 Deadline，使用新的默认预算。
player, err = s.players.AwaitGetPlayer(context.Background(), playerID)
player, err = s.players.AwaitGetPlayer(context.TODO(), playerID)
```

`nil`、Background 和 TODO 只放宽 Context 控制参数，不会让普通 goroutine 获得 Service
执行权。普通 goroutine 必须改用 `CallXxx`。纯 Go 无法在 owner 恰好执行另一个 Task 时
可靠判断当前 goroutine 身份，Origin 不使用 goid、`runtime.Stack` 或 unsafe 猜测它。

### 3.1 Deadline 的精确规则

对 Await、Call 和 Async 这三种有响应调用，规则如下：

1. 传入 Context 有显式 Deadline 时，完整继承该 Deadline；
2. 没有显式 Deadline时，使用 Service 覆盖值；
3. Service 未覆盖时，使用 Node 的 `scheduler.default_await_timeout`；
4. Node 未覆盖时，使用 Origin 内置默认值 **15 秒**；
5. Service、Node 或 Application 的生命周期停止始终是不可绕过的硬边界。

```go
// 上游只剩 3 秒，本次 Await 最多也只有 3 秒；框架不会重新补成 15 秒。
player, err := s.players.AwaitGetPlayer(ctx, playerID)

// 显式 5 分钟会覆盖默认 15 秒，适合确实需要长时间完成的特殊调用。
longCtx, cancel := context.WithTimeout(ctx, 5*time.Minute)
defer cancel()
player, err = s.players.AwaitGetPlayer(longCtx, playerID)
```

`context.WithTimeout(ctx, 5*time.Minute)` 仍不能延长一个更早到期的父 Context。例如父
Context 只剩 3 秒，子 Context 最终仍在约 3 秒后结束。若业务明确要脱离上游业务取消，
可在审慎评估后使用：

```go
// 保留 ctx 中的 Value，但主动脱离它的取消和 Deadline。
detachedParent := context.WithoutCancel(ctx)
longCtx, cancel := context.WithTimeout(detachedParent, 5*time.Minute)
defer cancel()

// 仍然不能绕过所属 Service/Application 的停止边界。
player, err := s.players.AwaitGetPlayer(longCtx, playerID)
```

连续三次 `AwaitXxx(nil)` 是三次独立公开调用，因此各自得到一份新的默认预算。如果三个
步骤必须共享一个总预算，应显式建立工作流 Context：

```go
workflowCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
defer cancel()

// 三个调用共同消耗同一个 20 秒预算，不会各自重新得到 15 秒。
first, err := s.players.AwaitGetPlayer(workflowCtx, firstID)
if err != nil {
    return err
}
second, err := s.players.AwaitGetPlayer(workflowCtx, secondID)
if err != nil {
    return err
}
_, err = s.players.AwaitGetPlayer(workflowCtx, thirdID)
_ = first
_ = second
return err
```

同一次 `AwaitXxx` 内部的发现、等待连接、编码、提交、远端执行、响应和调用方恢复排队
共享一个绝对 Deadline，不会变成“发现 15 秒 + 连接 15 秒 + 响应 15 秒”。

### 3.2 OnStart 和 OnStop

```go
func (s *CallerService) OnStart(ctx context.Context) error {
    // 推荐传生命周期 ctx，使启动取消和显式 Deadline 能向下传播。
    player, err := s.players.AwaitGetPlayer(ctx, initialPlayerID)
    if err != nil {
        // OnStart 返回错误会触发既定启动失败和回滚流程。
        return err
    }
    s.lastPlayer = player
    return nil
}
```

在 OnStart 中也可以传 nil、Background、TODO，或显式 `WithTimeout(..., 5*time.Minute)`；
它们会按上述规则工作，但无理由脱离启动 ctx 会降低应用停止和启动回滚的响应性，所以仍
推荐传入框架给出的 ctx。

`OnInit()` 没有 Context，而且 RPC Runtime、Transport 和发现条件尚未开放。OnInit 只做
配置解析、客户端绑定和静态资源登记；依赖其他服务的初始化放到 `OnStart`。

### 3.3 协作式调度原理

1. Await 从绑定 owner 捕获当前 Service 执行帧并冻结一次调用 Deadline；
2. 普通 Task 原子释放唯一执行槽，原 goroutine 直接等待，不创建等待辅助 goroutine；
3. 替补 Runner 可以继续逐个处理同一 Service 的其他任务；
4. RPC 结束后，原任务追加到统一 FIFO；
5. 原 goroutine 重新取得执行槽后，Await 才向业务返回。

Await 是明确的业务让出点。等待期间其他任务可能修改 Service 状态，因此调用前不要留下
“半完成”状态；跨 Await 的输入应复制为局部值，恢复后按需重新检查版本和前置条件。同步
同步事件监听器可以调用 `AwaitXxx`；Await 期间会释放所属 Service 执行权，恢复后才继续当前监听器和后续监听器。

## 4. Call：普通 goroutine 阻塞等待

```go
go func() {
    // nil 表示这次 Call 使用 Service/Node/内置 15 秒默认预算。
    player, err := s.players.CallGetPlayer(nil, playerID)
    if err != nil {
        // 在这个普通 goroutine 中处理最终错误。
        return
    }

    // Call 返回仍在当前 goroutine；不要直接修改非并发安全的 Service 状态。
    _ = s.DispatchAsync(func(context.Context) {
        // 重新进入 owner Service 串行队列后再修改状态。
        s.lastPlayer = player
    })
}()
```

Call 不读取、释放或恢复 Service 执行槽，也不会为等待创建辅助 goroutine。它可以从多个
普通 goroutine 并发调用。不要在 Service Task、Timer、Event、RPC Handler 或生命周期
回调中用 Call：它会占住唯一执行槽，同 Service RPC 或环形调用可能因此等待到 Deadline；
这些位置应使用 Await。

## 5. Async：稍后回到 owner Service

```go
err := s.players.AsyncGetPlayer(
    ctx,
    playerID,
    func(_ context.Context, player string, callErr error) {
        // callback 始终进入 CallerService 的后续串行任务。
        if callErr != nil {
            s.Logger().Error("async rpc failed")
            return
        }
        // 此处可以安全修改 CallerService 状态。
        s.lastPlayer = player
    },
)
if err != nil {
    // 立即提交失败：请求未被接受，callback 永远不会执行。
    return err
}

// 返回 nil 后当前代码继续；最终结果或错误由 callback 严格交付一次。
```

Async 可以从正在运行的 Service Task或任意普通 goroutine 调用，`ctx` 也允许 nil、
Background、TODO 和自定义 Context。无显式 Deadline时，响应等待使用同一套默认 15 秒链。
`OnStart` 需要顺序结果时使用 Await；`OnStop` 已进入停止边界，不应再创建新的 Async 工作。

```go
go func() {
    // 来源 goroutine 可以在提交后结束；callback 不会尝试“回到”这个 goroutine。
    _ = s.players.AsyncGetPlayer(nil, playerID, func(
        _ context.Context,
        player string,
        err error,
    ) {
        // callback 仍在 s 所属 Service 的串行 FIFO 中。
        if err == nil {
            s.lastPlayer = player
        }
    })
}()
```

如果必须让结果回到来源 goroutine 的同一调用栈，应直接用 Call。不要在 Service Task 中
用 Async 后阻塞 Channel 等回调，这会占住执行槽并可能造成死锁。

## 6. Notify 与 Broadcast：只提交，不等业务结果

```go
// nil、Background、TODO 和自定义 Context 都允许。
if err := s.players.NotifyRefresh(nil, version); err != nil {
    // 这里只能得到参数、路由、队列或本地 Transport 接受阶段错误。
    return err
}

// 返回 nil 不表示目标 Refresh 已经执行成功。
s.lastSubmittedRefresh = version
```

Notify 和 Broadcast 不创建响应 Pending，也没有业务结果或远端业务错误返回通道。Context
只约束准备、编码和本地提交阶段；目标接受后不能撤回。因为没有响应等待，nil、Background
和 TODO 不会额外建立一个 15 秒 Timer，保持通知准备热路径轻量。Broadcast 仍遵守发现
范围、Retired 选择、部分成功和编码一次规则。

若业务必须确认目标处理成功、需要返回值，或失败后必须立即补偿，应定义请求—响应方法并
使用 Await、Call 或 Async。

## 7. Context 与调用方式总表

| API | nil / Background / TODO | 自定义 Deadline | 调用位置 | 完成语义 |
| --- | --- | --- | --- | --- |
| `AwaitXxx` | 允许；每次新建默认预算 | 完整继承，可长于 15 秒 | 必须有 owner Service Task/生命周期帧 | 恢复原 Service 调用栈 |
| `CallXxx` | 允许；每次新建默认预算 | 完整继承，可长于 15 秒 | 普通 goroutine | 返回同一 goroutine |
| `AsyncXxx` | 允许；每次新建默认预算 | 完整继承，可长于 15 秒 | owner Running 时的任意 goroutine | callback 进入 owner Service FIFO |
| `NotifyXxx` | 允许；不建响应预算 | 约束本地提交前阶段 | 任意 goroutine | 接受后不可撤回 |
| `BroadcastXxx` | 允许；不建响应预算 | 约束本地提交前阶段 | 任意 goroutine | 返回全成、部分失败或全失败 |

所有 Context 都只负责取消、Deadline 和普通 Value，不再证明 Service 执行身份。Context
取消表示调用方不再等待，不等于目标业务已经回滚；有副作用的 RPC 仍需业务设计幂等键、
结果查询或补偿流程。

`GoSafe`/`RunSafe` 中运行的是普通 goroutine，所以请求—响应调用使用 Call 或 Async，不用
Await；只发通知时可直接用 Notify/Broadcast。

## 8. 示例与下一步

- [契约、生成、绑定、Await 与 Call](./01-contract-generate-bind/README.md)
- [Async、Notify 与 Broadcast](./02-async-and-notify/README.md)
- [v3.1 完整 RPC 调用规则](../../docs/maintenance/v3.1/guides/README.md)
- [跨节点 RPC](../../docs/baseline/v3.0/guides/07.remote-rpc.md)

同 Node、TCP 和 NATS 使用完全相同的生成客户端外观；跨节点章节只增加发现、路由和传输
配置，不改变本章的 Await、Call、Async、Notify 与 Broadcast 语义。
