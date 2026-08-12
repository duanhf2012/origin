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

教程章节、标题和 `examples/` 目录按同一序号排列；每章先运行对应示例，再阅读使用说明和
深入内容。

| 序号 | 章节 | 学习目标 | 教程 | 示例目录 |
| --- | --- | --- | --- | --- |
| 00 | 快速入口 | 运行第一个 Application | [快速入口](./docs/baseline/v3.0/guides/00.quickstart.md) | [00-quickstart](./examples/00-quickstart/) |
| 01 | 创建第一个应用 | 创建 Application、Node 与 Service | [创建第一个应用](./docs/baseline/v3.0/guides/01.first-application.md) | [01-first-application](./examples/01-first-application/) |
| 02 | 配置应用 | 加载 YAML 与业务配置 | [配置应用](./docs/baseline/v3.0/guides/02.configuration.md) | [02-configuration](./examples/02-configuration/) |
| 03 | 日志输出与管理 | 写日志、配置格式与文件滚动、运行时调整与替换输出后端 | [日志输出与管理](./docs/maintenance/v3.1/guides/03.logging.md) | [03-logging](./examples/03-logging/) |
| 04 | Service 与 Module | 用 Service、Module 组织业务 | [Service 与 Module](./docs/baseline/v3.0/guides/04.service-and-module.md) | [04-service-and-module](./examples/04-service-and-module/) |
| 05 | Timer、Event 与执行 | 使用 Timer、Event、Await 与安全执行 | [Timer、Event 与执行](./docs/baseline/v3.0/guides/05.timer-event-and-execution.md) | [05-timer-event-and-execution](./examples/05-timer-event-and-execution/) |
| 06 | RPC 基础 | 定义合约并调用同 Node RPC | [RPC 基础](./docs/baseline/v3.0/guides/06.rpc-basics.md) | [06-rpc-basics](./examples/06-rpc-basics/) |
| 07 | 跨节点 RPC | 使用 TCP、NATS 与多实例远程 RPC | [跨节点 RPC](./docs/baseline/v3.0/guides/07.remote-rpc.md) | [07-remote-rpc](./examples/07-remote-rpc/) |
| 08 | 服务发现 | 使用 Origin、etcd 或自定义服务发现 | [服务发现](./docs/baseline/v3.0/guides/08.discovery.md) | [08-discovery](./examples/08-discovery/) |
| 09 | Retire、Resume 与优雅停止 | 用命令在运行期退休/恢复整个 Application，并监听 Service 状态事件 | [Retire、Resume 与优雅停止](./docs/baseline/v3.0/guides/09.retire-and-resume.md) | [09-retire-and-resume](./examples/09-retire-and-resume/) |
| 10 | Admin 管理 HTTP、Diagnostics 与 pprof | 管理端点、诊断、动态 pprof 与监控适配 | [Admin 管理 HTTP、Diagnostics 与 pprof](./docs/maintenance/v3.1/guides/10.admin-diagnostics-and-pprof.md) | [10-admin-diagnostics-and-pprof](./examples/10-admin-diagnostics-and-pprof/) |
| 11 | 性能测试与容量理解 | 运行并解读 RPC 性能测试 | [性能测试与容量理解](./docs/baseline/v3.0/guides/11.performance.md) | [11-performance](./examples/11-performance/) |
| 12 | 故障排查 | 复现并排查常见故障 | [故障排查](./docs/baseline/v3.0/guides/12.troubleshooting.md) | [12-troubleshooting](./examples/12-troubleshooting/) |

完整教程见 [v3.0 使用指南](./docs/baseline/v3.0/guides/README.md)，全部可运行示例见 [examples](./examples/README.md)。

## 扩展组件教程

扩展组件按需学习，不作为 `00`～`12` 框架教程的前置条件。后续组件完成实现、测试和教程验收后，
再加入本表。

| 序号 | 组件 | 学习目标 | 教程 | 示例目录 |
| --- | --- | --- | --- | --- |
| 13 | TCP、WebSocket 与 KCP 网络模块 | 使用统一 Session、Server、Client、Dialer 与 PB/JSON Router | [网络模块文档](./docs/maintenance/v3.2/README.md) | [13-network](./examples/13-network/) |
| 14 | Gin HTTP Module 与 HTTP Client | 使用普通/Safe 路由、分层鉴权、有界 Client 与同 Service HTTP 自调用 | [HTTP 组件文档](./docs/maintenance/v3.2/README.md) | [14-http](./examples/14-http/) |
| 15 | MongoDB Module | 使用官方 Collection、索引/事务便利层与 Origin Await 完成游戏数据访问 | [MongoDB Module 使用指南](./docs/maintenance/v3.2/guides/MongoDB%20Module使用指南.md) | [15-mongodb](./examples/15-mongodb/) |
| 16 | Redis Module | 使用三种拓扑、高频基础命令、Pipeline/Lua、Lease Lock 与 Origin Await | [Redis Module 使用指南](./docs/maintenance/v3.2/guides/Redis%20Module使用指南.md) | [16-redis](./examples/16-redis/) |
| 17 | Kafka Module | 使用 Managed Producer/Consumer、Raw/JSON/PB、Service Handler 与 Native Sarama | [Kafka Module 使用指南](./docs/maintenance/v3.2/guides/Kafka%20Module使用指南.md) | [17-kafka](./examples/17-kafka/) |
| 18 | Blueprint Module | 在 Service 工作协程运行蓝图业务节点，管理 Instance、异步恢复与热加载 | [Blueprint Module 使用指南](./docs/maintenance/v3.2/guides/Blueprint%20Module使用指南.md) | [18-blueprint](./examples/18-blueprint/) |

## 学习方式

每一章都先从一个实际任务开始：复制命令、运行示例、修改少量代码并观察结果。后续章节才说明原理、限制和排错方式。

每个可直接启动的程序示例目录都有：

- `README.md`：目的、前置条件、运行命令与预期结果；
- `run.bat`：Windows 入口；
- `run.sh`：Linux 入口；
- 完整源码和必要配置。

依赖 NATS 或 etcd 的示例会明确提供依赖检查及启动、停止脚本。`00`～`06` 以及 `07` 的 TCP
示例不要求外部中间件。

## 文档边界

- [使用指南](./docs/baseline/v3.0/guides/README.md)：如何使用 Origin v3。
- [示例索引](./examples/README.md)：可直接运行、修改和调试的程序。
- [v3.0 设计索引](./docs/baseline/v3.0/design/设计文档索引.md)：设计依据、约束和研发记录。
- [性能测试结论](./docs/baseline/v3.0/design/details/2026-08-02-RPC性能测试结论.md)：测试环境、结果和解读边界。
