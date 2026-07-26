//go:build linux || darwin

package command

import (
	"context"
	"errors"
	"testing"

	"golang.org/x/sys/unix"
)

func TestUnixPlatformControlParentCancellation(t *testing.T) {
	t.Parallel()

	parent, cancelParent := context.WithCancel(context.Background())
	runCtx, closeControl, err := startPlatformControl(parent, "")
	if err != nil {
		t.Fatalf("startPlatformControl() error = %v", err)
	}

	// 父 Context 取消必须传播到同一个 Run Context，清理函数可以安全执行。
	cancelParent()
	<-runCtx.Done()
	if err := closeControl(); err != nil {
		t.Fatalf("closeControl() error = %v", err)
	}
}

func TestUnixMissingProcessClassification(t *testing.T) {
	t.Parallel()

	// 极大的正 PID 在测试环境中不存在，稳定触发 ESRCH 而不会向当前进程发送信号。
	const missingPID = int(^uint32(0) >> 1)
	err := requestPlatformStop(missingPID, "")
	if !platformProcessGone(err) {
		t.Fatalf("requestPlatformStop(missing pid) error = %v, want process gone", err)
	}
	if platformProcessGone(errors.New("ordinary")) {
		t.Fatalf("platformProcessGone(ordinary) = true")
	}
	if !platformProcessGone(unix.ESRCH) {
		t.Fatalf("platformProcessGone(ESRCH) = false")
	}
}
