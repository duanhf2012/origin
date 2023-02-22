package event

import "github.com/duanhf2012/origin/util/sync"

// eventPool的内存池,缓存Event
const defaultMaxEventChannelNum = 2000000

var eventPool = sync.NewPoolEx(make(chan sync.IPoolData, defaultMaxEventChannelNum), func() sync.IPoolData {
	return &Event{}
})

func NewEvent() *Event{
	return eventPool.Get().(*Event)
}

func DeleteEvent(event IEvent){
	eventPool.Put(event.(sync.IPoolData))
}

func SetEventPoolSize(eventPoolSize int){
	eventPool = sync.NewPoolEx(make(chan sync.IPoolData, eventPoolSize), func() sync.IPoolData {
		return &Event{}
	})
}
