package service

import (
	"context"
	"errors"
	"testing"
	"time"
	_ "time/tzdata"

	"github.com/duanhf2012/origin/v3/errs"
	"github.com/duanhf2012/origin/v3/internal/timerwheel"
)

func TestCronCalendarSemantics(t *testing.T) {
	tests := []struct {
		name       string
		expression string
		location   *time.Location
		from       time.Time
		want       time.Time
	}{
		{
			name:       "day of month or weekday",
			expression: "0 0 1 * 1",
			location:   time.UTC,
			from:       time.Date(2026, 4, 2, 0, 0, 0, 0, time.UTC),
			want:       time.Date(2026, 4, 6, 0, 0, 0, 0, time.UTC),
		},
		{
			name:       "month end skips april",
			expression: "0 0 31 * *",
			location:   time.UTC,
			from:       time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC),
			want:       time.Date(2026, 5, 31, 0, 0, 0, 0, time.UTC),
		},
		{
			name:       "leap day",
			expression: "0 0 29 2 *",
			location:   time.UTC,
			from:       time.Date(2025, 3, 1, 0, 0, 0, 0, time.UTC),
			want:       time.Date(2028, 2, 29, 0, 0, 0, 0, time.UTC),
		},
	}

	newYork, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Fatalf("LoadLocation(America/New_York) error = %v", err)
	}
	tests = append(tests, struct {
		name       string
		expression string
		location   *time.Location
		from       time.Time
		want       time.Time
	}{
		name:       "dst spring gap",
		expression: "30 2 * * *",
		location:   newYork,
		from:       time.Date(2026, 3, 8, 0, 0, 0, 0, newYork),
		want:       time.Date(2026, 3, 9, 2, 30, 0, 0, newYork),
	})

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			schedule, err := parseCronExpression(test.expression)
			if err != nil {
				t.Fatalf("parseCronExpression() error = %v", err)
			}
			if got := schedule.Next(test.from.In(test.location)); !got.Equal(test.want) {
				t.Fatalf("Next(%v) = %v, want %v", test.from, got, test.want)
			}
		})
	}

	// 秋季回拨时 01:30 出现两次。Parser 必须依次返回两个绝对时间不同、UTC 偏移不同的
	// 本地 01:30，不能漏掉第二次，也不能返回同一个绝对时刻造成死循环。
	foldSchedule, err := parseCronExpression("30 1 * * *")
	if err != nil {
		t.Fatalf("parseCronExpression(fall back) error = %v", err)
	}
	foldFrom := time.Date(2026, 11, 1, 0, 0, 0, 0, newYork)
	firstFold := foldSchedule.Next(foldFrom)
	secondFold := foldSchedule.Next(firstFold)
	for index, point := range []time.Time{firstFold, secondFold} {
		if point.Year() != 2026 ||
			point.Month() != time.November ||
			point.Day() != 1 ||
			point.Hour() != 1 ||
			point.Minute() != 30 {
			t.Fatalf("DST 回拨第 %d 个匹配点 = %v", index+1, point)
		}
	}
	_, firstOffset := firstFold.Zone()
	_, secondOffset := secondFold.Zone()
	if !secondFold.After(firstFold) ||
		secondFold.Sub(firstFold) != time.Hour ||
		firstOffset == secondOffset {
		t.Fatalf(
			"DST 重复时间不一致: first=%v offset=%d second=%v offset=%d",
			firstFold,
			firstOffset,
			secondFold,
			secondOffset,
		)
	}
}

