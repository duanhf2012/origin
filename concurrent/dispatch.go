package concurrent

import (
	"sync"
	"sync/atomic"
	"time"

	"fmt"
	"runtime"

	"context"
	"github.com/duanhf2012/origin/v2/log"
	"github.com/duanhf2012/origin/v2/util/queue"
)

var idleTimeout = int64(2 * time.Second)

const maxTaskQueueSessionId = 10000

type dispatch struct {
	minConcurrentNum int32
	maxConcurrentNum int32

	queueIdChannel chan int64
	workerQueue    chan task
	tasks          chan task
	idle           bool
	workerNum      int32
	cbChannel      chan func(error)

	mapTaskQueueSession map[int64]*queue.Deque[task]

	waitWorker   sync.WaitGroup
	waitDispatch sync.WaitGroup

	cancelContext context.Context
	cancel        context.CancelFunc
}

func (d *dispatch) open(minGoroutineNum int32, maxGoroutineNum int32, tasks chan task, cbChannel chan func(error)) {
	d.minConcurrentNum = minGoroutineNum
	d.maxConcurrentNum = maxGoroutineNum
	d.tasks = tasks
	d.mapTaskQueueSession = make(map[int64]*queue.Deque[task], maxTaskQueueSessionId)
	d.workerQueue = make(chan task)
	d.cbChannel = cbChannel
	d.queueIdChannel = make(chan int64, cap(tasks))
	d.cancelContext, d.cancel = context.WithCancel(context.Background())
	d.waitDispatch.Add(1)
	go d.run()
}

func (d *dispatch) run() {
	defer d.waitDispatch.Done()
	timeout := time.NewTimer(time.Duration(atomic.LoadInt64(&idleTimeout)))

	for {
		select {
		case queueId := <-d.queueIdChannel:
			d.processQueueEvent(queueId)
		default:
			select {
			case t, ok := <-d.tasks:
				if ok == false {
					return
				}
				d.processTask(&t)
			case queueId := <-d.queueIdChannel:
				d.processQueueEvent(queueId)
			case <-timeout.C:
				d.processTimer()
			case <-d.cancelContext.Done():
				atomic.StoreInt64(&idleTimeout, int64(time.Millisecond*5))
				timeout.Reset(time.Duration(atomic.LoadInt64(&idleTimeout)))
				for i := int32(0); i < d.workerNum; i++ {
					d.processIdle()
				}
			}
		}

		if atomic.LoadInt32(&d.minConcurrentNum) == -1 && d.workerNum == 0 {
			d.waitWorker.Wait()
			d.cbChannel <- nil
			return
		}
	}
}

func (d *dispatch) processTimer() {
	if d.idle == true && d.workerNum > atomic.LoadInt32(&d.minConcurrentNum) {
		d.processIdle()
	}

	d.idle = true
}

func (d *dispatch) processQueueEvent(queueId int64) {
	d.idle = false

	queueSession := d.mapTaskQueueSession[queueId]
	if queueSession == nil {
		return
	}

	queueSession.PopFront()
	if queueSession.Len() == 0 {
		return
	}

	t := queueSession.Front()
	d.executeTask(&t)
}

func (d *dispatch) executeTask(t *task) {
	select {
	case d.workerQueue <- *t:
		return
	default:
		if d.workerNum < d.maxConcurrentNum {
			var work worker
			work.start(&d.waitWorker, t, d)
			return
		}
	}

	d.workerQueue <- *t
}

func (d *dispatch) processTask(t *task) {
	d.idle = false

	//处理有排队任务
	if t.queueId != 0 {
		queueSession := d.mapTaskQueueSession[t.queueId]
		if queueSession == nil {
			queueSession = &queue.Deque[task]{}
			d.mapTaskQueueSession[t.queueId] = queueSession
		}

		//没有正在执行的任务，则直接执行
		if queueSession.Len() == 0 {
			d.executeTask(t)
		}

		queueSession.PushBack(*t)
		return
	}

	//普通任务
	d.executeTask(t)
}

func (d *dispatch) processIdle() {
	select {
	case d.workerQueue <- task{}:
		d.workerNum--
	default:
	}
}

func (d *dispatch) pushQueueTaskFinishEvent(queueId int64) {
	d.queueIdChannel <- queueId
}

func (d *dispatch) pushAsyncDoCallbackEvent(cb func(err error)) {
	if cb == nil {
		//不需要回调的情况
		return
	}

	d.cbChannel <- cb
}

func (d *dispatch) close() {
	atomic.StoreInt32(&d.minConcurrentNum, -1)
	d.cancel()

breakFor:
	for {
		select {
		case cb := <-d.cbChannel:
			if cb == nil {
				break breakFor
			}
			cb(nil)
		}
	}

	d.waitDispatch.Wait()
}

func (d *dispatch) DoCallback(cb func(err error)) {
	defer func() {
		if r := recover(); r != nil {
			buf := make([]byte, 4096)
			l := runtime.Stack(buf, false)
			errString := fmt.Sprint(r)
			log.Dump(string(buf[:l]), log.String("error", errString))
		}
	}()

	cb(nil)
}
