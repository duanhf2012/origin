# Timer、事件与执行示例

这些示例都运行在单个 Service 的执行语义中。建议按顺序阅读：先掌握 Timer，再理解本地事件，最后再使用 Await 和安全执行包装。

- [01-delay-and-cron](./01-delay-and-cron/README.md)：一次性 Timer 与 Cron，适合短小的周期任务。
- [02-local-event](./02-local-event/README.md)：同步本地事件，不跨 Node。
- [03-await-and-safe](./03-await-and-safe/README.md)：外部等待、panic 边界与后台工作。

对应教程：[Timer、Event 与执行](../../docs/baseline/v3.0/guides/04-timer-event-and-execution.md)。
