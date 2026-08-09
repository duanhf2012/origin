# 05 动态 pprof

`--pprof` 只决定 Application 启动时是否监听。这个示例同时启动 Admin 和 pprof；代码在
2 秒调用 `StopPprof(ctx)`，4 秒调用 `StartPprof(address)` 并用 `PprofAddress()` 查询实际
地址，6 秒再次关闭。停止 pprof 不影响独立的 Admin Listener。

在 pprof 开启的短窗口内可复制执行：

```bash
go tool pprof "http://127.0.0.1:6060/debug/pprof/profile?seconds=1"
go tool pprof http://127.0.0.1:6060/debug/pprof/heap
curl -s "http://127.0.0.1:6060/debug/pprof/goroutine?debug=1"
go tool pprof http://127.0.0.1:6060/debug/pprof/mutex
curl -o trace.out "http://127.0.0.1:6060/debug/pprof/trace?seconds=1"
go tool trace trace.out
```

CPU profile 和 trace 会保持请求直到采样结束；`StopPprof` 会在 Context 预算内等待活跃请求。
生产排障通常让 Listener 开启足够的短时间后再关闭，并根据问题选择 CPU、heap、goroutine、
mutex 或 trace。不要长期暴露端口，也不要把 pprof 当 Metrics 拉取接口。

Admin 可在 pprof 关闭期间继续访问：

```bash
curl -s http://127.0.0.1:6064/admin/v1/diagnostics
```

两个地址都只绑定回环接口。测试直接驱动抽出的转换函数，不使用真实 sleep 或 Timer。
