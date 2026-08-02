# Diagnostics 与 pprof 示例

先从进程内快照开始，再暴露受限 HTTP 诊断接口，随后学习按需启停 pprof 和接入业务监控。诊断数据应统一来自 `Application.Diagnostics()`。

- [01-diagnostics-snapshot](./01-diagnostics-snapshot/README.md)：进程内快照。
- [02-diagnostics-server](./02-diagnostics-server/README.md)：只读诊断 HTTP。
- [03-pprof-toggle](./03-pprof-toggle/README.md)：运行期启停 pprof。
- [04-metrics-adapter](./04-metrics-adapter/README.md)：业务监控适配层。

对应教程：[Diagnostics 与 pprof](../../docs/baseline/v3.0/guides/09-diagnostics-and-pprof.md)。