func TestCronAcceptsOnlyNumericFiveOrSixFields(t *testing.T) {
	fixture := newTimerFixture(t, 16)
	accepted := []string{
		"*/5 * * * *",
		"0 */5 * * * *",
	}
	for _, expression := range accepted {
		id, err := fixture.service.CronFunc(expression, noopTimerCallback)
		if err != nil || id == InvalidTimerID {
			t.Fatalf("CronFunc(%q) id = %d, error = %v", expression, id, err)
		}
		if !fixture.service.CancelTimer(&id) {
			t.Fatalf("取消 CronFunc(%q) 失败", expression)
		}
	}

	rejected := []string{
		"",
		"@daily",
		"0 0 * JAN *",
		"CRON_TZ=UTC 0 0 * * *",
		"0 0 0 * * * 2026",
		"0 0 ? * *",
		"0 0 L * *",
	}
	for _, expression := range rejected {
		id, err := fixture.service.CronFunc(expression, noopTimerCallback)
		if id != InvalidTimerID || !errs.IsCode(err, errs.CodeInvalidArgument) {
			t.Fatalf(
				"CronFunc(%q) id = %d, error = %v，期望 CodeInvalidArgument",
				expression,
				id,
				err,
			)
		}
	}
}

// TestGameTimeRebaseCoalescesCron 验证时间向前跨过多个日历点时，Cron 不补执行历史，
// 而是只提交一次当前回调，再从新逻辑时间寻找下一个未来匹配点。
func TestGameTimeRebaseCoalescesCron(t *testing.T) {
	fixture := newTimerFixture(t, 8)
	fired := make(chan struct{}, 2)
	id, err := fixture.service.CronFunc("* * * * *", func(context.Context, TimerID) {
		fired <- struct{}{}
	})
	if err != nil || id == InvalidTimerID {
		t.Fatalf("CronFunc() id = %d, error = %v", id, err)
	}
	if err := fixture.runtime.AddTime(3 * time.Minute); err != nil {
		t.Fatalf("AddTime() error = %v", err)
	}
	if err := RebaseTimers(fixture.service); err != nil {
		t.Fatalf("RebaseTimers() error = %v", err)
	}
	advanceTimerFixture(t, fixture, timerwheel.TickDuration)
	receive(t, fired)
	waitForTimerStats(t, fixture.service, func(stats TimerStats) bool {
		return stats.Running == 0 && stats.Scheduled == 1
	})
	select {
	case <-fired:
		t.Fatal("向前跳时补执行了多个 Cron 历史回调")
	default:
	}
	if !fixture.service.CancelTimer(&id) {
		t.Fatal("CancelTimer() 失败")
	}
}

func TestCronUsesNodeFrozenLocation(t *testing.T) {
	config := DefaultSchedulerConfig()
	location, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		t.Fatalf("LoadLocation() error = %v", err)
	}
	fixture := newTimerFixtureWithConfig(t, config, 8, true)
	fixture.runtime.timerLocation = location

	// 测试时钟是 12:00 UTC，即上海 20:00；表达式应在一分钟后的本地 20:01 触发。
	fired := make(chan struct{}, 1)
	id, err := fixture.service.CronFunc("1 20 * * *", func(
		context.Context,
		TimerID,
	) {
		fired <- struct{}{}
	})
	if err != nil || id == InvalidTimerID {
		t.Fatalf("CronFunc() id = %d, error = %v", id, err)
	}
	advanceTimerFixture(t, fixture, 59*time.Second)
	select {
	case <-fired:
		t.Fatal("Cron 在本地名义时间前触发")
	default:
	}
	advanceTimerFixture(t, fixture, time.Second)
	receive(t, fired)
}

// TestCronUsesNodeGameTime 防止 Cron 仍使用 TimerEngine 的真实墙上时间。Node 逻辑时间处于
// 20:00 时，20:01 的 Cron 应在一分钟真实等待后到期，而不是等待真实时钟走到 20:01。
func TestCronUsesNodeGameTime(t *testing.T) {
	fixture := newTimerFixture(t, 8)
	if err := fixture.runtime.AddTime(8 * time.Hour); err != nil {
		t.Fatalf("AddTime() error = %v", err)
	}
	fired := make(chan struct{}, 1)
	id, err := fixture.service.CronFunc("1 20 * * *", func(
		context.Context,
		TimerID,
	) {
		fired <- struct{}{}
	})
	if err != nil || id == InvalidTimerID {
		t.Fatalf("CronFunc() id = %d, error = %v", id, err)
	}

	advanceTimerFixture(t, fixture, 59*time.Second)
	select {
	case <-fired:
		t.Fatal("Cron 在 Node 逻辑名义时间前触发")
	default:
	}
	advanceTimerFixture(t, fixture, time.Second)
	receive(t, fired)
}

