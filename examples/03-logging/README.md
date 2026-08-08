# 03 日志输出与管理

这一章集中演示 v3.1 日志外观、输出格式、文件滚动和运行时控制。建议在完成
[`02-configuration`](../02-configuration/01-minimal-yaml/README.md) 后阅读；配置章节只保留“如何加载配置”的
内容，日志本身以本章为准。

| 示例 | 学习目标 |
| --- | --- |
| [`01-global-and-service`](./01-global-and-service/README.md) | 区分 `log.Xxx` 与 Service/Module Logger |
| [`02-formats-and-context`](./02-formats-and-context/README.md) | 配置 text/JSON 和独立的归属字段开关 |
| [`03-file-rotation`](./03-file-rotation/README.md) | 配置文件名、大小/日期滚动、清理和压缩 |
| [`04-runtime-control`](./04-runtime-control/README.md) | 运行时调整级别、暂停/恢复输出并读取状态 |

每个子目录都有完整源码、配置、`run.bat` 和 `run.sh`。完整规则见
[日志输出与管理教程](../../docs/maintenance/v3.1/guides/logging.md)。
