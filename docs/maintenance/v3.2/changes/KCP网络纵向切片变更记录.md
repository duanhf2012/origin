# KCP 网络纵向切片变更记录

> 日期：2026-08-11
> 状态：实现与 Windows/Ubuntu 验收完成

本切片在已经人工确认的公共 `network.Session`、`Handler`、Server、Client、Dialer 外观上增加 KCP，
没有修改使用者公共网络契约，也没有建立 v2 兼容层。

主要变更：

- 新增 `sysmodule/network/kcp` 的 Server、Client、Dialer、Client 有界重连和状态统计；
- 新增 `internal/kcpnet`，实现单 Reader/Writer、长度帧、有界发送队列、超时、连接准入和确定性停止；
- 抽取无分配的 `internal/lengthframe` 供 TCP/KCP 共用，TCP 行为与测试保持不变；
- 支持 1/2/4 字节长度字段、Big/Little Endian、MTU、发送/接收窗口、NoDelay、ACK NoDelay、
  Write Delay、FEC、DSCP、UDP Socket Buffer 和代码注入的 `BlockCrypt`；
- KCP 固定使用 Stream Mode；长度头与 Payload 通过 `WriteBuffers` 一次提交，不拼接完整消息；
- 严格限制 `MTU + 加密头 + FEC 头 <= 1500`，避免触发 `kcp-go` 固定报文缓冲边界；
- 明确 KCP 无远端握手、无 FIN/标准 Close 帧：`OnOpen` 只代表本地 UDP Session 就绪，静默对端
  依赖正数读空闲和业务心跳发现；
- 新增独立 `ServerConfig`、`ClientConfig`、完整默认值与严格 Service 配置转换；Dialer 是一次性
  代码对象，只使用 `DialOptions`，不进入 Service 配置；
- Config 保留 v2 真正有效的 KCP 参数，删除可变 StreamMode、重复读写消息上限和单一
  `PendingWriteNum`；不增加具有错误握手暗示的 `dial_timeout`，也不允许静态密钥进入 YAML；
- 新增配置驱动、由业务 Module 组合 Server/Client 的 KCP 服务自调用 Example 和使用指南；
- 新增长度帧 Fuzz、全部帧/端序、FEC/AES、读空闲、重连、容量拒绝、关闭语义、Race、弱网和
  双平台真实 UDP 测试。

依赖记录：

| 依赖 | 版本 | 许可证 | 用途 |
| --- | --- | --- | --- |
| `github.com/xtaci/kcp-go/v5` | `v5.6.18` | MIT | KCP Session、Listener、FEC/BlockCrypt 接口 |
| `github.com/klauspost/reedsolomon` | `v1.12.0` | MIT | 在创建 socket 前验证 FEC 分片组合 |

实现中没有新增公共内存池、引用计数、动态热更新、DUP、优先级队列或额外业务事件体系。现有
Buffer Pool、公共 Runtime、容量预算和消息队列已经满足当前功能；Benchmark 没有证明需要扩大性能
优化范围。

真实 Example 最终验收发现并修复了一项关闭分类问题：`kcp-go` Listener 与 Session 共享 UDP socket，
若先关闭 Listener，活动 Session 可能把本地主动停止误报为 `TransportUnavailable`。现在先给 Session
提交 `TransportClosed`，再关闭共享 socket；Windows/Ubuntu 高重复与 Race 测试均已锁定该行为。
