package timer

import (
	"fmt"
	"github.com/duanhf2012/origin/log"
	"github.com/duanhf2012/origin/util/sync"
	"reflect"
	"runtime"
	"time"
	"sync/atomic"
)

// ITimer
type ITimer interface {
	GetId() uint64
	Cancel()
	GetName() string
	IsActive() bool
	IsOpen() bool
	Open(bOpen bool)
	AppendChannel(timer ITimer)
	Do()
	GetFireTime() time.Time
	SetupTimer(now time.Time) error
}

type OnCloseTimer func(timer ITimer)
type OnAddTimer func(timer ITimer)

// Timer
type Timer struct {
	Id             uint64
	cancelled      int32          //是否关闭
	C              chan ITimer   //定时器管道
	interval       time.Duration // 时间间隔（用于循环定时器）
	fireTime       time.Time     // 触发时间
	cb             func(uint64, interface{})
	cbEx           func(t *Timer)
	cbCronEx       func(t *Cron)
	cbTickerEx     func(t *Ticker)
	cbOnCloseTimer OnCloseTimer
	cronExpr       *CronExpr
	AdditionData   interface{} //定时器附加数据
	rOpen          bool        //是否重新打开

	ref bool
}

// Ticker
type Ticker struct {
	Timer
}

// Cron
type Cron struct {
	Timer
}

var timerPool = sync.NewPoolEx(make(chan sync.IPoolData, 102400), func() sync.IPoolData {
	return &Timer{}
})

var cronPool = sync.NewPoolEx(make(chan sync.IPoolData, 10240), func() sync.IPoolData {
	return &Cron{}
})

var tickerPool = sync.NewPoolEx(make(chan sync.IPoolData, 102400), func() sync.IPoolData {
	return &Ticker{}
})

func newTimer(d time.Duration, c chan ITimer, cb func(uint64, interface{}), additionData interface{}) *Timer {
	timer := timerPool.Get().(*Timer)
	timer.AdditionData = additionData
	timer.C = c
	timer.fireTime = Now().Add(d)
	timer.cb = cb
	timer.interval = d
	timer.rOpen = false
	return timer
}

func releaseTimer(timer *Timer) {
	timerPool.Put(timer)
}

func newTicker() *Ticker {
	ticker := tickerPool.Get().(*Ticker)
	return ticker
}

func releaseTicker(ticker *Ticker) {
	tickerPool.Put(ticker)
}

func newCron() *Cron {
	cron := cronPool.Get().(*Cron)
	return cron
}

func releaseCron(cron *Cron) {
	cronPool.Put(cron)
}

// one dispatcher per goroutine (goroutine not safe)
type Dispatcher struct {
	ChanTimer chan ITimer
}

func (t *Timer) GetId() uint64 {
	return t.Id
}

func (t *Timer) GetFireTime() time.Time {
	return t.fireTime
}

func (t *Timer) Open(bOpen bool) {
	t.rOpen = bOpen
}

func (t *Timer) AppendChannel(timer ITimer) {
	t.C <- timer
}

func (t *Timer) IsOpen() bool {
	return t.rOpen
}

func (t *Timer) Do() {
	defer func() {
		if r := recover(); r != nil {
			buf := make([]byte, 4096)
			l := runtime.Stack(buf, false)
			errString := fmt.Sprint(r)
			log.SError("core dump info[", errString, "]\n", string(buf[:l]))
		}
	}()

	if t.IsActive() == false {
		if t.cbOnCloseTimer != nil {
			t.cbOnCloseTimer(t)
		}

		releaseTimer(t)
		return
	}

	if t.cb != nil {
		t.cb(t.Id, t.AdditionData)
	} else if t.cbEx != nil {
		t.cbEx(t)
	}

	if t.rOpen == false {
		if t.cbOnCloseTimer != nil {
			t.cbOnCloseTimer(t)
		}
		releaseTimer(t)
	}
}

func (t *Timer) SetupTimer(now time.Time) error {
	t.fireTime = now.Add(t.interval)
	if SetupTimer(t) == nil {
		return fmt.Errorf("failed to install timer")
	}
	return nil
}

func (t *Timer) GetInterval() time.Duration {
	return t.interval
}

func (t *Timer) Cancel() {
	atomic.StoreInt32(&t.cancelled,1)
}

// 判断定时器是否已经取消
func (t *Timer) IsActive() bool {
	return atomic.LoadInt32(&t.cancelled) == 0
}

func (t *Timer) GetName() string {
	if t.cb != nil {
		return runtime.FuncForPC(reflect.ValueOf(t.cb).Pointer()).Name()
	} else if t.cbEx != nil {
		return runtime.FuncForPC(reflect.ValueOf(t.cbEx).Pointer()).Name()
	}

	return ""
}

var emptyTimer Timer