func TestCronResumeSkipsPausedHistory(t *testing.T) {
	fixture := newTimerFixture(t, 8)
	fired := make(chan struct{}, 1)
	id, err := fixture.service.CronFunc("*/5 * * * *", func(
		context.Context,
		TimerID,
	) {
		fired <- struct{}{}
	})
	if err != nil || id == InvalidTimerID {
		t.Fatalf("CronFunc() id = %d, error = %v", id, err)
	}

	advanceTimerFixture(t, fixture, time.Minute)
	if !fixture.service.PauseTimer(id) {
		t.Fatal("PauseTimer() 失败")
	}
	advanceTimerFixture(t, fixture, 10*time.Minute)
	if !fixture.service.ResumeTimer(id) {
		t.Fatal("ResumeTimer() 失败")
	}

	// 当前为 12:11 UTC，恢复后只等待未来的 12:15，不补 12:05 和 12:10。
	advanceTimerFixture(t, fixture, 3*time.Minute+59*time.Second)
	select {
	case <-fired:
		t.Fatal("Cron Resume 补执行了暂停期间历史触发")
	default:
	}
	advanceTimerFixture(t, fixture, time.Second)
	receive(t, fired)
}

func TestCronRearmsWhenWallTimeIsBeforeNominalPoint(t *testing.T) {
	fixture := newPreparedTimerFixture(t, 8)
	id, err := fixture.service.CronFunc(
		"*/5 * * * *",
		noopTimerCallback,
	)
	if err != nil || id == InvalidTimerID {
		t.Fatalf("CronFunc() id = %d, error = %v", id, err)
	}

	scheduler := fixture.service.scheduler.Load()
	scheduler.mu.Lock()
	timer := scheduler.timers[id]
	// 模拟旧内部 Deadline 已到期，但墙上时间向后调整后尚未到 Cron 名义点。
	scheduler.deadlineQueue.Cancel(timer.deadlineID)
	delete(scheduler.deadlineBindings, timer.deadlineID)
	timer.deadlineID = 0
	timer.fireAt = scheduler.timerEngine.Now().Add(time.Minute)
	if scheduler.enqueueExpiredTimerLocked(timer) {
		scheduler.mu.Unlock()
		t.Fatal("墙上时间未到时 Cron 被提升到 Ready")
	}
	rearmed := timer.state == businessTimerScheduled &&
		timer.deadlineID != 0 &&
		scheduler.timerStats.Scheduled == 1
	scheduler.mu.Unlock()
	if !rearmed {
		t.Fatal("墙上时间后退后 Cron 未重新登记 Deadline")
	}
}

func TestCronForwardJumpRunsOnceAndSkipsHistory(t *testing.T) {
	fixture := newTimerFixture(t, 8)
	fired := make(chan struct{}, 2)
	id, err := fixture.service.CronFunc(
		"*/5 * * * *",
		func(context.Context, TimerID) {
			fired <- struct{}{}
		},
	)
	if err != nil || id == InvalidTimerID {
		t.Fatalf("CronFunc() id=%d error=%v", id, err)
	}

	// 从 12:00 一次跳到 12:16，只执行已经到期的当前触发一次，不补 12:10 和 12:15。
	advanceTimerFixture(t, fixture, 16*time.Minute)
	receive(t, fired)
	select {
	case <-fired:
		t.Fatal("墙上时间前跳后补执行了历史 Cron")
	default:
	}
	if coalesced := fixture.service.TimerStats().CoalescedTotal; coalesced != 0 {
		t.Fatalf("Cron 历史跳过错误计入 Ticker 合并统计: %d", coalesced)
	}

	// 回调完成后从当前 12:16 计算下一个未来点 12:20。
	advanceTimerFixture(t, fixture, 3*time.Minute+59*time.Second)
	select {
	case <-fired:
		t.Fatal("Cron 在新的未来名义点前触发")
	default:
	}
	advanceTimerFixture(t, fixture, time.Second)
	receive(t, fired)
	if !fixture.service.CancelTimer(&id) {
		t.Fatal("取消 Cron 失败")
	}
}

