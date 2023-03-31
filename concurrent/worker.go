package concurrent

import (
	"sync"

	"errors"
	"fmt"
	"runtime"

	"github.com/duanhf2012/origin/log"
)

type task struct {
	queueId int64
	fn      func() bool
	cb      func(err error)
}

type worker struct {
	*dispatch
}

func (t *task) isExistTask() bool {
	return t.fn == nil
}

func (w *worker) start(waitGroup *sync.WaitGroup, t *task, d *dispatch) {
	w.dispatch = d
	d.workerNum += 1
	waitGroup.Add(1)
	go w.run(waitGroup, *t)
}

func (w *worker) run(waitGroup *sync.WaitGroup, t task) {
	defer waitGroup.Done()

	w.exec(&t)
	for {
		select {
		case tw := <-w.workerQueue:
			if tw.isExistTask() {
				//exit goroutine
				log.SRelease("worker goroutine exit")
				return
			}
			w.exec(&tw)
		}
	}
}

func (w *worker) exec(t *task) {
	defer func() {
		if r := recover(); r != nil {
			buf := make([]byte, 4096)
			l := runtime.Stack(buf, false)
			errString := fmt.Sprint(r)

			cb := t.cb
			t.cb = func(err error) {
				cb(errors.New(errString))
			}

			w.endCallFun(true,t)
			log.SError("core dump info[", errString, "]\n", string(buf[:l]))
		}
	}()

	w.endCallFun(t.fn(),t)
}

func (w *worker) endCallFun(isDocallBack bool,t *task) {
	if isDocallBack {
		w.pushAsyncDoCallbackEvent(t.cb)
	}

	if t.queueId != 0 {
		w.pushQueueTaskFinishEvent(t.queueId)
	}
}
