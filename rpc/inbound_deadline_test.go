package rpc

import (
	"sync/atomic"
	"testing"
	"time"

	"github.com/duanhf2012/origin/v3/errs"
	"github.com/duanhf2012/origin/v3/internal/timerwheel"
)

// TestInboundDeadlinesExpireCancelAndCloseOnce 验证 TCP/NATS 共用的入站 Deadline 所有权。
func TestInboundDeadlinesExpireCancelAndCloseOnce(t *testing.T) {
	engine, err := timerwheel.New(timerwheel.DefaultOptions())
	if err != nil {
		t.Fatalf("timerwheel.New() error = %v", err)
	}
	if err := engine.Start(); err != nil {
		t.Fatalf("Engine.Start() error = %v", err)
	}
	defer engine.Close()

	deadlines, err := newInboundDeadlines(engine)
	if err != nil {
		t.Fatalf("newInboundDeadlines() error = %v", err)
	}

	var expiredCalls atomic.Int32
	expiredCause := make(chan error, 1)
	if _, err := deadlines.bind(20*time.Millisecond, func(cause error) {
		expiredCalls.Add(1)
		expiredCause <- cause
	}); err != nil {
		t.Fatalf("bind(expired) error = %v", err)
	}
	select {
	case cause := <-expiredCause:
		if !errs.IsCode(cause, errs.CodeDeadlineExceeded) {
			t.Fatalf("到期 cause = %v", cause)
		}
	case <-time.After(time.Second):
		t.Fatal("入站 Deadline 没有到期")
	}

	// 主动完成必须删除并取消 Deadline，随后 Close 也不能再次调用该绑定。
	var canceledCalls atomic.Int32
	canceledID, err := deadlines.bind(time.Second, func(error) {
		canceledCalls.Add(1)
	})
	if err != nil {
		t.Fatalf("bind(canceled) error = %v", err)
	}
	deadlines.unbind(canceledID)

	// 尚未完成的绑定在最终关闭时以 ServiceStopped 唯一完成。
	var closedCalls atomic.Int32
	closedCause := make(chan error, 1)
	if _, err := deadlines.bind(time.Second, func(cause error) {
		closedCalls.Add(1)
		closedCause <- cause
	}); err != nil {
		t.Fatalf("bind(closed) error = %v", err)
	}
	deadlines.close(errs.ErrServiceStopped)
	deadlines.close(errs.ErrServiceStopped)
	if cause := <-closedCause; !errs.IsCode(cause, errs.CodeServiceStopped) {
		t.Fatalf("关闭 cause = %v", cause)
	}
	if expiredCalls.Load() != 1 ||
		canceledCalls.Load() != 0 ||
		closedCalls.Load() != 1 {
		t.Fatalf(
			"完成次数 expired=%d canceled=%d closed=%d",
			expiredCalls.Load(),
			canceledCalls.Load(),
			closedCalls.Load(),
		)
	}
}
