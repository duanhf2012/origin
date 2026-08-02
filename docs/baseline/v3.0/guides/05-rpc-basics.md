# 05：RPC 基础

## 我要调用同一 Node 内的另一个 Service

先定义合约，并紧邻接口写上 `//origin:rpc`：

```go
//origin:rpc
type PlayerService interface {
    GetPlayer(context.Context, int64) (string, error)
    Refresh(context.Context, int64)
}
```

执行生成器后，业务 Service 使用默认绑定即可：

```go
s.players = tutorialrpc.BindPlayerService(s)
player, err := s.players.AwaitGetPlayer(ctx, playerID)
```

直接运行示例：[合约、生成与 Bind](../../../../examples/05-rpc-basics/01-contract-generate-bind)。

```text
examples\05-rpc-basics\01-contract-generate-bind\generate.bat
examples\05-rpc-basics\01-contract-generate-bind\run.bat
```

RPC 合约与业务实现放在不同包中，并使用相同领域名称：合约包的 `PlayerService` 描述远程能力，业务包的 `PlayerService` 提供实现。Go 接口不使用 `I` 前缀。模板中的 Service 实际改名时，使用生成的 `BindPlayerServiceTo` 指定实际 Service 名。

业务实现只需正常定义并加一条可选但推荐的编译期断言：

```go
type PlayerService struct{ service.Service }

var _ tutorialrpc.PlayerService = (*PlayerService)(nil)
```

不需要给业务 Service 加生成标记或生成适配文件。生成文件只属于契约包，并与声明源文件一一对应：`player_service.go` 生成 `player_service.rpc.gen.go`，其中包含客户端、静态 Dispatcher 和冷启动描述符。

Node 按配置中的模板名自动关联契约。例如 `player-1:PlayerService` 中，`PlayerService` 用于冷启动关联，`player-1` 才是配置、发现和 RPC 路由使用的实际名。框架不提供 `SetName`；实际名只有配置这一个来源。需要调用改名实例时：

```go
s.players = tutorialrpc.BindPlayerServiceTo(s, "player-1")
```

## 我要异步调用或只发送通知

```go
client.AsyncGetPlayer(ctx, playerID, func(ctx context.Context, value string, err error) {
    // 回调回到调用方 Service 的后续串行任务中执行。
})
client.NotifyRefresh(ctx, version)
```

直接运行示例：[Async 与 Notify](../../../../examples/05-rpc-basics/02-async-and-notify)。

`Async` 只表示请求已提交，结果在回调中处理；`Notify` 不等待也不返回业务结果，适合可容忍单向语义的通知。

## 深入一点：何时选择哪种调用

- `Await`：当前业务必须依赖返回值时使用；传入当前 Service 的任务或生命周期 Context。
- `Async`：需要继续处理其他工作，结果可在回调中消费时使用。
- `Notify`：无需返回值，可接受远端业务错误仅在目标诊断侧记录时使用。

不要手写 `rpc.Client` 编解码逻辑。`origingen` 生成强类型客户端、静态 Dispatcher、默认 Service 绑定和冷启动描述符；Node 装配完成后，RPC 热路径不会再查描述符、反射或做接口匹配。下一章将相同客户端扩展到其他 Node。
