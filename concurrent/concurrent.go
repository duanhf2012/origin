package concurrent

import (
	"errors"
	"runtime"

	"github.com/duanhf2012/origin/log"
)

const defaultMaxTaskChannelNum = 1000000

type IConcurrent interface {
	OpenConcurrentByNumCPU(cpuMul float32)
	OpenConcurrent(minGoroutineNum int32, maxGoroutineNum int32, maxTaskChannelNum int)
	AsyncDoByQueue(queueId int64, fn func() bool, cb func(err error))
	AsyncDo(f func() bool, cb func(err error))
}

type Concurrent struct {
	dispatch

	tasks     chan task
	cbChannel chan func(error)
}

/*
cpuMul 表示cpu的倍数
建议:(1)cpu密集型 使用1  (2)i/o密集型使用2或者更高
*/
func (c *Concurrent) OpenConcurrentByNumCPU(cpuNumMul float32) {
	goroutineNum := int32(float32(runtime.NumCPU())*cpuNumMul + 1)
	c.OpenConcurrent(goroutineNum, goroutineNum, defaultMaxTaskChannelNum)
}

func (c *Concurrent) OpenConcurrent(minGoroutineNum int32, maxGoroutineNum int32, maxTaskChannelNum int) {
	c.tasks = make(chan task, maxTaskChannelNum)
	c.cbChannel = make(chan func(error), maxTaskChannelNum)

	//打开dispach
	c.dispatch.open(minGoroutineNum, maxGoroutineNum, c.tasks, c.cbChannel)
}

func (c *Concurrent) AsyncDo(f func() bool, cb func(err error)) {
	c.AsyncDoByQueue(0, f, cb)
}

func (c *Concurrent) AsyncDoByQueue(queueId int64, fn func() bool, cb func(err error)) {
	if cap(c.tasks) == 0 {
		panic("not open concurrent")
	}

	if fn == nil && cb == nil {
		log.SStack("fn and cb is nil")
		return
	}

	if fn == nil {
		c.pushAsyncDoCallbackEvent(cb)
		return
	}

	if queueId != 0 {
		queueId = queueId % maxTaskQueueSessionId+1
	}

	select {
	case c.tasks <- task{queueId, fn, cb}:
	default:
		log.SError("tasks channel is full")
		if cb != nil {
			c.pushAsyncDoCallbackEvent(func(err error) {
				cb(errors.New("tasks channel is full"))
			})
		}
		return
	}
}

func (c *Concurrent) Close() {
	if cap(c.tasks) == 0 {
		return
	}

	log.SRelease("wait close concurrent")

	c.dispatch.close()

	log.SRelease("concurrent has successfully exited")
}

func (c *Concurrent) GetCallBackChannel() chan func(error) {
	return c.cbChannel
}
