# Timer、事件与执行示例

这些示例都运行在单个 Service 的执行语义中。建议按顺序阅读：先掌握 Timer，再理解本地事件，最后再使用 Await 和安全执行包装。

- [01-delay-and-cron](./01-delay-and-cron/README.md)：一次性 Timer、Ticker、Cron，以及暂停、恢复、取消和统计。
- [02-local-event](./02-local-event/README.md)：同步与异步本地事件，不跨 Node。
- [03-await-and-safe](./03-await-and-safe/README.md)：Await、异步派发、panic 边界、默认超时和执行统计。
- [04-node-game-time](./04-node-game-time/README.md)：Node 游戏逻辑时间，以及跨 Service 的 After、Ticker、Cron 统一重排。

对应教程：[Timer、Event 与执行](../../docs/baseline/v3.0/guides/05.timer-event-and-execution.md)。
