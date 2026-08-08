# Async 与 Notify

本示例复用共享 `PlayerService` RPC 合约，对比两种不阻塞当前业务流程的调用方式：`AsyncGetPlayer` 在回调中收到结果，`NotifyRefresh` 只提交单向通知，不等待业务返回值。

## 关键代码

`CallerService` 在 Timer 回调中提交两个请求。Async 回调仍回到调用方 Service 的后续串行任务中，因此可安全更新调用方自己的状态；Notify 只报告提交或传输层错误，目标方法没有业务返回值。

- [`../../_support/tutorialrpc/player_service.go`](../../_support/tutorialrpc/player_service.go)：共享 RPC 契约。
- [`../../_support/tutorialrpc/player_service.rpc.gen.go`](../../_support/tutorialrpc/player_service.rpc.gen.go)：生成客户端、静态 Dispatcher 和冷启动描述符。
- [`player_service.go`](player_service.go)：本示例自己的业务实现，只保留编译期接口断言，不生成适配文件。
- [`main.go`](main.go)：绑定客户端并演示 Async 与 Notify。

Node 使用业务类型的模板名 `PlayerService` 自动关联共享契约；这一匹配只发生在冷启动，
Async 和 Notify 热路径不查描述符。

## 运行与观察

直接执行 `run.bat` 或 `./run.sh`；共享生成代码需要更新时运行 `generate.bat`。预期会看到 async 查询结果和 `PlayerService` 的 refresh 日志。

## 何时使用

需要返回值且后续工作可以继续时选 Async；只需告知目标、允许不等待结果时选 Notify。若
Service 当前步骤必须使用返回值，改用 Await；普通 goroutine 需要原地得到结果时用 Call，
不要阻塞 Channel 等 Async 回调。

Async、Notify 都允许 nil、Background、TODO 和自定义 Context。Async 没有显式 Deadline
时使用 Service/Node/内置 15 秒预算；Notify 没有响应 Pending，不额外建立 15 秒 Timer。
无论 Async 从哪个 goroutine 提交，回调都进入绑定 owner Service 的后续串行 FIFO。

完整规则：[RPC 基础](../README.md)；v3.1 变更索引见
[RPC 调用与 Context](../../../docs/maintenance/v3.1/guides/README.md)。
