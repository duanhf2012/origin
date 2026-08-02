# 动态启停 pprof

pprof 不需要在进程启动时永久开启。应用可以在受控场景调用 `StartPprof(address)`，采集完成后调用 `StopPprof(ctx)`，缩短诊断端口暴露时间。

## 运行与观察

执行 `run.bat` 或 `./run.sh` 后立即访问：

```text
http://127.0.0.1:6060/debug/pprof/
```

示例两秒后自动关闭 Listener；刷新页面将无法连接。这是为了演示状态切换，真实业务应由受控运维入口决定何时开始和停止。

## 安全边界

pprof 可能暴露调用栈、内存和 CPU 细节。生产上优先绑定回环地址，通过受认证的跳板、代理或运维通道采集，完成后及时关闭。

对应教程：[Diagnostics 与 pprof](../../../docs/baseline/v3.0/guides/09-diagnostics-and-pprof.md)。
