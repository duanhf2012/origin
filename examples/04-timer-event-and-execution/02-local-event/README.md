# Service 本地事件

本地事件只在一个 Service 内分发，不是跨 Node 消息机制。监听器应在 `OnInit` 注册，这样业务任务开始前监听关系已经固定。示例同时覆盖 `NotifyEventSync`、`NotifyEventAsync` 和 `EventStats`。

## 流程

Timer 先进入 Service 任务上下文，再同步通知玩家 `1001`；监听器在当前任务中立即完成。随后异步通知玩家 `1002`，它作为普通后续 Service 任务排队执行。异步提交只保存 Event 接口值，提交成功后生产者不能再修改 payload。

## 运行与练习

执行 `run.bat` 或 `./run.sh`，预期依次看到 sync、async 两条玩家日志和 `sync=1 async=1 failures=0` 的统计。可增加第二个监听器观察订阅顺序；同步监听器不能在嵌套调用中执行 `Await`。

对应教程：[Timer、Event 与执行](../../../docs/baseline/v3.0/guides/04-timer-event-and-execution.md)。