func (t *Timer) Reset() {
	*t = emptyTimer
}

func (t *Timer) IsRef() bool {
	return t.ref
}

func (t *Timer) Ref() {
	t.ref = true
}

func (t *Timer) UnRef() {
	t.ref = false
}

func (c *Cron) Reset() {
	c.Timer.Reset()
}

func (c *Cron) Do() {
	defer func() {
		if r := recover(); r != nil {
			buf := make([]byte, 4096)
			l := runtime.Stack(buf, false)
			errString := fmt.Sprint(r)
			log.SError("core dump info[", errString, "]\n", string(buf[:l]))
		}
	}()

	if c.IsActive() == false {
		if c.cbOnCloseTimer != nil {
			c.cbOnCloseTimer(c)
		}
		releaseCron(c)
		return
	}

	now := Now()
	nextTime := c.cronExpr.Next(now)
	if nextTime.IsZero() {
		c.cbCronEx(c)
		return
	}

	if c.cb != nil {
		c.cb(c.Id, c.AdditionData)
	} else if c.cbCronEx != nil {
		c.cbCronEx(c)
	}

	if c.IsActive() == true {
		c.interval = nextTime.Sub(now)
		c.fireTime = now.Add(c.interval)
		SetupTimer(c)
	} else {
		if c.cbOnCloseTimer != nil {
			c.cbOnCloseTimer(c)
		}
		releaseCron(c)
		return
	}
}

func (c *Cron) IsRef() bool {
	return c.ref
}

func (c *Cron) Ref() {
	c.ref = true
}

func (c *Cron) UnRef() {
	c.ref = false
}

func (c *Ticker) Do() {
	defer func() {
		if r := recover(); r != nil {
			buf := make([]byte, 4096)
			l := runtime.Stack(buf, false)
			errString := fmt.Sprint(r)
			log.SError("core dump info[", errString, "]\n", string(buf[:l]))
		}
	}()

	if c.IsActive() == false {
		if c.cbOnCloseTimer != nil {
			c.cbOnCloseTimer(c)
		}

		releaseTicker(c)
		return
	}

	if c.cb != nil {
		c.cb(c.Id, c.AdditionData)
	} else if c.cbTickerEx != nil {
		c.cbTickerEx(c)
	}

	if c.IsActive() == true {
		c.fireTime = Now().Add(c.interval)
		SetupTimer(c)
	} else {
		if c.cbOnCloseTimer != nil {
			c.cbOnCloseTimer(c)
		}
		releaseTicker(c)
	}
}

func (c *Ticker) Reset() {
	c.Timer.Reset()
}

func (c *Ticker) IsRef() bool {
	return c.ref
}

func (c *Ticker) Ref() {
	c.ref = true
}

func (c *Ticker) UnRef() {
	c.ref = false
}

func NewDispatcher(l int) *Dispatcher {
	dispatcher := new(Dispatcher)
	dispatcher.ChanTimer = make(chan ITimer, l)
	return dispatcher
}

func (dispatcher *Dispatcher) AfterFunc(d time.Duration, cb func(uint64, interface{}), cbEx func(*Timer), onTimerClose OnCloseTimer, onAddTimer OnAddTimer) *Timer {
	timer := newTimer(d, dispatcher.ChanTimer, nil, nil)
	timer.cb = cb
	timer.cbEx = cbEx
	timer.cbOnCloseTimer = onTimerClose

	t := SetupTimer(timer)
	if onAddTimer != nil && t != nil {
		onAddTimer(t)
	}

	return timer
}

func (dispatcher *Dispatcher) CronFunc(cronExpr *CronExpr, cb func(uint64, interface{}), cbEx func(*Cron), onTimerClose OnCloseTimer, onAddTimer OnAddTimer) *Cron {
	now := Now()
	nextTime := cronExpr.Next(now)
	if nextTime.IsZero() {
		return nil
	}

	cron := newCron()
	cron.cb = cb
	cron.cbCronEx = cbEx
	cron.cbOnCloseTimer = onTimerClose
	cron.cronExpr = cronExpr
	cron.C = dispatcher.ChanTimer
	cron.interval = nextTime.Sub(now)
	cron.fireTime = nextTime
	SetupTimer(cron)
	onAddTimer(cron)
	return cron
}

func (dispatcher *Dispatcher) TickerFunc(d time.Duration, cb func(uint64, interface{}), cbEx func(*Ticker), onTimerClose OnCloseTimer, onAddTimer OnAddTimer) *Ticker {
	ticker := newTicker()
	ticker.C = dispatcher.ChanTimer
	ticker.fireTime = Now().Add(d)
	ticker.cb = cb
	ticker.cbTickerEx = cbEx
	ticker.interval = d

	// callback
	SetupTimer(ticker)
	onAddTimer(ticker)

	return ticker
}
