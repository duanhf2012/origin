# 08：Retire、Resume 与优雅停止

## 我想暂时下线一个 Service

运行：[examples/08-retire-and-resume/01-service-retire-resume](../../../../examples/08-retire-and-resume/01-service-retire-resume)。

```go
if err := s.Retire(ctx); err != nil { /* 处理错误 */ }
// 处理维护操作后：
if err := s.Resume(ctx); err != nil { /* 处理错误 */ }
```

`Retire` 把 Service 从 Running 切换到 Retired，并等待发现发布确认；它不是停止，不会调用 `OnStop`。

## 我想退休一个 Node 或整个 Application

运行：[examples/08-retire-and-resume/02-node-and-application](../../../../examples/08-retire-and-resume/02-node-and-application)。

```go
node.Retire(ctx) // Service 倒序
app.Retire(ctx)  // Node 倒序
```

恢复使用正序 `Resume`，让上游依赖先恢复可用。

## 我仍需要精确调用一个 Retired Service

运行：[examples/08-retire-and-resume/03-include-retired](../../../../examples/08-retire-and-resume/03-include-retired)。默认路由排除 Retired；精确 `OnNode` 目标原本就可以命中，自动选择时可显式使用 `IncludeRetired()`。

该示例的 `PlayerService` 是业务目录中的普通结构体，只通过模板名与共享 RPC 契约自动
关联。Retire/Resume 不重新生成或重新匹配契约，也不改变实际 ServiceName；它们只更新
准入状态和发现状态。

## 深入一点

退休是发现状态和本地准入状态的组合，而非延迟删除。Retire/Resume 不会回滚已经提交的本地状态：通知或发布失败会作为结果返回，由业务决定重试、告警或继续维护流程。
