# 教程章节重排设计

> 状态：已完成
> 基线：v3.0
> 目标版本：v3.1.0
> 兼容性：教程与示例路径重排；仓库内引用全部同步，冻结 v3.0 文档仅修复失效链接

## 目标

将新增的日志教程放入实际的第 03 章，使学习路径、章节序号、标题和示例目录一致；原第
03 至第 11 章依次顺延为第 04 至第 12 章。

## 最终学习路径

| 序号 | 章节标题 | 示例目录 |
| --- | --- | --- |
| 00 | 快速入口 | `00-quickstart` |
| 01 | 创建第一个应用 | `01-first-application` |
| 02 | 配置应用 | `02-configuration` |
| 03 | 日志输出与管理 | `03-logging` |
| 04 | Service 与 Module | `04-service-and-module` |
| 05 | Timer、Event 与执行 | `05-timer-event-and-execution` |
| 06 | RPC 基础 | `06-rpc-basics` |
| 07 | 跨节点 RPC | `07-remote-rpc` |
| 08 | 服务发现 | `08-discovery` |
| 09 | Retire、Resume 与优雅停止 | `09-retire-and-resume` |
| 10 | Diagnostics 与 pprof | `10-diagnostics-and-pprof` |
| 11 | 性能测试与容量理解 | `11-performance` |
| 12 | 故障排查 | `12-troubleshooting` |

## 目录迁移

`examples/12-logging` 移动为 `examples/03-logging`。其余教学目录按下表顺延：

| 旧目录 | 新目录 |
| --- | --- |
| `03-service-and-module` | `04-service-and-module` |
| `04-timer-event-and-execution` | `05-timer-event-and-execution` |
| `05-rpc-basics` | `06-rpc-basics` |
| `06-remote-rpc` | `07-remote-rpc` |
| `07-discovery` | `08-discovery` |
| `08-retire-and-resume` | `09-retire-and-resume` |
| `09-diagnostics-and-pprof` | `10-diagnostics-and-pprof` |
| `10-performance` | `11-performance` |
| `11-troubleshooting` | `12-troubleshooting` |

不迁移非教程目录 `examples/10-deployment-and-operations`、`_baseline` 和 `_support`。

## 文档与兼容策略

根 README 和 examples 索引以连续序号、正式章节标题和实际示例目录呈现。日志章节标题固定为
“日志输出与管理”，不再显示“v3.1 日志”或把学习目标当作标题。

所有 README、教程、设计、报告、Go 测试、配置、Windows/Linux 脚本中的旧示例路径都更新为
新路径。`docs/baseline/v3.0` 的正文和设计结论不重写；其中指向迁移示例的链接仅更新为新的
有效路径。

不保留空目录、重复源码或旧路径的运行脚本。Git 历史和 v3.0 发布标签承担旧路径追溯职责。

## 验收

1. 根 README 与 examples 索引的学习序号连续为 00 至 12，日志为第 03 章。
2. `examples/` 中存在上述最终学习目录，不存在被迁移的旧目录。
3. 仓库内没有旧教程示例目录路径的有效引用。
4. 全量 Markdown 相对链接检查无缺失目标。
5. `go test ./...` 通过；教程配置回归继续覆盖移动后的日志示例路径。
