# Diagnostics HTTP

示例将统一诊断快照暴露为只读 HTTP 接口，并且只监听回环地址，方便本机排查而不把运行信息公开到网络。

## 运行

执行 `run.bat` 或 `./run.sh` 后保持进程运行，在另一个终端访问：

```bash
curl http://127.0.0.1:6061/debug/origin/diagnostics
```

响应是 JSON 快照。Service 在 `OnStart` 调用 `StartDiagnosticsServer` 并通过 `DiagnosticsAddress` 查询实际地址，在 `OnStop` 显式调用 `StopDiagnosticsServer`；Application 仍提供异常路径兜底关闭。

## 生产提示

非回环监听必须交由反向代理、网络 ACL 和业务认证保护。该接口用于诊断，不应直接当作高频指标采集端点；监控系统应使用适配层定期读取并转换快照。

对应教程：[Diagnostics 与 pprof](../../../docs/baseline/v3.0/guides/09-diagnostics-and-pprof.md)。
