# Service 本地事件

本地事件只在一个 Service 内分发，不是跨 Node 消息机制。监听器应在 `OnInit` 注册，这样业务任务开始前监听关系已经固定。

## 流程

Timer 先进入 Service 任务上下文，再调用 `NotifyEventSync(PlayerJoined{...})`；监听器在同一串行语义下立即处理事件并记录加入日志。同步通知适合需要确定监听器已处理完的本地状态推进。

## 运行与练习

执行 `run.bat` 或 `./run.sh`，预期日志为 `player 1001 joined`。可增加第二个监听器观察注册顺序；不要把事件对象留给异步 goroutine 长期持有。

对应教程：[Timer、Event 与执行](../../../docs/baseline/v3.0/guides/04-timer-event-and-execution.md)。
