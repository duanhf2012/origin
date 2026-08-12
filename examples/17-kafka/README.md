# Kafka Module 示例

| 目录 | 重点 | 建议顺序 |
| --- | --- | --- |
| [01-producer-workflows](./01-producer-workflows/README.md) | RPC 风格异步发送、Delivery 回到 Service、同步确认、Raw/JSON/PB 与批量 | 先运行 |
| [02-consumer-service-handler](./02-consumer-service-handler/README.md) | 单条/批量 Handler、Service 协程、Await、成功 Mark、失败重投与幂等 | 第二步 |
| [03-managed-and-native](./03-managed-and-native/README.md) | Managed Hook、Native Admin、自由配置与资源所有权 | 有特殊需求时 |

先按 [`deploy/kafka`](../../deploy/kafka/README.md) 启动 Kafka 并创建 Topic。默认地址为 `192.168.8.3:9092`；可通过 `ORIGIN_KAFKA_BROKERS` 覆盖。

这些示例不会自动创建 Topic、伪造 Exactly Once 或隐藏重试。批量不是事务；Consumer Handler 必须幂等；Native Sarama 资源必须由业务 Module 在 `OnStart/OnStop` 成对创建和关闭。
