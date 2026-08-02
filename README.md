# Origin v3

Origin v3 是面向多节点 Go 服务的运行框架。本仓库的 README 是使用者教程入口；从一个可运行的 Service 开始，再逐步学习配置、RPC、服务发现、诊断和部署。

## 5 分钟快速开始

前置条件：已安装 Go `1.26.5` 或兼容版本。这个示例不需要 NATS、etcd 或 Docker。

Windows：

```text
examples\00-quickstart\01-hello-service\run.bat
```

Linux：

```bash
./examples/00-quickstart/01-hello-service/run.sh
```

也可以直接执行等价的 Go 命令：

```bash
go run ./examples/00-quickstart/01-hello-service
```

预期会看到 `OnInit`、`OnStart` 和业务任务输出。按 `Ctrl+C` 可观察 `OnStop`。

## 完整学习路径

教程章节与 `examples/` 编号目录一一对应；每章先运行对应示例，再阅读使用说明和深入内容。

| 顺序 | 学习目标 | 教程 | 示例目录 |
| --- | --- | --- | --- |
| 00 | 运行第一个 Application | [快速入口](./docs/baseline/v3.0/guides/00-quickstart.md) | [00-quickstart](./examples/00-quickstart/) |
| 01 | 创建 Application、Node 与 Service | [创建第一个应用](./docs/baseline/v3.0/guides/01-first-application.md) | [01-first-application](./examples/01-first-application/) |
| 02 | 加载 YAML 与业务配置 | [配置应用](./docs/baseline/v3.0/guides/02-configuration.md) | [02-configuration](./examples/02-configuration/) |
| 03 | 用 Service、Module 组织业务 | [Service 与 Module](./docs/baseline/v3.0/guides/03-service-and-module.md) | [03-service-and-module](./examples/03-service-and-module/) |
| 04 | 使用 Timer、Event、Await 与安全执行 | [Timer、Event 与执行](./docs/baseline/v3.0/guides/04-timer-event-and-execution.md) | [04-timer-event-and-execution](./examples/04-timer-event-and-execution/) |
| 05 | 定义合约并调用同 Node RPC | [RPC 基础](./docs/baseline/v3.0/guides/05-rpc-basics.md) | [05-rpc-basics](./examples/05-rpc-basics/) |
| 06 | 使用 TCP、NATS 与多实例远程 RPC | [跨节点 RPC](./docs/baseline/v3.0/guides/06-remote-rpc.md) | [06-remote-rpc](./examples/06-remote-rpc/) |
| 07 | 使用 Origin、etcd 或自定义服务发现 | [服务发现](./docs/baseline/v3.0/guides/07-discovery.md) | [07-discovery](./examples/07-discovery/) |
| 08 | Retire、Resume 与优雅下线 | [Retire、Resume 与优雅停止](./docs/baseline/v3.0/guides/08-retire-and-resume.md) | [08-retire-and-resume](./examples/08-retire-and-resume/) |
| 09 | 读取诊断、启停 pprof、适配监控 | [Diagnostics 与 pprof](./docs/baseline/v3.0/guides/09-diagnostics-and-pprof.md) | [09-diagnostics-and-pprof](./examples/09-diagnostics-and-pprof/) |
| 10 | 构建、依赖编排与 Ubuntu 运行 | [部署与运维](./docs/baseline/v3.0/guides/10-deployment-and-operations.md) | [10-deployment-and-operations](./examples/10-deployment-and-operations/) |
| 11 | 运行并解读 RPC 性能测试 | [性能测试与容量理解](./docs/baseline/v3.0/guides/11-performance.md) | [11-performance](./examples/11-performance/) |
| 12 | 复现并排查常见故障 | [故障排查](./docs/baseline/v3.0/guides/12-troubleshooting.md) | [12-troubleshooting](./examples/12-troubleshooting/) |

完整教程见 [v3.0 使用指南](./docs/baseline/v3.0/guides/README.md)，全部可运行示例见 [examples](./examples/README.md)。

## 学习方式

每一章都先从一个实际任务开始：复制命令、运行示例、修改少量代码并观察结果。后续章节才说明原理、限制和排错方式。

每个可直接启动的程序示例目录都有：

- `README.md`：目的、前置条件、运行命令与预期结果；
- `run.bat`：Windows 入口；
- `run.sh`：Linux 入口；
- 完整源码和必要配置。

依赖 NATS 或 etcd 的示例会明确提供依赖检查及启动、停止脚本；systemd、部署清单等非程序示例则提供对应的 Unit 或运维脚本。`00`–`05` 以及 `06` 的 TCP 示例不要求任何外部中间件。

## 文档边界

- [使用指南](./docs/baseline/v3.0/guides/README.md)：如何使用 Origin v3。
- [示例索引](./examples/README.md)：可直接运行、修改和调试的程序。
- [v3.0 设计索引](./docs/baseline/v3.0/design/设计文档索引.md)：设计依据、约束和研发记录。
- [性能测试结论](./docs/baseline/v3.0/design/details/2026-08-02-RPC性能测试结论.md)：测试环境、结果和解读边界。
