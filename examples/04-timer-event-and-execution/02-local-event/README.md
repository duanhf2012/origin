# Service 本地事件

事件监听只能在 `OnInit` 注册。示例通过一次 Timer 进入 Service 任务上下文，再用 `NotifyEventSync` 同步通知 `PlayerJoined`。

```text
run.bat
```

预期日志：`player 1001 joined`。
