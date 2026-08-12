# Producer 工作流

先启动 [`deploy/kafka`](../../../deploy/kafka/README.md)，再运行 `run.bat` 或 `run.sh`。示例会发送 Raw、JSON、PB 和 JSON Batch。

- `ProduceJSONAsync` 在调用方 Service task 完成编码和有界准入，不等待 Broker；
- `DispatchDelivery` 用一个预留任务等待，并把结果回调到同一 Service 串行工作协程；
- `Produce*Sync` 必须从 Service task 放入 `Await`，避免阻塞该 Service 的其他业务；
- Batch 可以跨 Topic，但不是事务，失败时要检查逐条结果和部分接受数量；
- Raw Buffer 在 Delivery 前不能修改；JSON/PB 在方法返回前已形成编码快照。

按 `Ctrl+C` 触发 Drain：先拒绝新消息，再排空已接受 Delivery，最后关闭 Client。
