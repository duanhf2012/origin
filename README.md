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

## 选择学习路线

教程分为两条路线：

- **框架教程**讲 Application、Node、Service、配置、执行模型、RPC 和生命周期，先建立不依赖外部
  基础设施的最小知识闭环；
- **组件与第三方扩展**按需接入 Origin 网络模块、NATS、etcd、自定义 Provider、日志 Handler、
  监控适配器和 Codec，不作为理解框架基础的前置条件。

建议新使用者先完成 `00`～`06`，再根据项目需要选择进阶主题和扩展。组件与第三方扩展的统一入口见
[组件与第三方扩展](./docs/extensions/README.md)。

## 框架基础教程

| 序号 | 章节 | 学习目标 | 教程 | 示例目录 |
| --- | --- | --- | --- | --- |
| 00 | 快速入口 | 运行第一个 Application | [快速入口](./docs/baseline/v3.0/guides/00.quickstart.md) | [00-quickstart](./examples/00-quickstart/) |
| 01 | 创建第一个应用 | 创建 Application、Node 与 Service | [创建第一个应用](./docs/baseline/v3.0/guides/01.first-application.md) | [01-first-application](./examples/01-first-application/) |
| 02 | 配置应用 | 加载 YAML 与业务配置 | [配置应用](./docs/baseline/v3.0/guides/02.configuration.md) | [02-configuration](./examples/02-configuration/) |
| 03 | 日志输出与管理 | 写日志、配置格式与文件滚动、运行时调整 | [日志输出与管理](./docs/maintenance/v3.1/guides/03.logging.md) | [03-logging](./examples/03-logging/) |
| 04 | Service 与 Module | 用 Service、Module 组织业务 | [Service 与 Module](./docs/baseline/v3.0/guides/04.service-and-module.md) | [04-service-and-module](./examples/04-service-and-module/) |
| 05 | Timer、Event 与执行 | 使用 Timer、Event、Await 与安全执行 | [Timer、Event 与执行](./docs/baseline/v3.0/guides/05.timer-event-and-execution.md) | [05-timer-event-and-execution](./examples/05-timer-event-and-execution/) |
| 06 | RPC 基础 | 定义合约并调用同 Node RPC | [RPC 基础](./docs/baseline/v3.0/guides/06.rpc-basics.md) | [06-rpc-basics](./examples/06-rpc-basics/) |

## 进阶运行与工程实践

| 序号 | 章节 | 学习目标 | 教程 | 示例目录 |
| --- | --- | --- | --- | --- |
| 07 | 跨节点 RPC | 使用内置 TCP、路由与广播完成多 Node 调用 | [跨节点 RPC](./docs/baseline/v3.0/guides/07.remote-rpc.md) | [07-remote-rpc](./examples/07-remote-rpc/) |
| 08 | 服务发现 | 使用 Origin Provider、目录筛选、状态事件与 Provider SPI | [服务发现](./docs/baseline/v3.0/guides/08.discovery.md) | [08-discovery](./examples/08-discovery/) |
| 09 | Retire、Resume 与优雅停止 | 用命令在运行期退休/恢复整个 Application，并监听 Service 状态事件 | [Retire、Resume 与优雅停止](./docs/baseline/v3.0/guides/09.retire-and-resume.md) | [09-retire-and-resume](./examples/09-retire-and-resume/) |
| 10 | Admin 管理 HTTP、Diagnostics 与 pprof | 管理端点、诊断与动态 pprof | [Admin 管理 HTTP、Diagnostics 与 pprof](./docs/maintenance/v3.1/guides/10.admin-diagnostics-and-pprof.md) | [10-admin-diagnostics-and-pprof](./examples/10-admin-diagnostics-and-pprof/) |
| 11 | 性能测试与容量理解 | 运行并解读 RPC 性能测试 | [性能测试与容量理解](./docs/baseline/v3.0/guides/11.performance.md) | [11-performance](./examples/11-performance/) |
| 12 | 故障排查 | 复现并排查常见故障 | [故障排查](./docs/baseline/v3.0/guides/12.troubleshooting.md) | [12-troubleshooting](./examples/12-troubleshooting/) |

## 组件与第三方扩展

| 类型 | 主题 | 何时使用 | 教程 |
| --- | --- | --- | --- |
| Origin 组件 | TCP 与 WebSocket 网络模块 | 对外提供游戏连接、网关或自定义协议入口 | [网络模块](./docs/maintenance/v3.2/README.md) |
| 第三方集成 | NATS RPC | 已有 NATS 集群，希望由 Broker 管理连接与恢复 | [NATS RPC](./docs/extensions/nats-rpc.md) |
| 第三方集成 | etcd 服务发现 | 已有 etcd 集群，需要跨进程共享服务目录 | [etcd 服务发现](./docs/extensions/etcd-discovery.md) |
| 自定义扩展 | Provider、Handler、监控与 Codec | 接入其他基础设施或业务协议 | [扩展点索引](./docs/extensions/README.md#自定义扩展点) |

网络模块属于 Origin 发布能力；即使内部使用成熟开源库，也不要求使用者自行适配，因此不归入第三方
集成。NATS、etcd 等需要独立部署和运维的基础设施才放入第三方教程。

## 学习方式

每一章都先从一个实际任务开始：复制命令、运行示例、修改少量代码并观察结果。后续章节才说明原理、限制和排错方式。

每个可直接启动的程序示例目录都有：

- `README.md`：目的、前置条件、运行命令与预期结果；
- `run.bat`：Windows 入口；
- `run.sh`：Linux 入口；
- 完整源码和必要配置。

基础教程不依赖 NATS、etcd 或 Docker。第三方示例会明确列出依赖，并提供对应的检查、启动或停止
脚本；这些脚本只用于本地开发，不代表生产部署方案。

## 文档边界

- [使用指南](./docs/baseline/v3.0/guides/README.md)：如何使用 Origin v3。
- [组件与第三方扩展](./docs/extensions/README.md)：如何选择和接入可选组件、第三方基础设施与扩展点。
- [示例索引](./examples/README.md)：可直接运行、修改和调试的程序。
- [v3.0 设计索引](./docs/baseline/v3.0/design/设计文档索引.md)：设计依据、约束和研发记录。
- [性能测试结论](./docs/baseline/v3.0/design/details/2026-08-02-RPC性能测试结论.md)：测试环境、结果和解读边界。
