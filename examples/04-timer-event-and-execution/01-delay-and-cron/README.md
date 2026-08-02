# 延迟 Timer 与 Cron

Timer 回调始终回到所属 Service 的串行执行语义中，因此适合更新该 Service 的业务状态。`AfterFunc` 只执行一次；`CronFunc` 适合固定节奏的周期性工作。

## 关键代码

示例在 `OnStart` 中注册一次延迟回调和每秒执行一次的 Cron 回调。不要在回调内无限阻塞；耗时 I/O 应使用 `Await` 或拆分为短任务。

## 运行与练习

执行 `run.bat` 或 `./run.sh`，先观察一次 `after` 日志，再观察每秒一条 Cron 日志。可修改 Cron 表达式的秒字段验证节奏变化，最后按 `Ctrl+C` 确认停止后不再触发。

对应教程：[Timer、Event 与执行](../../../docs/baseline/v3.0/guides/04-timer-event-and-execution.md)。
