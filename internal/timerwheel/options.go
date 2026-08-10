// Package timerwheel 提供 Origin Node 内部统一使用的分层 Deadline 时间轮。
//
// 该包只负责一次性 Deadline 的登记、取消和到期 ID 交付，不执行任何业务回调。
package timerwheel

import "time"

const (
	// TickDuration 是时间轮唯一的基础量化精度。
	TickDuration = 10 * time.Millisecond
	// LevelCount 是覆盖完整 Go time.Duration 正值范围所需的层数。
	LevelCount = 5
	// SlotsPerLevel 固定每层使用八位索引，保持定位和级联逻辑简洁。
	SlotsPerLevel = 256
)

// Clock 提供 Engine 使用的单调时间来源。
//
// 生产环境使用 time.Now；测试可以注入可控时钟，避免依赖真实等待。
type Clock interface {
	Now() time.Time
}

// WakeSource 是 Engine 唯一、可复用的底层唤醒源。
//
// Reset 和 Stop 只由 Engine 工作 goroutine 调用；实现不需要支持这两个方法并发执行。
type WakeSource interface {
	C() <-chan time.Time
	Reset(delay time.Duration)
	Stop()
}

// Options 定义一个 Engine 创建后不再改变的基础依赖。
type Options struct {
	// Clock 必须提供带单调语义或测试可控的当前时间。
	Clock Clock
	// WakeSource 必须是当前 Engine 独占的可复用唤醒源。
	WakeSource WakeSource
	// TrackEntryPool 控制是否记录 timerEntry 新建、复用和回收诊断统计。
	//
	// 该选项默认关闭；关闭时池化本身仍然生效，只是不增加统计路径。
	TrackEntryPool bool
}

// DefaultOptions 创建生产环境使用的默认依赖。
//
// 每次调用都会创建独立 WakeSource，不能在多个 Engine 之间复用返回值。
func DefaultOptions() Options {
	return Options{
		Clock:      systemClock{},
		WakeSource: newTimerWakeSource(),
	}
}

// systemClock 使用 time.Now 保留 Go 单调时钟部分。
type systemClock struct{}

// Now 返回当前带单调部分的时间。
func (systemClock) Now() time.Time {
	return time.Now()
}

// timerWakeSource 在整个 Engine 生命周期内只复用一个 time.Timer。
type timerWakeSource struct {
	timer *time.Timer
}

// newTimerWakeSource 创建并立即停止 Timer，避免 Engine 启动前产生无意义唤醒。
func newTimerWakeSource() *timerWakeSource {
	timer := time.NewTimer(time.Hour)
	timer.Stop()
	return &timerWakeSource{timer: timer}
}

// C 返回唯一 Timer 的到期 Channel。
func (source *timerWakeSource) C() <-chan time.Time {
	return source.timer.C
}

// Reset 停止并重新设置唯一 Timer。
func (source *timerWakeSource) Reset(delay time.Duration) {
	// 已经到期的目标应尽快唤醒，但不能把负值交给下层产生含糊语义。
	if delay < 0 {
		delay = 0
	}
	// Origin 最低 Go 版本为 1.26；Reset 已保证不会接收到重置前的旧值。
	source.timer.Reset(delay)
}

// Stop 停止底层 Timer。
func (source *timerWakeSource) Stop() {
	if source == nil || source.timer == nil {
		return
	}
	source.timer.Stop()
}
