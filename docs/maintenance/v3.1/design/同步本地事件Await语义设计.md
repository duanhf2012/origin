# 同步本地事件 Await 语义设计

- 状态：已确认
- 目标版本：v3.1.0
- 兼容性：放宽原有限制，不新增公开 API
- 依赖基线：v3.0

## 目标

`NotifyEventSync` 的监听器可以直接调用通用 `Await` 和生成的 `AwaitXxx` RPC，不再因“处于同步事件回调”返回 `ErrInvalidArgument`。

## 最终语义

1. `NotifyEventSync` 仍在当前 Service Task 调用栈中按注册顺序执行监听器。
2. 所有监听器完成后，`NotifyEventSync` 才返回。单个监听器的 error 和 panic 仍按原规则聚合并继续后续监听器。
3. 监听器调用 `Await` 时，整个原 Service Task 连同同步事件嵌套深度一起进入 Waiting，释放唯一 Service 执行槽。
4. 等待期间，同一 Service 可以处理已就绪的其他任务。Await 完成后，原 Task 仍通过统一 FIFO 恢复项重新取得执行权，再从原调用栈继续。
5. 监听器顺序不变，但“同步”不表示从通知开始到返回期间绝对不可插入其他 Service Task。
6. 同步事件仍只能从持有所属 Service 执行权的正常 Task 触发；外部 goroutine 不会自动退化为异步事件。
7. 嵌套同步事件最大深度仍为 64。

## 业务数据规则

Await 的等待函数运行时已释放 Service 执行权，不得在等待函数中读写 Service 的普通可变字段。应把 I/O 结果写入局部变量，等 Await 恢复并返回后再读写 Service 状态。Await 期间其他 Service Task 可能已改变业务状态，依赖 Await 前快照的代码需要在恢复后重新校验。

## 实现约束

- 不为同步事件创建第二套等待或续传调度机制；复用已有 Service Task Await 交接。
- `syncEventDepth` 是原 Task 的调用栈状态，在 Waiting 和 RecoveryReady 期间保留，恢复后由 `NotifyEventSync` 的 defer 对称减少。
- 生成的 `AwaitXxx` RPC 已经最终委托所属 Service Await，不增加生成代码分支。
- `Retire`/`Resume` 等内部使用 Await 的 Service 操作同样不再因同步事件帧被预先拒绝。

## 验收

- 同步监听器中的通用 Await 成功完成。
- Await 期间同 Service 的其他任务能执行，原监听器恢复后再执行下一个监听器。
- 嵌套深度、外部上下文拒绝、错误/panic 聚合及异步事件语义不回归。
- 教程和可运行本地事件示例明确展示 Await 用法与任务插入规则。
