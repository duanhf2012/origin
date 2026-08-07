# 显式包含 Retired 实例

默认的自动路由只选择 Running 实例，避免新请求流入正在退休的业务。只有业务明确规定 Retired 实例仍可处理某类请求时，才应显式派生 `IncludeRetired()` 客户端。

## 示例流程

示例先退休同 Node 的 `PlayerService`，再由调用方执行：

```go
// 派生一个允许 Retired 候选的客户端，基础 s.players 的默认规则不变。
value, err := s.players.IncludeRetired().AwaitGetPlayer(ctx, playerID)
```

精确 `OnNode` 与自动选择的语义不同；本例强调的是扩展自动候选范围。

## 契约与业务实现

- [`../../_support/tutorialrpc/player_service.go`](../../_support/tutorialrpc/player_service.go)：共享 RPC 契约。
- [`../../_support/tutorialrpc/player_service.rpc.gen.go`](../../_support/tutorialrpc/player_service.rpc.gen.go)：生成客户端、Dispatcher 和冷启动描述符。
- [`player_service.go`](player_service.go)：当前示例的业务实现，不生成业务侧适配文件。
- [`main.go`](main.go)：退休目标后使用 `IncludeRetired()` 派生客户端。

Retire 只改变候选状态，不改变冷启动时已经完成的模板名—契约关联。

## 运行与练习

执行 `run.bat` 或 `./run.sh`，确认退休后仍能在显式包含时调用成功。删除 `IncludeRetired()` 后应按默认规则没有候选；不要把它作为常规兜底重试。

对应教程：[Retire、Resume 与优雅停止](../../../docs/baseline/v3.0/guides/08-retire-and-resume.md)。
