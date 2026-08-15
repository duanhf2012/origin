# Origin v3.2.1 文档

v3.2.1 修正 TCP、WebSocket、KCP 公共 `network.SessionID` 的身份范围：从端点 Runtime
局部递增整数改为跨传输、跨 Module、跨进程生命周期工程上实际唯一的 27 字符
时间域与安全随机 Base64URL 字符串；同时把 Origin RPC 单个业务 payload 的默认及固定
硬上限由 4 MiB 调整为 32 MiB。

- [网络 Session 全局唯一字符串 ID 设计](design/网络Session全局唯一字符串ID设计.md)
- [网络 Session 全局唯一字符串 ID 实施计划](plans/网络Session全局唯一字符串ID实施计划.md)
- [网络 Session 全局唯一字符串 ID 验收报告](reports/网络Session全局唯一字符串ID验收报告.md)
- [RPC 单 Payload 固定上限 32 MiB 设计](design/RPC单Payload固定上限32MiB设计.md)
- [RPC 单 Payload 固定上限 32 MiB 实施计划](plans/RPC单Payload固定上限32MiB实施计划.md)
- [RPC 单 Payload 固定上限 32 MiB 验收报告](reports/RPC单Payload固定上限32MiB验收报告.md)

本目录只记录 v3.2.1 增量；v3.2.0 与 v3.3 资料保持各自版本边界。
