package timer

import (
	"reflect"
	"runtime"
	"sync"
	"time"
)

// Timer
type Timer struct {
	name 		string
	cancelled    bool        //是否关闭
	C            chan *Timer    //定时器管道
	interval  	 time.Duration     // 时间间隔（用于循环定时器）
	fireTime  	 time.Time         // 触发时间
	cb 			 func()
	AdditionData interface{}    //定时器附加数据
}

// Ticker
type Ticker struct {
	Timer
}

// Cron
type Cron struct {
	Timer
}

var timerPool = sync.Pool{New: func() interface{}{
	return &Timer{}
}}

var cronPool = sync.Pool{New: func() interface{}{
	return &Cron{}
}}

var tickerPool = sync.Pool{New: func() interface{}{
	return &Ticker{}
}}

func newTimer(d time.Duration,c chan *Timer,cb func(),name string,additionData interface{}) *Timer{
	if c == nil {
		return nil
	}

	timer := timerPool.Get().(*Timer)
	timer.AdditionData = additionData
	timer.C = c
	timer.fireTime = Now().Add(d)
	timer.cb = cb
	timer.name = name
	timer.interval = d

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
	ChanTimer chan *Timer
}

func (t *Timer) Do(){
	if t.cb != nil {
		t.cb()
	}
}

func (t *Timer) SetupTimer(now time.Time){
	t.fireTime = now.Add(t.interval)
	SetupTimer(t)
}

func (t *Timer) GetInterval() time.Duration{
	return t.interval
}

func (t *Timer) Cancel() {
	t.cancelled = true
}

// 判断定时器是否已经取消
func (t *Timer) IsActive() bool {
	return !t.cancelled
}

func (t *Timer) GetName() string{
	return t.name
}

func NewDispatcher(l int) *Dispatcher {
	disp := new(Dispatcher)
	disp.ChanTimer = make(chan *Timer, l)
	return disp
}

type OnTimerClose func(timer *Timer)
func (disp *Dispatcher) AfterFunc(d time.Duration, cb func(),onTimerClose OnTimerClose,onAddTimer func(timer *Timer)) *Timer {
	funName :=  runtime.FuncForPC(reflect.ValueOf(cb).Pointer()).Name()
	timer := newTimer(d,disp.ChanTimer,cb,funName,nil)
	cbFunc := func() {
		onTimerClose(timer)
		releaseTimer(timer)
		if timer.IsActive() == false {
			return
		}

		cb()
	}

	timer.cb = cbFunc
	t := SetupTimer(timer)
	onAddTimer(t)

	return t
}

func (disp *Dispatcher) CronFunc(cronExpr *CronExpr, cb func(*Cron),onTimerClose OnTimerClose,onAddTimer func(timer *Timer))  *Cron {
	now := Now()
	nextTime := cronExpr.Next(now)
	if nextTime.IsZero() {
		return nil
	}

	funcName :=  runtime.FuncForPC(reflect.ValueOf(cb).Pointer()).Name()
	cron := newCron()
	// callback
	var cbFunc func()
	cbFunc = func() {
		if cron.IsActive() == false{
			onTimerClose(&cron.Timer)
			releaseCron(cron)
			return
		}

		now := Now()
		nextTime := cronExpr.Next(now)
		if nextTime.IsZero() {
			cb(cron)
			return
		}

		cron.interval = nextTime.Sub(now)
		cron.fireTime = now.Add(cron.interval)
		SetupTimer(&cron.Timer)
		cb(cron)
	}
	cron.C = disp.ChanTimer
	cron.cb = cbFunc
	cron.name = funcName
	cron.interval = nextTime.Sub(now)
	cron.fireTime = Now().Add(cron.interval)
	SetupTimer(&cron.Timer)
	onAddTimer(&cron.Timer)
	return cron
}

func (disp *Dispatcher) TickerFunc(d time.Duration, cb func(*Ticker),onTimerClose OnTimerClose,onAddTimer func(timer *Timer))  *Ticker {
	funcName :=  runtime.FuncForPC(reflect.ValueOf(cb).Pointer()).Name()
	ticker := newTicker()
	cbFunc := func() {
		cb(ticker)
		if ticker.Timer.IsActive() == true{
			ticker.fireTime = Now().Add(d)
			SetupTimer(&ticker.Timer)
		}else{
			onTimerClose(&ticker.Timer)
			releaseTicker(ticker)
		}
	}

	ticker.C = disp.ChanTimer
	ticker.fireTime = Now().Add(d)
	ticker.cb = cbFunc
	ticker.name = funcName
	ticker.interval = d

	// callback
	SetupTimer(&ticker.Timer)
	onAddTimer(&ticker.Timer)

	return ticker
}
