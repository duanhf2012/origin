# 显式包含 Retired 实例

默认的自动路由只选择 Running 实例，避免新请求流入正在退休的业务。只有业务明确规定 Retired 实例仍可处理某类请求时，才应显式派生 `IncludeRetired()` 客户端。

## 示例流程

示例先退休同 Node 的 `PlayerService`，再由调用方执行：

```go
value, err := s.players.IncludeRetired().AwaitGetPlayer(ctx, playerID)
```

精确 `OnNode` 与自动选择的语义不同；本例强调的是扩展自动候选范围。

## 运行与练习

执行 `run.bat` 或 `./run.sh`，确认退休后仍能在显式包含时调用成功。删除 `IncludeRetired()` 后应按默认规则没有候选；不要把它作为常规兜底重试。

对应教程：[Retire、Resume 与优雅停止](../../../docs/baseline/v3.0/guides/08-retire-and-resume.md)。
