# Application Options 与自定义命令

这个示例把两类容易混淆的能力放在同一个可执行程序中：`application.Options` 是创建 Application 时一次确定的框架边界；`command.Command` 是不启动 Application 的一次性离线任务。

## 先运行自定义命令

执行 `run.bat` 或 `./run.sh`。预期输出：

```text
custom command args=[Alice]
```

它只执行 `print-options` 回调：不会读取 `config/`、不会创建 Node 或 Service，也不会取得 PID 运行锁。可自行运行 `go run . help` 与 `go run . help print-options`，观察自定义命令如何出现在帮助中。

## 再按正常方式启动

执行 `run-start.bat` 或 `./run-start.sh`，然后按 `Ctrl+C`。这次会加载 `config/application.yaml`、创建 `OptionService` 并输出启动日志。

`main.go` 中的 `StartTimeout`、`StopTimeout`、每 Node Timer 上限和 Cron 时区均通过 `application.New(application.Options{...})` 设置。`StartTimeout` 是所有选中 Node 共享的总启动时间；到期后启动失败，框架回滚已启动资源。`StopTimeout` 是整个停止过程共享的总时间；到期后框架取消可控等待、继续安全清理并返回错误。两者设为 `0` 都表示不设置 Application 级总超时；单次 RPC、`AwaitXxx` 和外部客户端仍使用自己的超时。这些超时无法强行停止忽略 Context 的 goroutine，所以生命周期和外部 I/O 代码仍应正确传递 Context。

完整说明见：[创建第一个应用](../../../docs/baseline/v3.0/guides/01-first-application.md)。
