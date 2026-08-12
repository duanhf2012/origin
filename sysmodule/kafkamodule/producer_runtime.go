package kafkamodule

import (
	"context"
	"errors"

	"github.com/IBM/sarama"
)

type producerRuntime interface {
	inputChannel() chan<- *sarama.ProducerMessage
	successChannel() <-chan *sarama.ProducerMessage
	errorChannel() <-chan *sarama.ProducerError
	asyncClose()
	closeClient() error
}

type driverProducerRuntime struct {
	client   sarama.Client
	producer sarama.AsyncProducer
}

func newDriverProducerRuntime(ctx context.Context, brokers []string, current *sarama.Config) (producerRuntime, error) {
	if ctx == nil {
		return nil, ErrInvalidArgument
	}
	client, err := sarama.NewClient(brokers, current)
	if err != nil {
		return nil, err
	}
	if err = ctx.Err(); err != nil {
		return nil, errors.Join(err, client.Close())
	}
	producer, err := sarama.NewAsyncProducerFromClient(client)
	if err != nil {
		return nil, errors.Join(err, client.Close())
	}
	if err = ctx.Err(); err != nil {
		producer.AsyncClose()
		driver := &driverProducerRuntime{client: client, producer: producer}
		drainProducerRuntime(driver)
		return nil, errors.Join(err, client.Close())
	}
	return &driverProducerRuntime{client: client, producer: producer}, nil
}

func (runtime *driverProducerRuntime) inputChannel() chan<- *sarama.ProducerMessage {
	return runtime.producer.Input()
}
func (runtime *driverProducerRuntime) successChannel() <-chan *sarama.ProducerMessage {
	return runtime.producer.Successes()
}
func (runtime *driverProducerRuntime) errorChannel() <-chan *sarama.ProducerError {
	return runtime.producer.Errors()
}
func (runtime *driverProducerRuntime) asyncClose()        { runtime.producer.AsyncClose() }
func (runtime *driverProducerRuntime) closeClient() error { return runtime.client.Close() }
