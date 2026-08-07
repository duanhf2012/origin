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

`main.go` 中的 `StartTimeout`、`StopTimeout`、每 Node Timer 上限和 Cron 时区均通过 `application.New(application.Options{...})` 设置。它们是框架级覆盖项；日志级别、日志输出、Node 与 Service 仍由 YAML/JSON 配置控制。

完整说明见：[创建第一个应用](../../../docs/baseline/v3.0/guides/01-first-application.md)。
