# Origin v3 示例

示例目录与教程章节一一对应。先从 [`00-quickstart`](./00-quickstart/01-hello-service/README.md) 开始；前六章不要求 NATS、etcd 或 Docker。

| 目录 | 用途 |
| --- | --- |
| `00-quickstart` | 第一个可运行 Application |
| `01-first-application` | Application、Node、Service 与生命周期 |
| `02-configuration` | YAML、默认值与 Service 配置 |
| `03-service-and-module` | Service、Module 与配置归属 |
| `04-timer-event-and-execution` | Timer、事件、Await 与安全执行 |
| `05-rpc-basics` | 合约生成和同 Node RPC |
| `06-remote-rpc` | TCP、NATS、路由与广播 |
| `07-discovery` | Origin、etcd、自定义 Provider 与等待目标服务 |
| `08-retire-and-resume` | 优雅退休与恢复 |
| `09-diagnostics-and-pprof` | 诊断快照与动态 pprof |
| `10-deployment-and-operations` | 构建、进程与依赖部署 |
| `11-performance` | 可重复执行的性能测试 |
| `12-troubleshooting` | 可控故障与修复练习 |

每个可直接运行的程序示例都包含 `run.bat` 和 `run.sh`。依赖编排与 systemd 示例提供其对应的 `deps-*` 脚本或 Unit 文件。脚本只包装 README 中公开的命令，不会隐藏后台进程或自动修改系统状态。

`_support/` 是教程共享代码，`_baseline/` 是版本基线归档；二者均不属于学习路径。