func TestCronExpiryQueueClosureReleasesTimer(t *testing.T) {
	fixture := newPreparedTimerFixture(t, 8)
	id, err := fixture.service.CronFunc(
		"*/5 * * * *",
		noopTimerCallback,
	)
	if err != nil || id == InvalidTimerID {
		t.Fatalf("CronFunc() id = %d, error = %v", id, err)
	}

	scheduler := fixture.service.scheduler.Load()
	scheduler.mu.Lock()
	timer := scheduler.timers[id]
	scheduler.deadlineQueue.Cancel(timer.deadlineID)
	delete(scheduler.deadlineBindings, timer.deadlineID)
	timer.deadlineID = 0
	timer.fireAt = scheduler.timerEngine.Now().Add(time.Minute)
	scheduler.deadlineQueue.Close()
	if scheduler.enqueueExpiredTimerLocked(timer) {
		scheduler.mu.Unlock()
		t.Fatal("关闭 Queue 后 Cron 被提升到 Ready")
	}
	active := scheduler.timerStats.Active
	_, exists := scheduler.timers[id]
	scheduler.mu.Unlock()
	if active != 0 || exists || fixture.runtime.active.Load() != 0 {
		t.Fatalf(
			"Queue 关闭后 Cron 未回收: active=%d exists=%v slots=%d",
			active,
			exists,
			fixture.runtime.active.Load(),
		)
	}
}

func TestCronQuotaFailureUsesStableOverloadError(t *testing.T) {
	fixture := newTimerFixture(t, 1)
	if id := fixture.service.AfterFunc(time.Hour, noopTimerCallback); id == InvalidTimerID {
		t.Fatal("占用额度的 AfterFunc 创建失败")
	}
	id, err := fixture.service.CronFunc(
		"*/5 * * * *",
		noopTimerCallback,
	)
	if id != InvalidTimerID || !errors.Is(err, errs.ErrServiceQueueFull) {
		t.Fatalf("Cron 额度耗尽 id=%d error=%v", id, err)
	}
}

func TestCronInvalidExpressionKeepsArgumentErrorWhileStopping(t *testing.T) {
	fixture := newTimerFixture(t, 1)
	if err := BeginStopScheduler(fixture.service); err != nil {
		t.Fatalf("BeginStopScheduler() error = %v", err)
	}
	fixture.runtime.state.Store(uint32(StateStopping))

	id, err := fixture.service.CronFunc("@daily", noopTimerCallback)
	if id != InvalidTimerID || !errs.IsCode(err, errs.CodeInvalidArgument) {
		t.Fatalf("Stopping CronFunc(@daily) id=%d error=%v", id, err)
	}
}

func FuzzParseCronExpressionDoesNotPanic(f *testing.F) {
	for _, expression := range []string{
		"* * * * *",
		"*/1 * * * * *",
		"0 0 0 1 1 *",
		"",
		"@every 1s",
		"999999999999999999999 * * * *",
	} {
		f.Add(expression)
	}
	f.Fuzz(func(t *testing.T, expression string) {
		schedule, err := parseCronExpression(expression)
		if err != nil {
			return
		}
		// 某些数字组合在解析层合法但没有任何未来日历点，公开 createCron 会把零值
		// Next 收敛为参数错误；Fuzz 在这里固定的性质只是任意输入都不得 panic。
		now := time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC)
		_ = schedule.Next(now)
	})
}
