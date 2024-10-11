package pubsub

import (
	"container/list"
	"sync/atomic"
)

type TopicType int
type Key uint64

type IBaseSubscriber interface {
	OnSubscribe(key Key)
	GetKey() Key
}

type ISubscriber interface {
	IBaseSubscriber
	OnEvent(ctx ...any)
}

type IPublisher interface {
	Publish(topic TopicType, ctx ...any)
	Subscribe(topic TopicType, sub ISubscriber)
	UnSubscribe(topic TopicType)
	UnSubscribeKey(key Key)
}

var keyID uint64

func genKeyID() Key {
	return Key(atomic.AddUint64(&keyID, 1))
}

type KeyData struct {
	subscriber ISubscriber
	topicType  TopicType
	keyElement *list.Element
}

type SubscriberSet map[Key]KeyData
type TopicSet map[TopicType]*list.List

type Publisher struct {
	subscriberSet SubscriberSet
	topicSet      TopicSet
}

func (set *SubscriberSet) init() {
	*set = make(SubscriberSet, 64)
}

func (set *SubscriberSet) add(keyElement *list.Element, topicType TopicType, subscriber ISubscriber) {
	(*set)[keyElement.Value.(Key)] = KeyData{subscriber: subscriber, topicType: topicType, keyElement: keyElement}
}

func (set *SubscriberSet) del(key Key) {
	delete(*set, key)
}

func (set *SubscriberSet) get(key Key) (KeyData, bool) {
	keyData, ok := (*set)[key]
	if !ok {
		return keyData, false
	}

	return keyData, true
}

func (set *TopicSet) init() {
	*set = make(TopicSet, 64)
}

func (set *TopicSet) add(topic TopicType, key Key) *list.Element {
	keyList := (*set)[topic]
	if keyList == nil {
		keyList = list.New()
		(*set)[topic] = keyList
	}

	return keyList.PushBack(key)
}

func (set *TopicSet) del(topic TopicType, keyElement *list.Element) {
	keyList := (*set)[topic]
	if keyList == nil {
		return
	}

	keyList.Remove(keyElement)
}

func (set *TopicSet) foreach(topic TopicType, cb func(key Key)) {
	keyList := (*set)[topic]
	if keyList == nil {
		return
	}
	for e := keyList.Front(); e != nil; e = e.Next() {
		cb(e.Value.(Key))
	}
}

type BaseSubscriber struct {
	key Key
}

func (bs *BaseSubscriber) OnSubscribe(key Key) {
	bs.key = key
}

func (bs *BaseSubscriber) GetKey() Key {
	return bs.key
}

func (pub *Publisher) lazyInit() {
	if pub.subscriberSet == nil {
		pub.subscriberSet.init()
	}
	if pub.topicSet == nil {
		pub.topicSet.init()
	}
}

func (pub *Publisher) add(topic TopicType, sub ISubscriber) Key {
	key := genKeyID()
	ele := pub.topicSet.add(topic, key)
	pub.subscriberSet.add(ele, topic, sub)

	return key
}

func (pub *Publisher) Publish(topic TopicType, ctx ...any) {
	pub.lazyInit()
	pub.topicSet.foreach(topic, func(key Key) {
		keyData, ok := pub.subscriberSet.get(key)
		if ok == false {
			return
		}
		keyData.subscriber.OnEvent(ctx...)
	})
}

func (pub *Publisher) Subscribe(topic TopicType, sub ISubscriber) bool {
	if topic == 0 {
		return false
	}

	pub.lazyInit()
	sub.OnSubscribe(pub.add(topic, sub))
	return true
}

func (pub *Publisher) UnSubscribe(topic TopicType) {
	keyList := pub.topicSet[topic]
	if keyList == nil {
		return
	}

	for e := keyList.Front(); e != nil; e = e.Next() {
		pub.subscriberSet.del(e.Value.(Key))
	}

	delete(pub.topicSet, topic)
}

func (pub *Publisher) UnSubscribeKey(key Key) {
	keyData, ok := pub.subscriberSet.get(key)
	if ok == false {
		return
	}

	pub.topicSet.del(keyData.topicType, keyData.keyElement)
	pub.subscriberSet.del(key)
}
