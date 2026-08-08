package node

import (
	"errors"
	"fmt"
	"math"
	"time"

	"github.com/duanhf2012/origin/v3/errs"
	"github.com/duanhf2012/origin/v3/service"
)

// Now 返回当前 Node 的游戏逻辑时间。
//
// 逻辑时间默认等于真实时间，并统一转换到 Node 冻结的 TimerLocation。读取只执行一次
// time.Now 和一次原子偏移读取，不获取修改锁，供业务热路径频繁使用。
func (node *Node) Now() time.Time {
	if node == nil {
		return time.Time{}
	}
	location := node.timerResources.timerLocation
	if location == nil {
		location = time.Local
	}
	return time.Now().Add(time.Duration(node.gameTimeOffset.Load())).In(location)
}

// SetTime 把当前 Node 的游戏逻辑时间设置到 value，并保持之后按真实时间一比一前进。
//
// 该方法只改变 Node 内业务时间，不修改操作系统时钟。Created、Starting 和 Ready 状态允许
// 修改；停止边界之后拒绝新的时间变化，避免业务 Timer 清理过程中重新登记工作。
func (node *Node) SetTime(value time.Time) error {
	if node == nil || value.IsZero() {
		return errs.NewMessage(errs.CodeInvalidArgument, "Node 游戏逻辑时间不能为空")
	}

	// 修改锁把状态检查、偏移提交以及后续 Timer 重排固定为一个冷路径事务。
	node.gameTimeMu.Lock()
	defer node.gameTimeMu.Unlock()
	if err := node.gameTimeMutationError(); err != nil {
		return err
	}

	// time.Time.Sub 在超出 Duration 范围时会饱和；回加并比较真实时刻可以稳定识别该边界，
	// 防止把无法表达的目标静默截断到约 290 年偏移。
	realNow := time.Now()
	offset := value.Sub(realNow)
	if !realNow.Add(offset).Equal(value) {
		return errs.NewMessage(
			errs.CodeInvalidArgument,
			"Node 游戏逻辑时间超出可表达范围",
		)
	}
	node.gameTimeOffset.Store(int64(offset))
	return node.rebaseBusinessTimersLocked()
}

// AddTime 在当前 Node 的游戏逻辑时间偏移上增加 delta。
//
// delta 可以为零或负数。偏移溢出时保持旧值并返回 CodeInvalidArgument，调用方可以安全
// 重试、改用 SetTime 或记录配置错误。
func (node *Node) AddTime(delta time.Duration) error {
	if node == nil {
		return errs.NewMessage(errs.CodeInvalidArgument, "Node 不能为空")
	}

	// Set 与 Add 共用同一修改锁，使多个管理请求拥有清晰的线性化顺序。
	node.gameTimeMu.Lock()
	defer node.gameTimeMu.Unlock()
	if err := node.gameTimeMutationError(); err != nil {
		return err
	}
	if delta == 0 {
		// 零增量是真正的幂等读操作，不改变代次、Deadline 或 Timer 排序。
		return nil
	}

	current := node.gameTimeOffset.Load()
	addition := int64(delta)
	if (addition > 0 && current > math.MaxInt64-addition) ||
		(addition < 0 && current < math.MinInt64-addition) {
		return errs.NewMessage(
			errs.CodeInvalidArgument,
			"Node 游戏逻辑时间偏移溢出",
		)
	}
	node.gameTimeOffset.Store(current + addition)
	return node.rebaseBusinessTimersLocked()
}

// rebaseBusinessTimersLocked 把当前 Node 的时间变化广播给全部 Service Scheduler。
//
// 调用方必须持有 gameTimeMu，使两次 Set/Add 不会交叉重排。Service 使用配置顺序，
// 因此同一次时间跳跃在日志和测试中都有稳定的处理顺序。
func (node *Node) rebaseBusinessTimersLocked() error {
	var result error
	for _, entry := range node.services {
		result = errors.Join(result, service.RebaseTimers(entry.instance))
	}
	return result
}

// gameTimeMutationError 把 Node 生命周期映射为时间修改的稳定错误。
//
// 调用方必须持有 gameTimeMu；Now 不受该状态限制，便于停止日志读取最后逻辑时间。
func (node *Node) gameTimeMutationError() error {
	switch node.State() {
	case StateCreated, StateStarting, StateReady:
		return nil
	case StateStopping:
		return errs.ErrServiceStopping
	case StateStopped:
		return errs.ErrServiceStopped
	case StateFailed:
		return errs.ErrServiceFailed
	default:
		return errs.NewMessage(
			errs.CodeInvalidArgument,
			fmt.Sprintf("Node %q 的游戏逻辑时间状态无效", node.id),
		)
	}
}
