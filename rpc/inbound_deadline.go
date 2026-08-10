package rpc

import (
	"context"
	"sync"
	"time"

	"github.com/duanhf2012/origin/v3/errs"
	"github.com/duanhf2012/origin/v3/internal/timerwheel"
)

// inboundDeadlines 管理一个 Transport 的全部入站 Request Deadline。
//
// TCP 与 NATS 共享相同的时间轮语义，但该对象仍属于 rpc 包，不把协议专用取消函数
// 下沉到通用容器。每个 DeadlineID 只会被到期、业务完成或最终关闭中的一个路径取得。
type inboundDeadlines struct {
	mu       sync.Mutex
	queue    *timerwheel.DeadlineQueue
	bindings map[timerwheel.DeadlineID]context.CancelCauseFunc
	done     chan struct{}
}

// newInboundDeadlines 创建独立 DeadlineQueue 并启动唯一批量消费 goroutine。
func newInboundDeadlines(
	engine *timerwheel.Engine,
) (*inboundDeadlines, error) {
	if engine == nil {
		return nil, errs.ErrInvalidArgument
	}
	queue, err := engine.NewDeadlineQueue()
	if err != nil {
		return nil, err
	}
	deadlines := &inboundDeadlines{
		queue:    queue,
		bindings: make(map[timerwheel.DeadlineID]context.CancelCauseFunc),
		done:     make(chan struct{}),
	}
	go deadlines.watch(queue)
	return deadlines, nil
}

// bind 登记一次入站 Request 的唯一超时。
func (deadlines *inboundDeadlines) bind(
	delay time.Duration,
	cancel context.CancelCauseFunc,
) (timerwheel.DeadlineID, error) {
	if deadlines == nil || delay <= 0 || cancel == nil {
		return timerwheel.InvalidDeadlineID, errs.ErrInvalidArgument
	}
	deadlines.mu.Lock()
	queue := deadlines.queue
	if queue == nil {
		deadlines.mu.Unlock()
		return timerwheel.InvalidDeadlineID, errs.ErrServiceStopped
	}

	// ScheduleAfter 与绑定发布位于同一锁内。watch 即使先收到信号，也必须在 Drain 后取得
	// 同一把锁，因此不可能先消费 ID、再看到尚未登记的空绑定。
	id, err := queue.ScheduleAfter(delay)
	if err != nil {
		deadlines.mu.Unlock()
		return timerwheel.InvalidDeadlineID, err
	}
	deadlines.bindings[id] = cancel
	deadlines.mu.Unlock()
	return id, nil
}

// unbind 取消仍未到期的 ID，并删除可能已被 watcher 取得前的绑定。
func (deadlines *inboundDeadlines) unbind(id timerwheel.DeadlineID) {
	if deadlines == nil || id == timerwheel.InvalidDeadlineID {
		return
	}
	deadlines.mu.Lock()
	queue := deadlines.queue
	delete(deadlines.bindings, id)
	deadlines.mu.Unlock()
	if queue != nil {
		queue.Cancel(id)
	}
}

// close 关闭时间轮队列，并以指定原因完成所有尚未被其他路径取得的绑定。
func (deadlines *inboundDeadlines) close(cause error) {
	if deadlines == nil {
		return
	}
	if cause == nil {
		cause = errs.ErrServiceStopped
	}
	deadlines.mu.Lock()
	queue := deadlines.queue
	if queue == nil {
		deadlines.mu.Unlock()
		return
	}
	deadlines.queue = nil
	cancels := make([]context.CancelCauseFunc, 0, len(deadlines.bindings))
	for _, cancel := range deadlines.bindings {
		cancels = append(cancels, cancel)
	}
	clear(deadlines.bindings)
	done := deadlines.done
	deadlines.mu.Unlock()

	// Queue.Close 只撤销计时条目，不执行协议回调；取消函数必须在锁外逐一调用。
	queue.Close()
	for _, cancel := range cancels {
		cancel(cause)
	}
	<-done
}

// watch 批量消费到期 ID，并在锁外调用对应取消函数。
func (deadlines *inboundDeadlines) watch(
	queue *timerwheel.DeadlineQueue,
) {
	defer close(deadlines.done)
	expired := make([]timerwheel.DeadlineID, 0, 64)
	for range queue.ExpiredSignal() {
		for {
			var err error
			expired, err = queue.DrainExpired(expired[:0], 256)
			if err != nil {
				return
			}
			if len(expired) == 0 {
				break
			}

			cancels := make([]context.CancelCauseFunc, 0, len(expired))
			deadlines.mu.Lock()
			for _, id := range expired {
				if cancel := deadlines.bindings[id]; cancel != nil {
					delete(deadlines.bindings, id)
					cancels = append(cancels, cancel)
				}
			}
			deadlines.mu.Unlock()
			for _, cancel := range cancels {
				cancel(errs.ErrDeadlineExceeded)
			}
			if len(expired) < 256 {
				break
			}
		}
	}
}
