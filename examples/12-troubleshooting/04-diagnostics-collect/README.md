# 诊断收集练习

诊断快照适合在故障发生时保存一个带时间点的运行视图，再交给人工分析、工单或业务监控。该示例通过 Admin Diagnostics 按需请求 Full Snapshot，并把 JSON 响应保存到本目录。

## 运行

先在一个终端执行 `run-server.bat` 或 `./run-server.sh`，保持回环地址上的 Admin 服务运行；再在第二个终端执行 `collect.bat` 或 `./collect.sh`。采集脚本请求 `/admin/v1/diagnostics?detail=full`，结果写入 `diagnostics.json`，该文件已被 Git 忽略。

## 如何使用结果

检查 Application 状态、Node 状态、Service 数量和传输/发现信息，结合启动日志与错误码定位问题。快照可能含运行拓扑信息，导出到外部系统前应按组织安全要求脱敏和控制访问。

对应教程：[Diagnostics 与 pprof](../../../docs/baseline/v3.0/guides/10.diagnostics-and-pprof.md) 与 [故障排查](../../../docs/baseline/v3.0/guides/12.troubleshooting.md)。
