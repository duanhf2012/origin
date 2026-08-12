# Managed 与 Native Sarama

本示例同时使用两个清晰层级：

- `ManagedProducerModule` 由 kafkamodule 管理 Client、队列、Delivery、Drain 与 Stats；Hook 不能关闭完成 Channel、破坏幂等或启用事务；
- `NativeAdminModule` 只借助 `BuildAdminSaramaConfig` 建配置，自己在 `OnStart` 创建 Admin、在 `OnStop` 关闭；网络调用仍从 Service task 放进 `Await`；
- `BuildSaramaConfig` 是事务、手工 Offset、OAuth 和特殊 Consumer 的自由入口，但所有资源、goroutine、错误 Channel 和关闭顺序都由业务负责。

事务骨架只说明配置入口，不构成 Exactly Once。需要 Kafka EOS 时还必须设计事务状态恢复、Offset 同事务提交、未知结果、幂等以及外部数据库一致性；普通 Producer Batch 不具备事务原子性。
