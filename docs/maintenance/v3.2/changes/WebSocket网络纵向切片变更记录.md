# WebSocket 网络纵向切片变更记录

> 日期：2026-08-11
> 状态：实现与 Windows/Ubuntu 验收完成

本切片在已经冻结的公共 `network.Session`、`Handler`、容量和 Client/Dialer 外观上增加 WebSocket，
没有修改 TCP 使用者接口，也没有建立 v2 兼容层。

主要变更：

- 新增 `sysmodule/network/websocket` 的 Server、Client、Dialer；
- 新增专用 HTTP Upgrade Listener，支持 Path、安全同源默认值、自定义 Origin、Header、子协议和 TLS；
- 默认使用 Binary Message，可显式选择严格 UTF-8 Text Message；使用 WebSocket 原生消息边界，不套
  长度帧或端序；
- 新增单 Reader/Writer、Ping/Pong、业务读空闲、写超时、标准 Close Code 和消息大小限制；
- 复用公共 Runtime、Buffer Pool、Session/端点字节预算、Service 串行回调与统计；
- 把 TCP 的有界发送 Ring 提取为 `internal/messagequeue`，TCP 使用薄适配保持原行为，WebSocket 直接
  复用同一所有权、水位、慢连接和端点总预算实现；
- Gorilla WebSocket 从间接依赖提升为生产代码的直接依赖，仍保持压缩默认关闭；
- 增加 Binary/Text 服务自调用、Client 重连、Dialer、Origin、Path、TLS、Header/子协议、Ping/Pong、
  连接准入、超大消息、Race、Fuzz 和 Buffer 配平测试；
- 新增 WebSocket 使用指南和带完整中文注释的自调用 Example。

最终 Race 门禁还修正了 TCP 与 WebSocket 回环测试中的两个瞬时时序假设：远端已经收到消息时，
本地 Writer 可能尚未递增发送统计；客户端完成关闭时，服务端最终 `OnClose` 可能仍在 Service
队列中。测试现在等待公开统计和 Session 注销终态，不再把合法异步窗口误判为失败。TCP Broadcast
Race 在 Windows 连续 50 轮、Ubuntu 连续 100 轮复验，Buffer 全部配平。

本切片没有加入公共握手元数据、动态 Middleware、代理抽象、实验性压缩或公开内存池；这些能力
没有当前必要性，避免扩大设计和测试面。
