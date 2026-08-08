# 普通日志与 Service 日志

本例只回答一个最常见的问题：业务代码应该调用哪个日志入口？运行：

```text
run.bat
```

Linux/macOS 使用 `./run.sh`。终端中可以观察到两类输出：

```text
... INFO 01-global-and-service/main.go:... process logger is ready component=bootstrap-helper
... INFO [game-1/LoggingService] 01-global-and-service/main.go:... player service is ready player_id=10001
... INFO [game-1/LoggingService] 01-global-and-service/main.go:... audit module started component=AuditModule
```

调用规则很简单：

- 没有 Service 引用的工具代码使用 `log.Info`、`log.Warn` 等包级函数；它们不自动带
  Node/Service 归属。
- Service 内使用 `target.Logger()`；框架会自动附加当前配置实例的 `node_id` 和
  `service_name`。
- Module 内使用 `module.Logger()`；它直接复用所属 Service Logger。需要辨认 Module 时，
  自行增加 `component` 等业务字段。

包级日志不会创建第二套 Runtime、队列或文件。Application 在进入业务生命周期前安装默认
Logger，停止后自动清除；初始化前或完全停止后调用只会安全地成为空操作。

同进程并行运行多个 Application 时，默认 Logger 无法自动判断归属，因此不要依赖包级
`log.Xxx`；Service/Module Logger 仍有明确归属。当前常规部署方式是一进程一个 Application。

示例源码见 [`main.go`](./main.go)，配置见
[`config/application.yaml`](./config/application.yaml)。
