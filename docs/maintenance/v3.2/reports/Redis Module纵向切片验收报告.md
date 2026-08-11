# Redis Module 纵向切片验收报告

> 日期：2026-08-12
> 结论：通过
> 范围：`sysmodule/redismodule`、`examples/16-redis`、使用指南和入口索引

## 验收结果

| 项目 | 结果 | 证据 |
| --- | --- | --- |
| Windows 单测/示例 | 通过 | 包单测、四个示例编译、`go vet`、GoDoc Example |
| Ubuntu 竞态 | 通过 | 全拓扑和安全端点环境执行 `go test -race` |
| Redis 7.2 | 通过 | Docker Redis 7.2.15 Standalone 全包测试 |
| Redis 8.x | 通过 | Docker Redis 8.10.0 Standalone 全包测试 |
| Sentinel | 通过 | 三 Sentinel + Master/Replica，受控真实 Failover 后同一 Module 恢复读写 |
| Cluster | 通过 | 3 Primary + 3 Replica，启动、读写、Hash Tag、跨 Slot 拒绝和 Cluster Scan 边界 |
| TLS/ACL | 通过 | 私有 CA、主机名/IP 校验、自定义 ACL 用户、正确/错误凭证启动行为 |
| 连接池边界 | 通过 | `MaxActiveConnections=1` 时阻塞占用，第二请求在 PoolTimeout 内有界失败 |
| 精确整数 | 通过 | `±(2^53-1)`、小数读取、递增溢出和无写入回归 |
| 覆盖率 | 84.1% statements | 真实 Redis、三拓扑、TLS/ACL 与 Failover 合并覆盖 |
| Example 实跑 | 通过 | Ubuntu 依次完成缓存/会话、集合/排行、Pipeline/Lua、分布式锁业务日志 |

重点内部边界函数中，参数/Slot/数量/分数校验、OptionalString 转换、整数转换和启动失败清理达到 100%；
配置标准化为 97.7%，锁核心路径为 84.6%～100%。总覆盖率未以无意义 Mock 强行追求 100%：大量一行命令包装还包含
底层 I/O 异常分支，网络写入“服务端已执行但响应丢失”、操作系统证书池失败、特定 MOVED/ASK 时序无法稳定、无副作用地逐条触发。
这些分支通过统一错误链、真实拓扑、竞态和故障测试覆盖公共机制，并在 GoDoc/教程中明确剩余语义。

## 性能基线

Ubuntu AMD Ryzen 7 7840HS、Redis 本机容器、Go 1.26.5，`-benchtime=1s -count=3`：

| Benchmark | 结果 |
| --- | --- |
| `OptionalStrings` | 35.70～36.31 ns/op，96 B/op，1 alloc/op |
| 便利层 `Get` | 37.43～37.96 μs/op，224 B/op，7 allocs/op |
| 官方 Client `Get` | 36.79～39.98 μs/op，224 B/op，7 allocs/op |

便利层没有增加可测的额外分配，延迟差异处于本机网络波动范围，因此不加入对象池或自定义协议优化。

## 环境保留

Ubuntu 保留 `restart unless-stopped` 的 Redis 7.2、7.4、8.10、三 Sentinel、Sentinel Master/Replica、六节点 Cluster
和 TLS/ACL 容器，供后续回归复用。测试凭证只用于隔离环境，未写入仓库。

## 已知边界

- 命令超时不等于服务端一定未执行；非幂等命令默认不自动重试。
- Sentinel/Cluster 切换窗口中的在途命令可能失败，业务仍需 Context、幂等和有界重试策略。
- Redis Lease 不能替代货币、奖励、支付或跨系统事务的最终幂等与持久化约束。
- Cluster Replica Read 默认关闭；启用后业务必须接受复制延迟。

验收范围内没有已知未修复功能缺陷。上述边界属于 Redis 分布式语义，已进入公共 GoDoc、指南和示例，而不是由 Module 隐藏。
