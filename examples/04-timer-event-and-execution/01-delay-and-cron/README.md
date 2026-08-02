# 延迟 Timer 与 Cron

Timer 回调始终回到所属 Service 的串行执行语义中，因此适合更新该 Service 的业务状态。`AfterFunc` 只执行一次，`NewTicker` 使用固定节拍重复执行，`CronFunc` 按墙上时间表达式调度。

## 关键代码

示例在 `OnStart` 中注册一次延迟回调、250ms Ticker 和每秒 Cron。Ticker 第 2 次触发后通过 `PauseTimer` 暂停，由另一个 After Timer 调用 `ResumeTimer`；第 4 次触发时使用 `CancelTimer` 取消，并读取 `TimerStats`。`CancelTimer` 会把保存的 `TimerID` 清零。

## 运行与练习

执行 `run.bat` 或 `./run.sh`，观察 After 只执行一次、Ticker 的暂停/恢复/取消顺序，以及每秒一条 Cron 日志。可修改 Ticker 间隔和 Cron 秒字段验证调度差异；耗时 I/O 应使用 `Await`，不要长期阻塞 Timer 回调。

对应教程：[Timer、Event 与执行](../../../docs/baseline/v3.0/guides/04-timer-event-and-execution.md)。
