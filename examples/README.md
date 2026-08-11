# Origin v3 示例

示例按“框架基础”和“组件与第三方扩展”分组。先从
[`00-quickstart`](./00-quickstart/01-hello-service/README.md) 开始；基础组不要求 NATS、etcd 或
Docker。

## 框架基础

| 目录 | 用途 |
| --- | --- |
| `00-quickstart` | 第一个可运行 Application |
| `01-first-application` | Application、Options、自定义命令、Node、Service 与生命周期 |
| `02-configuration` | YAML、默认值与 Service 配置 |
| [`03-logging`](./03-logging/README.md) | 日志调用、格式、文件滚动与运行时控制 |
| `04-service-and-module` | Service、Module 与配置归属 |
| [`05-timer-event-and-execution`](./05-timer-event-and-execution/README.md) | Timer、事件、Await、安全执行与 v3.1 Node 游戏逻辑时间 |
| [`06-rpc-basics`](./06-rpc-basics/README.md) | 合约生成，以及 Await、Call、Async、Notify、Broadcast |
| [`07-remote-rpc/01-tcp-two-nodes`](./07-remote-rpc/01-tcp-two-nodes/README.md) | 内置 TCP 跨节点 RPC |
| [`07-remote-rpc/03-route-and-broadcast`](./07-remote-rpc/03-route-and-broadcast/README.md) | 多实例路由与广播 |
| [`08-discovery/01-origin-provider`](./08-discovery/01-origin-provider/README.md) | 内置 Origin Provider |
| [`08-discovery/03-watch-and-lost`](./08-discovery/03-watch-and-lost/README.md) | 发现状态事件与 Lost |
| [`08-discovery/05-await-service`](./08-discovery/05-await-service/README.md) | 等待和查询目标服务 |
| [`09-retire-and-resume`](./09-retire-and-resume/) | 优雅退休与恢复 |
| [`10-admin-diagnostics-and-pprof`](./10-admin-diagnostics-and-pprof/README.md) | Admin 端点、诊断快照、内置 Diagnostics 与动态 pprof |
| `11-performance` | 可重复执行的性能测试 |
| `12-troubleshooting` | 可控故障与修复练习 |

## 组件与第三方扩展

| 示例 | 类型 | 用途 |
| --- | --- | --- |
| [`13-network`](./13-network/README.md) | Origin 组件 | v3.2 TCP/WebSocket Session、Server、Client、Dialer 与协议 Router |
| [`07-remote-rpc/02-nats-two-nodes`](./07-remote-rpc/02-nats-two-nodes/README.md) | 第三方集成 | 使用 NATS 承载跨节点 RPC |
| [`08-discovery/02-etcd-provider`](./08-discovery/02-etcd-provider/README.md) | 第三方集成 | 使用 etcd 保存和同步服务目录 |
| [`03-logging/05-custom-handler`](./03-logging/05-custom-handler/README.md) | 自定义扩展 | 替换日志输出后端 |
| [`08-discovery/04-custom-provider`](./08-discovery/04-custom-provider/README.md) | 自定义扩展 | 接入其他发现系统 |
| [`10-admin-diagnostics-and-pprof/06-metrics-adapter`](./10-admin-diagnostics-and-pprof/06-metrics-adapter/README.md) | 自定义扩展 | 把诊断快照适配到监控系统 |

扩展教程入口见[组件与第三方扩展](../docs/extensions/README.md)。每个可直接运行的程序示例都包含
`run.bat` 和 `run.sh`。依赖 NATS 或 etcd 的示例提供对应的 `deps-*` 检查、启动或停止脚本；脚本
只包装 README 中公开的命令，不会隐藏后台进程或自动修改系统状态。

`_support/` 是教程共享代码，`_baseline/` 是版本基线归档；二者均不属于学习路径。

日志位于第 03 章；完成 `02-configuration` 后即可继续阅读。
