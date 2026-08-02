# 09：Diagnostics 与 pprof

## 我想在代码中获取统一诊断快照

运行：[examples/09-diagnostics-and-pprof/01-diagnostics-snapshot](../../../../examples/09-diagnostics-and-pprof/01-diagnostics-snapshot)。

```go
snapshot := app.Diagnostics()
// snapshot 包含 Application、Go Runtime、Node、Service、RPC、发现和 Timer 状态。
```

诊断快照是某一时刻的只读聚合，用于排错、运维采集和业务侧监控适配，不是持续监控系统本身。

## 我想提供诊断 HTTP 接口

运行：[examples/09-diagnostics-and-pprof/02-diagnostics-server](../../../../examples/09-diagnostics-and-pprof/02-diagnostics-server)。

```go
app.StartDiagnosticsServer("127.0.0.1:6061")
```

访问 `http://127.0.0.1:6061/debug/origin/diagnostics` 获取 JSON。生产环境不要直接暴露到公网；框架会对非回环监听发出无内建 TLS/认证警告。

## 我想按需启动和关闭 pprof

运行：[examples/09-diagnostics-and-pprof/03-pprof-toggle](../../../../examples/09-diagnostics-and-pprof/03-pprof-toggle)。

```go
app.StartPprof("127.0.0.1:6060")
defer app.StopPprof(ctx)
```

这比固定命令行 `--pprof` 更适合线上临时诊断：业务可以在运行期明确开启、收集、关闭。

## 我想接入自己的监控系统

运行：[examples/09-diagnostics-and-pprof/04-metrics-adapter](../../../../examples/09-diagnostics-and-pprof/04-metrics-adapter)。示例只把快照转换为业务自定义接口，不直接绑定 Prometheus；后续可由业务层适配 Prometheus、OpenTelemetry 或其他系统。
