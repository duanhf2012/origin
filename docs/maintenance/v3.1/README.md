# Origin v3.1 文档

此目录预留给 v3.1 的持续维护与新能力开发。它以 `../../baseline/v3.0/` 为初始基线；首个维护项开始时，按需建立以下子目录：

- `proposals/`：尚未确认的方案。
- `design/`：已确认的设计与兼容性约束。
- `plans/`：已确认设计的实施计划。
- `changes/`：已完成变更的摘要、关联提交与验证结果。
- `reports/`：性能、故障演练、兼容性和验收报告。
- `guides/`：面向使用者的使用、部署、配置和排障文档。

每篇文档必须注明：状态、目标版本、兼容性和基线（通常为 `v3.0`）。

## 已确认设计

- [Origin 发布前全面复审与优化设计](./design/Origin发布前全面复审与优化设计.md)
- [RPC 可选 Context 与 goroutine 调用设计](./design/RPC可选Context与goroutine调用设计.md)
- [Node 游戏逻辑时间设计](./design/Node游戏逻辑时间设计.md)
- [日志易用性、输出格式与运行时控制设计](./design/日志易用性、输出格式与运行时控制设计.md)
- [同步本地事件 Await 语义设计](./design/同步本地事件Await语义设计.md)
- [Admin 管理 HTTP、Diagnostics 与 pprof 设计](./design/Admin管理HTTP、Diagnostics与pprof设计.md)

## 实施计划

- [Origin 发布前全面复审与优化实施计划](./plans/Origin发布前全面复审与优化实施计划.md)
- [Admin 管理 HTTP、Diagnostics 与 pprof 实施计划](./plans/Admin管理HTTP、Diagnostics与pprof实施计划.md)

## 使用者变更

- [v3.1 使用变更索引](./guides/README.md)
- [Node 游戏逻辑时间教程](./guides/node-game-time.md)
- [日志：调用、格式、滚动与运行时控制](./guides/03.logging.md)
- [Admin 管理 HTTP、Diagnostics 与 pprof](./guides/10.admin-diagnostics-and-pprof.md)
- [部署与运维](./guides/deployment-and-operations.md)

## 已完成变更与报告

- [Node 游戏逻辑时间变更摘要](./changes/Node游戏逻辑时间变更摘要.md)
- [Node 游戏逻辑时间验收报告](./reports/Node游戏逻辑时间验收报告.md)
- [日志易用性、输出格式与运行时控制变更摘要](./changes/日志易用性、输出格式与运行时控制变更摘要.md)
- [日志易用性、输出格式与运行时控制验收报告](./reports/日志易用性、输出格式与运行时控制验收报告.md)
- [同步本地事件 Await 语义变更摘要](./changes/同步本地事件Await语义变更摘要.md)
- [Admin 管理 HTTP、Diagnostics 与 pprof 变更摘要](./changes/Admin管理HTTP、Diagnostics与pprof变更摘要.md)
- [Admin 管理 HTTP、Diagnostics 与 pprof 验收报告](./reports/Admin管理HTTP、Diagnostics与pprof验收报告.md)
- [Origin 系统级稳定性、容量与性能验收报告](./reports/Origin系统级稳定性容量与性能验收报告.md)
- [Origin 覆盖率、Example 与教程最终验收报告](./reports/Origin覆盖率Example与教程最终验收报告.md)
- [Origin 发布前全面复审验收报告](./reports/Origin发布前全面复审验收报告.md)
