# 同步本地事件 Await 语义变更摘要

- 状态：已实施
- 目标版本：v3.1.0
- 兼容性：放宽原有限制，公开 API 与 RPC 线协议不变
- 依赖基线：v3.0

## 使用者可见变更

- `NotifyEventSync` 监听器现在可以调用通用 `Await`、生成的 `AwaitXxx` RPC，以及 `Retire`/`Resume` 这类内部需要 Await 的 Service 操作。
- `NotifyEventSync` 仍等待所有监听器完成，监听器仍按注册顺序执行。
- Await 期间同 Service 其他已就绪任务可以执行；原监听器恢复并返回后，才继续下一监听器。
- 等待函数不得读写 Service 普通可变字段；只保存局部结果，Await 返回后再更新 Service 状态。

## 实现变更

- 删除 Service Scheduler 对“同步事件嵌套深度非零”的 Await 拒绝。
- 嵌套深度跟随原 Task 穿越 Waiting 和 RecoveryReady，恢复后仍由原 defer 对称清理。
- 删除 `Retire`/`Resume` 的遗留同步事件预拒绝。

## 验证要求

回归测试覆盖同步监听器 Await、Await 期间任务插入、监听器恢复顺序、嵌套深度、错误/panic 聚合和同步监听器中的 Retire。
