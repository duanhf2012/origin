# Origin v3.2 文档

v3.2 已进入纵向切片实施阶段。公共网络契约、TCP、WebSocket 和 KCP 已完成对应纵向切片；KCP
运行时默认值经过 Windows 与 Ubuntu 弱网验证后，已提供严格 Service Config。

## 网络模块教程

TCP、WebSocket 与 KCP 使用相同的 `network.Session`、`Handler`、Server、Client、Dialer、容量和错误
语义。建议先运行对应的 Raw 回环 Example，确认生命周期和服务自调用，再阅读使用指南接入 PB、
JSON 或自定义 Codec。

| 网络模块 | 适用场景 | 使用指南 | Example |
| --- | --- | --- | --- |
| TCP | 游戏客户端、自定义长度帧、直接二进制长连接 | [TCP 网络模块使用指南](guides/TCP网络模块使用指南.md) | [TCP Raw 服务自调用](../../../examples/13-network/01-tcp-raw-self-call/README.md) |
| WebSocket | 浏览器、网关、HTTP Upgrade、WS/WSS 长连接 | [WebSocket 网络模块使用指南](guides/WebSocket网络模块使用指南.md) | [WebSocket Raw 服务自调用](../../../examples/13-network/02-websocket-raw-self-call/README.md) |
| KCP | UDP 弱网、低时延游戏长连接 | [KCP 网络模块使用指南](guides/KCP网络模块使用指南.md) | [KCP Raw 服务自调用](../../../examples/13-network/03-kcp-raw-self-call/README.md) |

全部网络 Example 见 [`examples/13-network`](../../../examples/13-network/README.md)。三个示例都从完整
默认配置开始，通过所属 Service 的严格配置覆盖启动。

## 设计与实施资料

本目录沿用 `../v3.1/README.md` 定义的文档分层：

- `proposals/`：能力分析、问题定义和范围提案；
- `design/`：通过提案确认后形成的正式设计；
- `plans/`：设计明确允许实施后形成的执行计划；
- `changes/`：实施过程中的变更记录；
- `reports/`：测试、稳定性和验收报告；
- `guides/`：面向使用者的教程与迁移说明。

当前入口：

- [`Origin 网络系统模块能力分析与设计提案`](proposals/Origin网络系统模块能力分析与设计提案.md)
- [`Origin 网络事件与自定义协议扩展调研`](proposals/Origin网络事件与自定义协议扩展调研.md)
- [`Origin 网络模块核心设计`](design/Origin网络模块核心设计.md)：已经确认的单一实现依据，包含
  公共 API、内存池、消息队列、所有权、背压、协议和实施门禁。

前两份文档保存能力分析和调研依据，不单独授权实现。核心设计已允许按 TCP、WebSocket、KCP
纵向切片实施；每个切片仍须独立完成计划、测试、文档和验收。

当前实施计划：

- [`TCP 网络首批纵向切片实施计划`](plans/TCP网络首批纵向切片实施计划.md)
- [`WebSocket 网络纵向切片实施计划`](plans/WebSocket网络纵向切片实施计划.md)
- [`KCP 网络纵向切片实施计划`](plans/KCP网络纵向切片实施计划.md)

当前变更与验收材料：

- [`TCP 网络首批纵向切片变更记录`](changes/TCP网络首批纵向切片变更记录.md)
- [`TCP 网络首批纵向切片验收报告`](reports/TCP网络首批纵向切片验收报告.md)
- [`WebSocket 网络纵向切片变更记录`](changes/WebSocket网络纵向切片变更记录.md)
- [`WebSocket 网络纵向切片验收报告`](reports/WebSocket网络纵向切片验收报告.md)
- [`KCP 网络纵向切片变更记录`](changes/KCP网络纵向切片变更记录.md)
- [`KCP 网络纵向切片验收报告`](reports/KCP网络纵向切片验收报告.md)

v3.1 的维护资料继续保留在 `../v3.1/`，不得混入本目录。
