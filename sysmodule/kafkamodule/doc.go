// Package kafkamodule 提供与 Origin Service 生命周期和串行工作协程集成的 Kafka 能力。
//
// Producer 与 Consumer 是两个独立 Module；常规业务优先使用受管外观，事务、Admin、手工
// Offset 等特殊场景可通过 BuildProducerSaramaConfig、BuildConsumerSaramaConfig 和
// BuildAdminSaramaConfig 创建自由模式配置。
package kafkamodule
