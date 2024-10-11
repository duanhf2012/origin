package pubsub

import (
	"testing"
)

const (
	Invalid TopicType = iota
	Topic1
	Topic2
)

var test *testing.T

type Subscriber1 struct {
	BaseSubscriber
}

type Subscriber2 struct {
	BaseSubscriber
}

func (sub *Subscriber1) OnEvent(ctx ...any) {
	test.Log("Subscriber1 OnEvent", " key ", sub.GetKey(), ctx)
}

func (sub *Subscriber2) OnEvent(ctx ...any) {
	test.Log("Subscriber2 OnEvent", " key ", sub.GetKey(), ctx)
}

func TestPubSub(t *testing.T) {
	test = t
	var publisher Publisher

	// 创建3个订阅者
	var subscriber []ISubscriber
	subscriber = append(subscriber, &Subscriber1{}, &Subscriber1{}, &Subscriber2{})

	// 分别注册进Publisher中
	publisher.Subscribe(Topic1, subscriber[0])
	publisher.Subscribe(Topic1, subscriber[1])
	publisher.Subscribe(Topic2, subscriber[2])

	// 发布订阅,两个Subscriber1都会调用OnEvent
	publisher.Publish(Topic1, 1, 2, 3)

	// 删除订阅，Publish后，只有Subscriber1的key2收到
	publisher.UnSubscribeKey(subscriber[0].GetKey())
	publisher.Publish(Topic1, 1, 2, 3)

	// 删除Topic2，Publish将收不到
	publisher.UnSubscribe(Topic2)
	publisher.Publish(Topic2, 1)
}
