# Origin v3 示例

示例目录与教程章节一一对应。先从 [`00-quickstart`](./00-quickstart/01-hello-service/README.md) 开始；
`00`～`06` 不要求 NATS、etcd 或 Docker；`07` 的 TCP 示例也可独立运行。

| 目录 | 用途 |
| --- | --- |
| `00-quickstart` | 第一个可运行 Application |
| `01-first-application` | Application、Options、自定义命令、Node、Service 与生命周期 |
| `02-configuration` | YAML、默认值与 Service 配置 |
| [`03-logging`](./03-logging/README.md) | 日志调用、格式、文件滚动、运行时控制与自定义 Handler |
| `04-service-and-module` | Service、Module 与配置归属 |
| [`05-timer-event-and-execution`](./05-timer-event-and-execution/README.md) | Timer、事件、Await、安全执行与 v3.1 Node 游戏逻辑时间 |
| [`06-rpc-basics`](./06-rpc-basics/README.md) | 合约生成，以及 Await、Call、Async、Notify、Broadcast |
| `07-remote-rpc` | TCP、NATS、路由与广播 |
| `08-discovery` | Origin、etcd、自定义 Provider 与等待目标服务 |
| `09-retire-and-resume` | 优雅退休与恢复 |
| [`10-admin-diagnostics-and-pprof`](./10-admin-diagnostics-and-pprof/README.md) | Admin 扩展、诊断快照、内置 Diagnostics 与动态 pprof |
| `11-performance` | 可重复执行的性能测试 |
| `12-troubleshooting` | 可控故障与修复练习 |
| [`13-network`](./13-network/README.md) | v3.2 TCP/WebSocket/KCP Session、Server、Client、Dialer 与协议 Router |

每个可直接运行的程序示例都包含 `run.bat` 和 `run.sh`。依赖 NATS 或 etcd 的示例提供其对应的 `deps-*` 检查、启动或停止脚本。脚本只包装 README 中公开的命令，不会隐藏后台进程或自动修改系统状态。

`_support/` 是教程共享代码，`_baseline/` 是版本基线归档；二者均不属于学习路径。

日志位于第 03 章；完成 `02-configuration` 后即可继续阅读。
