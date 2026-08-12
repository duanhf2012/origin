package kafkamodule

import (
	"context"
	"errors"

	"github.com/IBM/sarama"
)

type consumerRuntime interface {
	consume(context.Context, []string, sarama.ConsumerGroupHandler) error
	errors() <-chan error
	close() error
	pause(map[string][]int32)
	resume(map[string][]int32)
	pauseAll()
	resumeAll()
}

type driverConsumerRuntime struct {
	client sarama.Client
	group  sarama.ConsumerGroup
}

func newDriverConsumerRuntime(ctx context.Context, brokers []string, groupID string, current *sarama.Config) (consumerRuntime, error) {
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
	group, err := sarama.NewConsumerGroupFromClient(groupID, client)
	if err != nil {
		return nil, errors.Join(err, client.Close())
	}
	if err = ctx.Err(); err != nil {
		drained := make(chan struct{})
		go func() {
			for range group.Errors() {
			}
			close(drained)
		}()
		closeErr := group.Close()
		<-drained
		return nil, errors.Join(err, closeErr, client.Close())
	}
	return &driverConsumerRuntime{client: client, group: group}, nil
}

func (runtime *driverConsumerRuntime) consume(ctx context.Context, topics []string, handler sarama.ConsumerGroupHandler) error {
	return runtime.group.Consume(ctx, topics, handler)
}
func (runtime *driverConsumerRuntime) errors() <-chan error { return runtime.group.Errors() }
func (runtime *driverConsumerRuntime) close() error {
	return errors.Join(runtime.group.Close(), runtime.client.Close())
}
func (runtime *driverConsumerRuntime) pause(partitions map[string][]int32) {
	runtime.group.Pause(partitions)
}
func (runtime *driverConsumerRuntime) resume(partitions map[string][]int32) {
	runtime.group.Resume(partitions)
}
func (runtime *driverConsumerRuntime) pauseAll()  { runtime.group.PauseAll() }
func (runtime *driverConsumerRuntime) resumeAll() { runtime.group.ResumeAll() }
