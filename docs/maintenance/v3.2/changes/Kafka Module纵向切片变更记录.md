# Kafka Module 纵向切片变更记录

## 外观

- 新增独立 `Producer` 与 `Consumer` Module，一个实例对应一个逻辑 Kafka 集群。
- 新增 Raw/JSON/PB 的单条、批量、同步和异步对称外观。
- 新增可重复读取的 `Delivery`、Service 完成回调、原子 Stats、Pause/Resume 和 LastError。
- 新增 `BuildSaramaConfig`、Managed Producer/Consumer Builder 与 Admin Builder。

## 可靠性

- Producer 使用单 AsyncProducer 内核和消息数/字节双有界容量，预算持有到 Delivery。
- Consumer 不增加第二层消息队列；同 Partition 等待 Service Handler，成功后才 Mark。
- Handler panic 脱敏、失败不 Mark；基础设施恢复使用单循环有界退避和抖动。
- Pause 意图在 Claim/Rebalance 后重放，覆盖刚启动时 Claim 尚未建立的窗口。

## 驱动与环境

- IBM Sarama `v1.60.1`、Sonic `v1.15.2`、xdg-go/scram `v1.2.0`。
- 新增 Apache Kafka 4.3.1 KRaft Compose、持久 Volume、健康检查和显式 Topic 脚本。
- 新增 Windows/Ubuntu Raw、JSON、PB、Tombstone、批量、Offset 重投、Pause 和恢复测试。

## 明确不做

不兼容 v2 API；不自动建 Topic、死信、无限业务重试、Schema Registry、Outbox、Managed 事务/EOS、对象池或全局 Registry。
