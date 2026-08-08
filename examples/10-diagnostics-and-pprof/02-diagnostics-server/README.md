# Diagnostics HTTP

示例将统一诊断快照暴露为只读 HTTP 接口，并且只监听回环地址，方便本机排查而不把运行信息公开到网络。`run` 脚本通过 `--diagnostics 127.0.0.1:6061` 在任何 Service 启动前建立初始 Listener；代码中的同地址 `StartDiagnosticsServer` 是幂等调用，也演示运行期公开外观。

## 运行

执行 `run.bat` 或 `./run.sh` 后保持进程运行，在另一个终端访问：

```bash
# 查询仅绑定到本机回环地址的诊断 JSON 快照。
curl http://127.0.0.1:6061/debug/origin/diagnostics
```

响应是当前时刻重新采集的 JSON 快照。Service 通过 `Application()` 取得受限进程外观，在
`OnStart` 调用 `StartDiagnosticsServer` 并通过 `DiagnosticsAddress` 查询实际地址，在 `OnStop`
显式调用 `StopDiagnosticsServer`；Application 仍提供异常路径兜底关闭。

## 生产提示

非回环监听必须交由反向代理、网络 ACL 和业务认证保护。Server 空闲时不周期采样；每次 GET
才会读取 Go 内存统计、复制 Node/Service 快照并编码 JSON，因此不应按业务请求频率抓取。
通常以秒级间隔供监控适配器或故障采集工具读取即可。

对应教程：[Diagnostics 与 pprof](../../../docs/baseline/v3.0/guides/10.diagnostics-and-pprof.md)。
