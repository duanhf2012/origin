# Origin v3.0 使用指南

本指南按“先会用、再深入”的顺序组织。每篇文档先给出一个能直接运行的例子，随后才说明对象关系、默认规则和边界。`00`～`06` 是框架基础；Origin 可选组件和需要独立部署的第三方基础设施从[组件与第三方扩展](../../../extensions/README.md)按需进入。

## 框架基础

| 顺序 | 主题 | 首要问题 |
| --- | --- | --- |
| 00 | [快速入口](./00.quickstart.md) | 怎样立即运行一个 Origin 应用？ |
| 01 | [创建第一个应用](./01.first-application.md) | 怎样创建 Application、Node 和 Service？ |
| 02 | [配置应用](./02.configuration.md) | 怎样用 YAML 配置应用和业务？ |
| 03 | [日志输出与管理](../../../maintenance/v3.1/guides/03.logging.md) | 怎样写日志、配置输出、滚动文件与运行时控制？ |
| 04 | [Service 与 Module](./04.service-and-module.md) | 怎样组织业务代码？ |
| 05 | [Timer、Event 与执行](./05.timer-event-and-execution.md) | 怎样写定时任务、事件和安全后台工作？ |
| 06 | [RPC 基础](./06.rpc-basics.md) | 怎样调用同一 Node 中的另一个 Service？ |

## 进阶运行与工程实践

| 顺序 | 主题 | 首要问题 |
| --- | --- | --- |
| 07 | [跨节点 RPC](./07.remote-rpc.md) | 怎样用内置 TCP 调用其他 Node，并完成路由与广播？ |
| 08 | [服务发现](./08.discovery.md) | 怎样使用 Origin Provider、筛选目录、监听状态或替换 Provider？ |
| 09 | [Retire、Resume 与优雅停止](./09.retire-and-resume.md) | 怎样用命令在运行期退休和恢复整个 Application，并监听状态事件？ |
| 10 | [Admin、Diagnostics 与 pprof](../../../maintenance/v3.1/guides/10.admin-diagnostics-and-pprof.md) | 怎样管理运行状态和按需诊断？ |
| 11 | [性能](./11.performance.md) | 怎样运行和理解 RPC 基准测试？ |
| 12 | [故障排查](./12.troubleshooting.md) | 怎样定位常见配置、RPC 和发现故障？ |

NATS RPC、etcd 服务发现、TCP/WebSocket 系统模块及自定义适配器不混入基础顺序；根据项目依赖从
[组件与第三方扩展](../../../extensions/README.md)选择。

## 参考资料

- [配置参考](./reference/configuration.md)
- [公开 API 索引](./reference/api-index.md)
- [错误与排错入口](./reference/errors.md)
- [术语表](./reference/glossary.md)
- [部署与运维](../../../maintenance/v3.1/guides/deployment-and-operations.md)

## 示例约定

每个教程小节会给出完整示例路径。以可直接启动的 `examples/07-remote-rpc/01-tcp-two-nodes/` 为例：

```text
README.md          # 使用步骤和预期输出
run.bat             # Windows 前台运行
run.sh              # Linux 前台运行
main.go 或 cmd/     # 完整源码
config/             # 需要时提供 YAML
```

依赖 NATS 或 etcd 的示例会提供 `deps-*` 检查、启动或停止脚本。文档中的代码片段用于阅读；示例目录中的代码才是可直接编译和调试的完整版本。
