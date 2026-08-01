package service

import (
	"fmt"
	"runtime/debug"

	"github.com/duanhf2012/origin/v3/errs"
	originlog "github.com/duanhf2012/origin/v3/log"
)

// GoSafe 启动一个由当前 Service 负责管理、具有 panic 保底的业务 goroutine。
//
// Origin 只隔离最外层 panic，不跟踪、取消、等待或重启该 goroutine。业务仍应自己持有
// Context、CancelFunc 和 WaitGroup，并在 OnStop 中完成资源回收。
func (service *Service) GoSafe(fn func()) error {
	if service == nil || fn == nil {
		return errs.ErrInvalidArgument
	}
	switch service.State() {
	case StateStarting, StateRunning, StateRetired:
		// 继续创建业务 goroutine。
	case StateStopping:
		return errs.ErrServiceStopping
	case StateStopped:
		return errs.ErrServiceStopped
	case StateFailed:
		return errs.ErrServiceFailed
	default:
		return errs.ErrServiceNotReady
	}

	// panic 只能在发生它的 goroutine 中 recover。这里把边界放在业务 fn 最外层，保证
	// 一次 panic 不会越过 goroutine 边界终止整个进程。
	go func() {
		_ = service.runSafe(fn, "service GoSafe goroutine panic")
	}()
	return nil
}

// RunSafe 在调用方当前 goroutine 同步执行一次 fn，并隔离该 Job 的 panic。
//
// RunSafe 不授予 Service Scheduler 执行权。业务 Worker 仍不能借此并发访问只允许 Service
// 单执行槽读写的状态；它只适合包裹 AOI、寻路等已经与 Service 状态隔离的单个 Job。
func (service *Service) RunSafe(fn func()) error {
	if service == nil || fn == nil {
		return errs.ErrInvalidArgument
	}
	return service.runSafe(fn, "service RunSafe job panic")
}

// runSafe 保存真正 panic 位置的堆栈并通过可靠 ErrorStack 路径输出一次。
func (service *Service) runSafe(fn func(), message string) (result error) {
	defer func() {
		value := recover()
		if value == nil {
			return
		}
		stack := debug.Stack()
		service.Logger().ErrorStack(
			message,
			originlog.String("panic", fmt.Sprint(value)),
			originlog.String("panic_stack", string(stack)),
		)
		result = errs.NewMessage(errs.CodeInternal, "service safe boundary recovered panic")
	}()
	fn()
	return nil
}
