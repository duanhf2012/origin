package service

import (
	"fmt"
	"strings"
	"time"

	"github.com/duanhf2012/origin/v3/errs"
	originlog "github.com/duanhf2012/origin/v3/log"
	"github.com/robfig/cron/v3"
)

// cronSchedule 是 Origin 对第三方 Cron Parser 的最小依赖边界。
//
// 框架只调用 Next 计算未来墙上时间，不创建 cron.Cron、不使用其 goroutine、Timer 或日志器。
type cronSchedule interface {
	Next(time.Time) time.Time
}

// numericCronParser 支持可选秒字段和标准的分、时、日、月、周字段。
//
// robfig Parser 本身还支持英文名称；公开入口会先执行严格字符白名单，因此 Origin 的稳定
// 契约仍然只接受数字、星号、逗号、横线和斜线。
var numericCronParser = cron.NewParser(
	cron.SecondOptional |
		cron.Minute |
		cron.Hour |
		cron.Dom |
		cron.Month |
		cron.Dow,
)

// CronFunc 创建使用当前 Node 冻结时区的周期业务 Timer。
func (service *Service) CronFunc(
	expression string,
	fn TimerFunc,
) (TimerID, error) {
	if service == nil || fn == nil {
		return InvalidTimerID, invalidArgument("Cron Service 和回调不能为空")
	}
	// 参数错误不依赖 Service 当前生命周期；先解析可让同一非法表达式始终返回稳定的
	// CodeInvalidArgument，避免停止阶段改变调用方配置诊断。
	schedule, err := parseCronExpression(expression)
	if err != nil {
		return InvalidTimerID, err
	}
	if err := service.timerCreationError(); err != nil {
		return InvalidTimerID, err
	}
	scheduler := service.scheduler.Load()
	if scheduler == nil {
		return InvalidTimerID, errs.ErrServiceNotReady
	}
	timerID, err := scheduler.createCron(schedule, fn)
	if err != nil {
		return InvalidTimerID, err
	}
	return timerID, nil
}

// parseCronExpression 把第三方解析错误收敛为 Origin CodeInvalidArgument。
func parseCronExpression(expression string) (cronSchedule, error) {
	fields := strings.Fields(expression)
	if len(fields) != 5 && len(fields) != 6 {
		return nil, invalidArgument("Cron 只支持 5 段或 6 段表达式")
	}
	for _, field := range fields {
		for _, character := range field {
			if (character < '0' || character > '9') &&
				!strings.ContainsRune("*,/-", character) {
				return nil, invalidArgument("Cron 只支持数字表达式")
			}
		}
	}

	schedule, err := numericCronParser.Parse(strings.Join(fields, " "))
	if err != nil {
		return nil, errs.NewMessage(
			errs.CodeInvalidArgument,
			fmt.Sprintf("Cron 表达式无效: %v", err),
		)
	}
	return schedule, nil
}

// createCron 计算当前冻结时区中的首个未来名义点，并复用统一 Timer 准入路径。
func (scheduler *serviceScheduler) createCron(
	schedule cronSchedule,
	fn TimerFunc,
) (TimerID, error) {
	scheduler.mu.Lock()

	switch scheduler.state {
	case schedulerPrepared, schedulerRunning:
		// 继续计算未来名义点。
	case schedulerDraining:
		scheduler.mu.Unlock()
		return InvalidTimerID, errs.ErrServiceStopping
	default:
		scheduler.mu.Unlock()
		return InvalidTimerID, errs.ErrServiceStopped
	}

	location := scheduler.runtime.TimerLocation()
	if location == nil {
		location = time.Local
	}
	now := scheduler.businessTimerNow().In(location)
	next := schedule.Next(now)
	if next.IsZero() || !next.After(now) {
		scheduler.timerStats.RejectedTotal++
		scheduler.mu.Unlock()
		return InvalidTimerID, invalidArgument("Cron 没有可用的未来触发时间")
	}
	timerID, quotaRejected := scheduler.createTimerLocked(
		businessTimerCron,
		next,
		0,
		schedule,
		location,
		fn,
	)
	logQuota, suppressed := false, uint64(0)
	if quotaRejected {
		logQuota, suppressed = scheduler.timerQuotaLogDecisionLocked(time.Now())
	}
	scheduler.mu.Unlock()

	if logQuota {
		scheduler.logger.Warn(
			"service timer quota exhausted",
			originlog.Int("timer_limit", scheduler.runtime.TimerLimit()),
			originlog.Uint64("suppressed_timer_rejections", suppressed),
		)
	}
	if timerID == InvalidTimerID {
		return InvalidTimerID, errs.ErrServiceQueueFull
	}
	return timerID, nil
}
