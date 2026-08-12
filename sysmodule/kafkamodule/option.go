package kafkamodule

import "github.com/IBM/sarama"

// SaramaConfigHook 在 Origin 配置映射完成后、最终校验前定制 Sarama 配置。
// Hook 在调用 Builder 或 Module 启动的 goroutine 中执行，不在 Service 业务工作协程中执行。
type SaramaConfigHook func(*sarama.Config) error

// ProducerOption 定制受管 Producer 的低频 Sarama 能力。
type ProducerOption interface{ applyProducer(*producerOptions) }
type producerOptionFunc func(*producerOptions)

func (option producerOptionFunc) applyProducer(target *producerOptions) { option(target) }

type producerOptions struct{ hooks []SaramaConfigHook }

// WithProducerSaramaConfig 添加 Producer Sarama Hook；Hook 不能破坏受管模式不变量。
func WithProducerSaramaConfig(hook SaramaConfigHook) ProducerOption {
	return producerOptionFunc(func(target *producerOptions) { target.hooks = append(target.hooks, hook) })
}

// ConsumerOption 定制受管 Consumer 的低频 Sarama 能力。
type ConsumerOption interface{ applyConsumer(*consumerOptions) }
type consumerOptionFunc func(*consumerOptions)

func (option consumerOptionFunc) applyConsumer(target *consumerOptions) { option(target) }

type consumerOptions struct{ hooks []SaramaConfigHook }

// WithConsumerSaramaConfig 添加 Consumer Sarama Hook；Hook 不能破坏受管模式不变量。
func WithConsumerSaramaConfig(hook SaramaConfigHook) ConsumerOption {
	return consumerOptionFunc(func(target *consumerOptions) { target.hooks = append(target.hooks, hook) })
}

// SaramaConfigOption 定制自由模式 Admin Sarama 配置。
type SaramaConfigOption interface{ applySarama(*saramaOptions) }
type saramaConfigOptionFunc func(*saramaOptions)

func (option saramaConfigOptionFunc) applySarama(target *saramaOptions) { option(target) }

type saramaOptions struct{ hooks []SaramaConfigHook }

// WithSaramaConfig 添加自由模式 Sarama Hook。
func WithSaramaConfig(hook SaramaConfigHook) SaramaConfigOption {
	return saramaConfigOptionFunc(func(target *saramaOptions) { target.hooks = append(target.hooks, hook) })
}
