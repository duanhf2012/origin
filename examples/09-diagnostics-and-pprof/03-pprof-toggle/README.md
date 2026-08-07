# 动态启停 pprof

`--pprof` 在日志初始化后、任何 Node 和 Service 启动前绑定端口，适合分析启动过程；它只
决定初始状态，不会把 pprof 永久锁定为开启。运行中仍可调用 `StartPprof(address)` 和
`StopPprof(ctx)`，缩短诊断端口暴露时间。

## 运行与观察

`run` 脚本使用的完整参数包含：

```text
# 在 Service.OnStart 之前启动本机 pprof Listener。
--pprof 127.0.0.1:6060
```

执行 `run.bat` 或 `./run.sh` 后立即访问：

```text
# 动态 pprof Listener 打开时可访问的本机采样入口。
http://127.0.0.1:6060/debug/pprof/
```

示例在两秒后关闭、四秒后重新开启、六秒后再次关闭。`StopPprof` 可能等待正在进行的采样，
所以 Service Task 通过 `Await` 调用；独立 goroutine 可以直接同步调用。真实业务应由经过
认证的管理 RPC、运维控制面或本地管理入口决定何时开始和停止。

## 安全边界

pprof 可能暴露调用栈、内存和 CPU 细节。生产上优先绑定回环地址，通过受认证的跳板、代理或运维通道采集，完成后及时关闭。CPU Profile 和 Trace 是进程级互斥资源；采集本身会增加开销，Heap、goroutine 等大快照也可能短时增加 CPU、内存和响应延迟，不要把 pprof 作为高频监控接口。

对应教程：[Diagnostics 与 pprof](../../../docs/baseline/v3.0/guides/09-diagnostics-and-pprof.md)。
