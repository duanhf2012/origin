package service

import (
	"fmt"
	"reflect"
	"time"

	"github.com/duanhf2012/origin/v3/errs"
	originlog "github.com/duanhf2012/origin/v3/log"
)

// Runtime 是 Node 为单个 Service 实例提供的最小只读运行环境。
//
// Runtime 由框架装配，业务代码不应自行实现或替换。接口只暴露 M7 已经确认的本地查询，
// 后续 RPC、Timer 和配置能力在各自里程碑按真实需求扩展。
type Runtime interface {
	NodeID() string
	ServiceName() string
	State() State
	Logger() originlog.Logger
	LookupService(name string) (IService, bool)
	AcquireTimerSlot() (TimerID, bool)
	ReleaseTimerSlot()
	TimerLimit() int
	TimerLocation() *time.Location
	// Failure 返回所属 Service 第一个不可恢复根因；正常状态返回 nil。
	Failure() error
	// ReportFailure 由 Scheduler 在无法证明状态安全时调用，业务代码不应主动使用。
	ReportFailure(cause error)
}

// RuntimeOf 返回框架绑定给 target 的只读 Runtime。
//
// 该入口只供 rpc 等框架包在生成客户端构造冷路径使用。业务代码应继续调用 Service 上的
// Name、NodeID、Logger 和 LookupService，不应保存或替换 Runtime。
func RuntimeOf(target IService) Runtime {
	if target == nil || isNilService(target) {
		return nil
	}
	base := target.baseService()
	if base == nil {
		return nil
	}
	return base.runtime
}

// BindRuntime 把一个尚未绑定的 Service 实例与唯一 Node 运行环境关联。
//
// 该方法是 node 包装配 Service 时使用的框架边界。重复绑定、nil 参数或无效基础对象都会
// 返回 CodeInvalidArgument，且不会修改已经存在的绑定。
func BindRuntime(target IService, runtime Runtime) error {
	// 先检查接口和接口内有类型 nil，避免调用 baseService 时触发业务侧 panic。
	if target == nil || isNilService(target) {
		return invalidArgument("Service 不能为空")
	}
	base := target.baseService()
	if base == nil {
		return invalidArgument("Service 基础对象不能为空")
	}
	if runtime == nil {
		return invalidArgument("Service Runtime 不能为空")
	}

	// Service 在实例装配阶段只允许绑定一次；互斥锁只走启动冷路径。
	base.bindMu.Lock()
	defer base.bindMu.Unlock()
	if base.runtime != nil {
		return invalidArgument(fmt.Sprintf(
			"Service %q 已经绑定到 Node %q",
			base.runtime.ServiceName(),
			base.runtime.NodeID(),
		))
	}
	base.runtime = runtime
	return nil
}

// isNilService 识别存放在接口中的有类型 nil 指针。
//
// 业务通常把 *PlayerService 传给 Setup；当该指针为 nil 时，接口本身并不等于 nil。
// 这里仅检查可为 nil 的反射种类，不创建新对象，也不进入生命周期热路径。
func isNilService(target IService) bool {
	value := reflect.ValueOf(target)
	switch value.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return value.IsNil()
	default:
		return false
	}
}

// invalidArgument 创建带稳定错误码的 Service 参数错误。
func invalidArgument(message string) error {
	return errs.NewMessage(errs.CodeInvalidArgument, message)
}
