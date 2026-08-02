# Async 与 Notify

本示例复用共享 `PlayerRPC`，对比两种不阻塞当前业务流程的调用方式：`AsyncGetPlayer` 在回调中收到结果，`NotifyRefresh` 只提交单向通知，不等待业务返回值。

## 关键代码

`CallerService` 在 Timer 回调中提交两个请求。Async 回调仍回到调用方 Service 的后续串行任务中，因此可安全更新调用方自己的状态；Notify 只报告提交或传输层错误，目标方法没有业务返回值。

## 运行与观察

直接执行 `run.bat` 或 `./run.sh`；共享生成代码需要更新时运行 `generate.bat`。预期会看到 async 查询结果和 `PlayerService` 的 refresh 日志。

## 何时使用

需要返回值且后续工作可以继续时选 Async；只需告知目标、允许不等待结果时选 Notify。若当前步骤必须使用返回值，改用上一示例的 Await 方法，而不是在回调中拼接同步流程。

对应教程：[RPC 基础](../../../docs/baseline/v3.0/guides/05-rpc-basics.md)。
