# RPC 基础示例

本章演示同一 Node 内的强类型 RPC。两个示例共用
[`_support/tutorialrpc`](../_support/tutorialrpc/player_service.go) 中的契约和生成代码，
业务实现仍放在各自目录，不生成适配文件。

建议按顺序运行：

- [合约、生成与 Bind](./01-contract-generate-bind/README.md)：Await 与普通 goroutine Call；
- [Async、Notify 与 Broadcast](./02-async-and-notify/README.md)：异步结果和单向投递。

Windows 执行目录内的 `run.bat`，Linux 执行 `./run.sh`。

## 1. 定义契约与实现

共享契约包只声明可调用能力：

```go
//origin:rpc
type PlayerService interface {
    GetPlayer(context.Context, int64) (string, error)
    Refresh(context.Context, int64)
}
```

业务 Service 正常实现接口，并用编译期断言防止漏方法：

```go
type PlayerService struct{ service.Service }

var _ tutorialrpc.PlayerService = (*PlayerService)(nil)
```

调用方通常在 `OnInit` 绑定一次轻量客户端：

```go
func (target *CallerService) OnInit() error {
    target.players = tutorialrpc.BindPlayerService(target)
    return nil
}
```

默认绑定实际名 `PlayerService`。若配置使用 `player-1:PlayerService`，调用方改用
`BindPlayerServiceTo(target, "player-1")`；右侧仍是关联契约的模板名。

## 2. 选择调用方式

| 场景 | API | 完成位置 |
| --- | --- | --- |
| Service Task、Timer、Event、RPC Handler | `AwaitXxx` | 原 Service 调用栈恢复后返回 |
| 普通 goroutine 需要原地结果 | `CallXxx` | 当前 goroutine |
| 当前流程先继续，稍后处理结果 | `AsyncXxx` | owner Service 的后续串行任务 |
| 只通知一个目标 | `NotifyXxx` | 目标接受提交即返回 |
| 通知当前范围内全部匹配目标 | `BroadcastXxx` | 返回全成、部分失败或全失败 |

Service 执行链中不要使用 `CallXxx`，否则会占住唯一执行槽；普通 goroutine 不要使用
`AwaitXxx`。Async 返回非 nil 时 callback 不会执行；返回 nil 后 callback 严格执行一次，
并始终进入绑定 owner Service 的串行队列。

生命周期回调本身可以使用 Await，但同 Node 的 Service 在全部 `OnStart` 成功后才统一进入
Running，停止时也会关闭新 RPC 准入；不要在 `OnStart`/`OnStop` 调用同 Node 业务 RPC。
启动后工作可像示例一样登记零延迟 Timer 或其他后续任务。

有响应的 Await、Call 和 Async 共享同一套 Deadline 规则：优先使用显式 Deadline，否则依次
使用 Service、Node 和内置 15 秒默认值。Notify 与 Broadcast 不等待响应，不建立默认响应
Timer。所有方法都接受 nil Context；nil 只表示使用相应默认规则，不授予 Service 执行权。
Context Value 只在同进程调用链中保留，不会通过 TCP/NATS 自动传到其他 Node；跨 Node 必需
的数据应声明为 RPC 参数。

## 3. 更新生成代码

修改契约后，在仓库根目录执行：

```bash
go generate ./examples/_support/tutorialrpc
go test ./...
go run ./cmd/origingen rpc --check ./...
```

也可以直接运行两个示例目录中的 `generate.bat` 或 `generate.sh`。Go 的 `./...` 会跳过名称
以 `_` 开头的目录，因此本仓的 `_support` 必须显式指定。生成的 `*.rpc.gen.go` 应提交到
Git，但不要手工修改。

## 4. 继续阅读

- [RPC 基础教程](../../docs/baseline/v3.0/guides/06.rpc-basics.md)：契约目录、安装方式和 CI；
- [RPC Context 与调用规则](../../docs/maintenance/v3.1/guides/README.md)：Deadline、取消和执行权边界；
- [跨节点 RPC](../../docs/baseline/v3.0/guides/07.remote-rpc.md)：复用相同客户端连接其他 Node。
