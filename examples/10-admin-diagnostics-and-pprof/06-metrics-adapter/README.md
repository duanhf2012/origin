# 06 Metrics Adapter

监控周期内先调用一次 `Application.DiagnosticsSummary()`，再把返回值缓存成 `metricsBatch`，
供 Prometheus、OpenTelemetry、日志或其他消费者复用。这样所有消费者看到同一轮采样，也不会
为每个输出端重复聚合 Runtime、Node、Service、RPC、Timer 与 Event。

```go
batch := collectMetrics(app) // 本轮只采样一次
batch.Publish(prometheusSink)
batch.Publish(openTelemetrySink)
```

缓存只服务于当前采集周期；不要把旧 Summary 永久当成实时状态。采集频率应从秒级开始，
结合实际 Node 数、响应成本和监控需要调整。

Summary 中的 `go_memory_used_bytes`、Heap 和 GC 是 Go Runtime 口径，不等于操作系统 RSS。
以下指标应由进程/容器外部监控采集，再与 Origin Summary 关联：

- OS RSS、文件描述符和系统 CPU；
- 容器 working set、memory limit/throttling；
- 进程 CPU、宿主机负载和网络吞吐。

pprof 用于短时定位 CPU、内存分配、goroutine 或锁竞争，不是 Metrics API，也不应被周期抓取。

运行本地控制台适配器：

```bash
./examples/10-admin-diagnostics-and-pprof/06-metrics-adapter/run.sh
```
