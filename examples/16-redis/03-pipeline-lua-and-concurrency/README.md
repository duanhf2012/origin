# Redis Pipeline、Lua 与并发

示例展示三种不同语义：Pipeline 只减少 RTT；Watch/TxPipelined 用于乐观并发且冲突只做三次有界重试；
Lua 在 Redis 内完成幂等奖励检查和加金币。Cluster 下相关 Key 使用同一个 `{playerID}` Hash Tag。

运行成功会输出 `Redis pipeline/lua/concurrency demo completed`。Watch 回调可能重入，不能在其中发 RPC、
Kafka、邮件或奖励；Lua 必须短小有界，奖励正确性还应由持久化幂等记录或唯一约束兜底。
