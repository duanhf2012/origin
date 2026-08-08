# 延迟 Timer 与 Cron

Timer 回调始终回到所属 Service 的串行执行语义中，因此适合更新该 Service 的业务状态。`AfterFunc` 只执行一次，`NewTicker` 使用固定节拍重复执行，`CronFunc` 按墙上时间表达式调度。

## 关键代码

示例在 `OnStart` 中注册一次延迟回调、250ms Ticker 和每秒 Cron。Ticker 第 2 次触发后通过 `PauseTimer` 暂停，由另一个 After Timer 调用 `ResumeTimer`；第 4 次触发时使用 `CancelTimer` 取消，并读取 `TimerStats`。`CancelTimer` 会把保存的 `TimerID` 清零。

控制接口的使用规则：

- `PauseTimer(id)` 暂停尚未开始的 Timer；After/Ticker 会保存暂停时的剩余延迟。周期回调已经
  开始时不会中断当前回调，而是在本轮结束后暂停。
- `ResumeTimer(id)` 只接受处于暂停状态的 Timer。After/Ticker 从剩余延迟继续，Cron 从当前
  时间寻找下一个匹配点，不补执行暂停期间错过的历史点。
- `CancelTimer(&id)` 接收 `*TimerID`，取消尚未开始的 Timer；周期回调正在执行时，会等本轮
  完成后阻止下一轮。无论是否命中有效 Timer，非零 ID 都会先清零。
- 三个接口只允许创建该 Timer 的同一 Service 或 Module 调用；不存在、已完成、归属错误或
  状态不允许的操作返回 `false`。

`TimerStats()` 可观察 `Scheduled`、`Paused`、`Running`、`TriggeredTotal`、`PausedTotal`、
`ResumedTotal` 和 `CanceledTotal`，适合确认控制是否生效，不应在每个业务请求中频繁上报。

## 运行与练习

执行 `run.bat` 或 `./run.sh`，观察 After 只执行一次、Ticker 的暂停/恢复/取消顺序，以及每秒一条 Cron 日志。可修改 Ticker 间隔和 Cron 秒字段验证调度差异；耗时 I/O 应使用 `Await`，不要长期阻塞 Timer 回调。

对应教程：[Timer、Event 与执行](../../../docs/baseline/v3.0/guides/04-timer-event-and-execution.md)。
