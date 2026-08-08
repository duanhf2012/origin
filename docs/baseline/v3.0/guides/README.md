# Origin v3.0 使用指南

本指南按“先会用、再深入”的顺序组织。每篇文档先给出一个能直接运行的例子，随后才说明对象关系、默认规则和边界。不要跳过 `00` 到 `06`：它们不依赖 NATS、etcd 或 Docker，是理解后续多节点能力的最短路径。

## 学习路径

| 顺序 | 主题 | 首要问题 |
| --- | --- | --- |
| 00 | [快速入口](./00-quickstart.md) | 怎样立即运行一个 Origin 应用？ |
| 01 | [创建第一个应用](./01-first-application.md) | 怎样创建 Application、Node 和 Service？ |
| 02 | [配置应用](./02-configuration.md) | 怎样用 YAML 配置应用和业务？ |
| 03 | [Service 与 Module](./03-service-and-module.md) | 怎样组织业务代码？ |
| 04 | [Timer、Event 与执行](./04-timer-event-and-execution.md) | 怎样写定时任务、事件和安全后台工作？ |
| 05 | [RPC 基础](./05-rpc-basics.md) | 怎样调用同一 Node 中的另一个 Service？ |
| 06 | [跨节点 RPC](./06-remote-rpc.md) | 怎样用 TCP 或 NATS 调用其他 Node？ |
| 07 | [服务发现](./07-discovery.md) | 怎样发现服务、使用 etcd 或替换 Provider？ |
| 08 | [Retire 与 Resume](./08-retire-and-resume.md) | 怎样优雅地下线和恢复业务？ |
| 09 | [Diagnostics 与 pprof](./09-diagnostics-and-pprof.md) | 怎样观察运行状态和按需诊断？ |
| 10 | [性能](./10-performance.md) | 怎样运行和理解 RPC 基准测试？ |
| 11 | [故障排查](./11-troubleshooting.md) | 怎样定位常见配置、RPC 和发现故障？ |

## 参考资料

- [配置参考](./reference/configuration.md)
- [公开 API 索引](./reference/api-index.md)
- [错误与排错入口](./reference/errors.md)
- [术语表](./reference/glossary.md)

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
