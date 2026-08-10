package service

import (
	"context"
	"time"
)

// TimerID 是业务 Timer 在所属 Node 当前生命周期内唯一且不会复用的标识。
type TimerID uint64

const (
	// InvalidTimerID 表示 Timer 创建失败、已经失效或没有绑定任何业务 Timer。
	InvalidTimerID TimerID = 0
)

// TimerFunc 是业务 Timer 到期后在所属 Service 唯一执行槽中调用的回调。
type TimerFunc func(ctx context.Context, timerID TimerID)

// ITimer 定义业务 Service 可直接使用的定时器能力，并由 IService 直接组合。
type ITimer interface {
	AfterFunc(delay time.Duration, fn TimerFunc) TimerID
	NewTicker(interval time.Duration, fn TimerFunc) TimerID
	CronFunc(expression string, fn TimerFunc) (TimerID, error)

	// PauseTimer 暂停同一 Service 或 Module 作用域创建且尚未开始的 Timer；周期回调运行中时，
	// 在本轮完成后暂停。
	PauseTimer(timerID TimerID) bool
	// ResumeTimer 恢复同一作用域已经暂停的 Timer；Cron 不补执行暂停期间错过的历史点。
	ResumeTimer(timerID TimerID) bool
	// CancelTimer 取消同一作用域的 Timer，并把调用方持有的非零 TimerID 清零。
	CancelTimer(timerID *TimerID) bool

	TimerStats() TimerStats
}

// TimerStats 是一个 Service 全部业务 Timer 在同一时刻的一致统计快照。
type TimerStats struct {
	Active     int
	Scheduled  int
	DuePending int
	Ready      int
	Running    int
	Paused     int

	ActiveHighWatermark int

	CreatedTotal   uint64
	RejectedTotal  uint64
	TriggeredTotal uint64
	CompletedTotal uint64
	CanceledTotal  uint64
	PausedTotal    uint64
	ResumedTotal   uint64
	// CoalescedTotal 只统计 NewTicker 按固定节拍跳过的名义触发次数。Cron 从当前 Node
	// 逻辑时间直接计算下一个未来日历点，不遍历可能很长的历史区间。
	CoalescedTotal          uint64
	PanicTotal              uint64
	PanicLimitCanceledTotal uint64

	LastReadyDelay time.Duration
	MaxReadyDelay  time.Duration
}
