# Origin v3 示例

示例目录与教程路径一一对应。先从 [`00-quickstart`](./00-quickstart/01-hello-service/README.md) 开始；
日志章节虽然保留路径编号 `12`，但学习上建议在 `02-configuration` 之后先阅读；前六章不要求
NATS、etcd 或 Docker。

| 目录 | 用途 |
| --- | --- |
| `00-quickstart` | 第一个可运行 Application |
| `01-first-application` | Application、Options、自定义命令、Node、Service 与生命周期 |
| `02-configuration` | YAML、默认值与 Service 配置 |
| [`12-logging`](./12-logging/README.md) | v3.1 日志调用、格式、文件滚动与运行时控制 |
| `03-service-and-module` | Service、Module 与配置归属 |
| [`04-timer-event-and-execution`](./04-timer-event-and-execution/README.md) | Timer、事件、Await、安全执行与 v3.1 Node 游戏逻辑时间 |
| [`05-rpc-basics`](./05-rpc-basics/README.md) | 合约生成，以及 Await、Call、Async、Notify、Broadcast |
| `06-remote-rpc` | TCP、NATS、路由与广播 |
| `07-discovery` | Origin、etcd、自定义 Provider 与等待目标服务 |
| `08-retire-and-resume` | 优雅退休与恢复 |
| `09-diagnostics-and-pprof` | 诊断快照与动态 pprof |
| `10-performance` | 可重复执行的性能测试 |
| `11-troubleshooting` | 可控故障与修复练习 |

每个可直接运行的程序示例都包含 `run.bat` 和 `run.sh`。依赖 NATS 或 etcd 的示例提供其对应的 `deps-*` 检查、启动或停止脚本。脚本只包装 README 中公开的命令，不会隐藏后台进程或自动修改系统状态。

`_support/` 是教程共享代码，`_baseline/` 是版本基线归档；二者均不属于学习路径。

`12-logging` 使用编号 12，避免重排已经冻结的 v3.0 示例路径；推荐学习顺序把它放在完成
`02-configuration` 后、`03-service-and-module` 前。
