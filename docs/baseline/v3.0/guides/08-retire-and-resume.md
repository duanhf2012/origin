# 08：Retire、Resume 与优雅停止

## 我想让进程以 Retired 状态启动

运行：[examples/09-retire-and-resume/01-service-retire-resume](../../../../examples/09-retire-and-resume/01-service-retire-resume)。其完整启动命令包含：

```text
# 全部选中 Node 的 Service 正常执行 OnInit/OnStart，但首次对外发布就是 Retired。
game-server start --app-name game --config ./config --node game-1 --retired
```

`--retired` 适合先启动管理、预热和恢复能力，再由业务确认后调用 `Resume(ctx)` 接收默认流量。
不传时初始状态为 `Running`。该参数不是“在线退休另一个已运行进程”的控制命令，也不会跳过
生命周期：它只决定本次 `start` 所选全部 Service 的初始发布状态。

## 我想暂时下线一个 Service

```go
// 停止接收自动路由的新流量，并等待发现状态发布确认。
if err := s.Retire(ctx); err != nil {
    // 本地状态可能已经提交；记录错误并按业务策略重试发布或告警。
    return err
}
// 完成维护后恢复 Running 状态与默认流量准入。
if err := s.Resume(ctx); err != nil {
    // 恢复失败时不要假定已经重新进入默认候选。
    return err
}
```

`Retire` 把 Service 从 Running 切换到 Retired，并等待发现发布确认；它不是停止，不会调用
`OnStop`。在已经 Retired 时重复调用 `Retire`、在 Running 时重复调用 `Resume`都幂等成功。

## 我想退休一个 Node 或整个 Application

运行：[examples/09-retire-and-resume/02-node-and-application](../../../../examples/09-retire-and-resume/02-node-and-application)。

```go
// Node 内按 Service 启动顺序的倒序退休。
if err := node.Retire(ctx); err != nil {
    return err
}
// Application 内按 Node 启动顺序的倒序退休。
if err := app.Retire(ctx); err != nil {
    return err
}
```

恢复使用正序 `Resume`，让被依赖对象先恢复可用。批量操作是 best-effort：一个对象失败不会
跳过后续对象，返回值会聚合全部错误。

## 我仍需要精确调用一个 Retired Service

运行：[examples/09-retire-and-resume/03-include-retired](../../../../examples/09-retire-and-resume/03-include-retired)。默认自动路由排除 Retired；精确 `OnNode` 目标原本就可以命中，自动选择时可显式使用 `IncludeRetired()`。

该示例的 `PlayerService` 是业务目录中的普通结构体，只通过模板名与共享 RPC 契约自动
关联。Retire/Resume 不重新生成或重新匹配契约，也不改变实际 ServiceName；它们只更新
准入状态和发现状态。

## 深入一点

退休是发现状态和本地准入状态的组合，而非延迟删除。Retire/Resume 不会回滚已经提交的本地
状态：通知或发布失败会作为结果返回，由业务决定重试、告警或继续维护流程。

Retired 仍会运行 Timer、本地事件、Await、Async 回调和后台任务，也允许精确 `OnNode` RPC；
框架只从默认自动单目标与 Broadcast 候选中排除它。退休不会自动排空任务、关闭 TCP/NATS、
暂停 Timer 或调用 `OnStop`。需要拒绝某类入站业务时，由业务根据 `State()` 主动返回
`errs.ErrServiceRetired`；维护、存档和恢复类接口仍可继续工作。
