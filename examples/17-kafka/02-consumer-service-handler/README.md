# Consumer Service Handler

先启动本示例，再运行 `01-producer-workflows`。单条 Handler 消费 JSON Topic，批量 Handler 消费 Raw Topic。

- Sarama Claim goroutine 只负责接收、派发和成功后的 `MarkMessage`；
- 业务 Handler 在所属 Service 串行工作协程执行，可以安全访问 Module 的非并发业务状态；
- Handler 内的数据库/HTTP I/O 用 `Await`，wait 函数在 Await worker 执行，返回后继续在 Service 工作协程；
- 返回错误、panic、Service 队列满或 Session 撤销都不会 Mark 当前消息；消息可能重投，业务必须用 Event ID、唯一键或幂等表防重；
- 批量失败时整个批次不 Mark，批次不是 Kafka 事务。

`Pause/PauseAll` 只暂停后续 Fetch，不撤回已进入 Service 队列的任务；Module 会在新 Claim/Rebalance 后重放暂停意图。
