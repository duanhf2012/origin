# Admin 管理 HTTP、Diagnostics 与 pprof 变更摘要

> 状态：已实施
> 基线：v3.0
> 目标版本：v3.1.0
> 兼容性：删除旧 Diagnostics HTTP 入口；保留本地 Full Snapshot v2 与独立 pprof Listener
> 关联提交：本文件所在提交

## 使用者可见变更

- 新增唯一 `--admin <address>` 管理 HTTP 入口，默认关闭。它提供 Diagnostics、固定的
  Application/Node/Service Retire/Resume，以及业务注册的 GET/POST Endpoint。
- 新增 `admin` 包：Service 通过 `AdminEndpoints() []admin.Endpoint` 声明端点；Application
  通过 `RegisterAdminEndpoint` 声明进程级端点。Service Handler 由目标 Service 的现有有界 FIFO
  串行执行，Application Handler 仅能访问并发安全的进程级数据。
- 新增 `SetAdminGuard`。无 Guard 只允许环回绑定；Guard 按 GET/POST 与固定目标授权。Admin
  统一执行 Body/响应/并发/Deadline 限制、错误映射和脱敏审计。
- `GET /admin/v1/diagnostics` 默认返回低基数 Summary schema v2；
  `GET /admin/v1/diagnostics?detail=full` 返回详细 Full Snapshot schema v2。
- Summary v2 仅保留 Listener 健康、Go Runtime、Node 可用性、聚合 RPC 与聚合 Service 工作；它
  不再输出 Listener 地址、`heap_objects`、逐 Transport RPC 或逐 Service DTO。新增
  `heap_goal_bytes`、`memory_limit_configured`，并将无限 GOMEMLIMIT 表示为 `false`/`0`。

## 删除与迁移

- 删除 `--diagnostics`、`StartDiagnosticsServer`、`StopDiagnosticsServer`、
  `DiagnosticsAddress`、独立 Diagnostics Listener 和 `/debug/origin/diagnostics`。
- 本地完整诊断继续使用 `Application.Diagnostics()`。Full v2 保留详细 Listener 地址、逐
  Transport/Service 数据；历史 JSON 消费者所见的 `application.diagnostics_server` 仍保留为
  Deprecated 的 `stopped` 占位。
- `--pprof`、`StartPprof`、`PprofAddress`、`StopPprof` 保持独立 Listener 语义。命令行只决定
  初始状态，运行中仍可关闭、重开并再次关闭。

## 边界与示例

- Admin 空闲时不周期采样 Diagnostics；实际请求会读取 Runtime 并聚合 Node/Service/RPC/Timer/
  Event 后编码 JSON。Summary 适合秒级监控，Full 与 pprof 适合按需排障。
- OS RSS、容器 working set/limit、进程 CPU、宿主机负载和网络吞吐继续由外部系统指标采集；
  pprof 不是 Metrics API。
- 完整用法、API 路径、迁移与六组可运行程序见
  [第 10 章教程](../guides/10.admin-diagnostics-and-pprof.md) 和
  [`examples/10-admin-diagnostics-and-pprof`](../../../../examples/10-admin-diagnostics-and-pprof/README.md)。
