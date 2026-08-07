# 09：Diagnostics 与 pprof

## 我想在 Service 中读取统一诊断快照

运行：[examples/09-diagnostics-and-pprof/01-diagnostics-snapshot](../../../../examples/09-diagnostics-and-pprof/01-diagnostics-snapshot)。

```go
// 真实 Service 从 OnInit 起可取得受限、并发安全的进程级外观。
runtime := s.Application()
if runtime == nil {
    // 零值 Service、Setup 样本和未装配的测试替身可能返回 nil。
    return errors.New("application runtime is unavailable")
}

// 每次调用都采集一份新的只读快照；不要长期缓存后当作实时状态。
snapshot := runtime.Diagnostics()
// 使用稳定字段上报 Application、Node、Service、RPC、发现、Timer 和 Go Runtime 状态。
report(snapshot)
```

`Application.Diagnostics()` 汇总同一份快照模型；`Node.Diagnostics()`、`ExecutionStats()`、
`TimerStats()` 和 `EventStats()` 适合只关心局部对象的低成本查询。快照不包含配置原文、密码、
Token、RPC Payload、玩家数据、完整远端地址表或 goroutine 栈。

## Application 公开函数可以从哪里调用

管理 Service 优先通过 `s.Application()` 使用以下受限外观：

- `Diagnostics()`；
- `StartDiagnosticsServer`、`StopDiagnosticsServer`、`DiagnosticsAddress`；
- `StartPprof`、`StopPprof`、`PprofAddress`。

该外观故意不开放 `Start`、`Stop`、`Setup`、Node 构建、配置修改和 Provider 注册，避免一个
普通业务 RPC 获得整个进程的任意控制权。业务持有具体 `*application.Application` 时仍可调用
`State`、`Node`、`Nodes`、`Logger`、`Retire` 和 `Resume`，但应把进程控制集中在明确的管理
Service，而不是分散到普通业务代码。

上述诊断外观的方法都可安全地从其他 goroutine 调用；内部会串行化并发启停。调用位置只影响
是否占用 Service 串行执行权：

```go
// Diagnostics 是冷路径快照读取，可在 Service Task 中直接调用。
snapshot := s.Application().Diagnostics()

// StopPprof 可能等待正在进行的 HTTP 采样退出；Service Task 应通过 Await 释放执行权。
err := s.Await(ctx, func(waitCtx context.Context) error {
    return s.Application().StopPprof(waitCtx)
})

// 独立 goroutine 不持有 Service 执行权，可以直接同步等待。
go func() {
    // 生产代码仍需自行提供 Context、panic 边界和退出等待；Service 所有的工作优先用 GoSafe。
    _ = runtime.StopDiagnosticsServer(shutdownCtx)
}()
```

`StartDiagnosticsServer`、`StartPprof` 和地址查询是短操作，可在 Service Task 中直接调用。
`OnStop` 已处于生命周期清理路径，应直接使用它收到的停止 Context；不要再创建无法运行的
Service Await。Application 本身是一次性对象，`app.Start()` 仍只能由程序入口调用一次。

## 我想提供诊断 HTTP 接口

运行：[examples/09-diagnostics-and-pprof/02-diagnostics-server](../../../../examples/09-diagnostics-and-pprof/02-diagnostics-server)。启动参数为：

```text
# 在任何 Node.OnStart 之前建立只读诊断 Listener，绑定失败会使启动失败。
game-server start --app-name game --config ./config --diagnostics 127.0.0.1:6061
```

随后访问：

```bash
# 采集当前进程的一次 JSON 诊断快照。
curl http://127.0.0.1:6061/debug/origin/diagnostics
```

使用场景包括：进程存活但 RPC 变慢时查看队列和超时累计、发现异常时确认 Provider 与目录状态、
退休或停止卡住时确认具体 Service 状态，以及让业务监控适配器把统一快照转换为自己的指标。
它是只读故障观测入口，不提供退休、停止、配置修改或任意 RPC 调用。

Server 空闲时不会周期采样，只有一个 Listener 和 HTTP goroutine；每次 GET 才执行
`runtime.ReadMemStats`、复制 Node/Service 快照并编码 JSON。成本随真实 Node/Service 数量线性
增长，不会暂停 Scheduler 或持有整个 Application 大锁。M22 的 Windows 基线中，64 Node ×
64 Service 的纯快照聚合中位数约 `0.91 ms/op`、约 `1.43 MB/op`；这不包含 HTTP JSON 编码，
也不是所有机器的性能承诺。因此应按秒级而非每请求/毫秒级采集；多个监控消费者应由业务
适配层统一采集和缓存一次结果。

默认只绑定回环地址。Origin 不内置 TLS 和认证；非回环监听会记录安全警告，生产环境必须用
内网 ACL、受认证反向代理、Sidecar 或安全隧道保护。

## 我想在启动阶段开启 pprof

运行：[examples/09-diagnostics-and-pprof/03-pprof-toggle](../../../../examples/09-diagnostics-and-pprof/03-pprof-toggle)。

```text
# 日志初始化后、任何 Node 生命周期前打开 pprof，因此可以分析启动过程。
game-server start --app-name game --config ./config --pprof 127.0.0.1:6060
```

常用入口：

```text
# 查看可用 Profile。
http://127.0.0.1:6060/debug/pprof/
# 采集 30 秒 CPU Profile。
http://127.0.0.1:6060/debug/pprof/profile?seconds=30
# 查看 goroutine 快照。
http://127.0.0.1:6060/debug/pprof/goroutine?debug=1
```

`--pprof` 只决定初始状态。运行中仍可调用 `StartPprof(address)`、`PprofAddress()` 和
`StopPprof(ctx)`，示例会依次关闭、重开和再次关闭 Listener。适合的场景是 CPU 突增、内存
持续增长、goroutine 泄漏、锁竞争或启动阶段变慢时进行短时定位。

pprof 不是普通监控：CPU Profile 和 Trace 是进程级互斥采集，采样会增加运行开销；Heap、
goroutine、mutex 等大快照也可能短时增加 CPU、内存和响应延迟。只在需要时开启和采集，
完成后关闭；不要高频抓取，也不要直接暴露到公网。Diagnostics 与 pprof 使用独立 Listener，
关闭 pprof 不影响持续的 JSON 诊断接口。

## 我想接入自己的监控系统

运行：[examples/09-diagnostics-and-pprof/04-metrics-adapter](../../../../examples/09-diagnostics-and-pprof/04-metrics-adapter)。示例把 `diagnostics.Source` 转换为业务自定义指标，不直接绑定 Prometheus。业务可在适配层决定采集周期、缓存、指标名、标签上限和 Prometheus/OpenTelemetry 导出方式，Origin 的 RPC 热路径不会因此增加动态标签或逐请求 Histogram。
