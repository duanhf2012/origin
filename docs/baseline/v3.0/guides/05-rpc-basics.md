# 05：RPC 基础

## 我要调用同一 Node 内的另一个 Service

先定义合约，并紧邻接口写上 `//origin:rpc`：

```go
//origin:rpc
type PlayerRPC interface {
    GetPlayer(context.Context, int64) (string, error)
    Refresh(context.Context, int64)
}
```

执行生成器后，业务 Service 使用默认绑定即可：

```go
s.players = tutorialrpc.BindPlayerRPC(s)
player, err := s.players.AwaitGetPlayer(ctx, playerID)
```

直接运行示例：[合约、生成与 Bind](../../../../examples/05-rpc-basics/01-contract-generate-bind)。

```text
examples\05-rpc-basics\01-contract-generate-bind\generate.bat
examples\05-rpc-basics\01-contract-generate-bind\run.bat
```

默认绑定名来自合约 `PlayerRPC`，对应 `PlayerService`。模板中的 Service 实际改名时，使用生成的 `BindPlayerRPCTo` 指定实际 Service 名。

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

不要手写 `rpc.Client` 编解码逻辑。`origingen` 生成强类型客户端、静态 Dispatcher 和默认 Service 绑定；下一章将相同客户端扩展到其他 Node。
