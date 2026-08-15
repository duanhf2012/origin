# Origin v3.3 文档

v3.3 包含 RPC Client Labels 候选过滤、Go 1.27 泛型方法接入，以及 Go 1.27 下 Kafka JSON
整数解码契约修复。本目录只记录 v3.3 增量，不回填已经冻结的 v3.0 基线或 v3.1/v3.2
维护资料。

## RPC Labels 候选过滤

- [RPC 按 Labels 筛选候选节点路由设计](design/RPC按Labels筛选候选节点路由设计.md)：公共外观、
  组合语义、候选链、Broadcast 边界、性能和生成 ABI。
- [RPC 按 Labels 筛选候选节点路由实施计划](plans/RPC按Labels筛选候选节点路由实施计划.md)：
  实施顺序、质量门禁和范围保护。
- [RPC 按 Labels 筛选候选节点路由验收报告](reports/RPC按Labels筛选候选节点路由验收报告.md)：
  生成检查、单元/真实 TCP/Race、覆盖率、Benchmark 和构建结果。

## v3.3 RC 发布审查

- [v3.3 RC 发布审查报告](reports/v3.3%20RC发布审查报告.md)：汇总 Go 1.27 工具链门槛、
  泛型完成回调、Kafka JSON 兼容性修复、RPC Labels 能力和最终发布门禁。
