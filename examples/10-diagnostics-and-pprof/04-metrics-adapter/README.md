# 业务监控适配

框架提供 `diagnostics.Source` 和统一快照，而不是硬编码 Prometheus 或特定监控 SDK。业务可在一个适配层中把需要的快照字段转换成自己的 Gauge、日志或遥测事件。

## 示例流程

示例定义最小 `GaugeSink` 并把两项快照数值写到控制台。替换这个 Sink 的实现即可接入 Prometheus、OpenTelemetry 或公司监控，不需要在 Application、Node、RPC、Service 等多处插入采集代码。

## 运行与练习

执行 `run.bat` 或 `./run.sh`，观察输出的指标名和值。可新增自己的 Sink 实现，注意采集失败不能影响业务调度，也不要把高基数业务 ID 作为指标标签。

对应教程：[Diagnostics 与 pprof](../../../docs/baseline/v3.0/guides/10.diagnostics-and-pprof.md)。
