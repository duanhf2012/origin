package timer

import (
	"github.com/duanhf2012/origin/util/timewheel"
	"reflect"
	"runtime"
	"sync"
	"time"
)

var timerPool = sync.Pool{New: func() interface{}{
	return &Timer{}
}}

var cronPool = sync.Pool{New: func() interface{}{
	return &Cron{}
}}

var tickerPool = sync.Pool{New: func() interface{}{
	return &Ticker{}
}}

// one dispatcher per goroutine (goroutine not safe)
type Dispatcher struct {
	ChanTimer chan *timewheel.Timer
}

func NewDispatcher(l int) *Dispatcher {
	disp := new(Dispatcher)
	disp.ChanTimer = make(chan *timewheel.Timer, l)
	return disp
}

type ITime interface {
	Close ()
	Do()
	GetName() string
}

// Timer
type Timer struct {
	t  *timewheel.Timer
	cb func()
	name string
	onClose func(timer  *timewheel.Timer)
}

// Cron
type Cron struct {
	Timer
}

// Ticker
type Ticker struct {
	Timer
}

func NewTimer(t *timewheel.Timer,cb func(),name string,onClose func(timer  *timewheel.Timer)) *Timer {
	timer := timerPool.Get().(*Timer)
	timer.t = t
	timer.cb = cb
	timer.onClose = onClose
	timer.name = name

	return timer
}

func ReleaseTimer(timer *Timer) {
	timerPool.Put(timer)
}

func (t *Timer) Close(){
	if t.t!=nil {
		t.t.Close()
	}
	if t.onClose!=nil {
		t.onClose(t.t)
	}
	ReleaseTimer(t)
}

func (t *Timer) Do(){
	t.Close()
	t.cb()
}

func (t *Timer) GetName() string{
	return t.name
}

func NewCron(t *timewheel.Timer,cb func(),name string,onClose func(timer  *timewheel.Timer)) *Cron {
	cron := cronPool.Get().(*Cron)
	cron.t = t
	cron.cb = cb
	cron.onClose = onClose
	cron.name = name
	return cron
}

func ReleaseCron(cron *Cron) {
	cronPool.Put(cron)
}

func (c *Cron) Do(){
	if c.onClose!=nil {
		c.onClose(c.t)
	}

	c.cb()
}

func (c *Cron) Close() {
	if c.t != nil {
		c.t.Close()
	}

	if c.onClose!=nil {
		c.onClose(c.t)
	}

	ReleaseCron(c)
}

func NewTicker(t *timewheel.Timer,cb func(),name string,onClose func(timer  *timewheel.Timer)) *Ticker {
	ticker := tickerPool.Get().(*Ticker)
	ticker.t = t
	ticker.cb = cb
	ticker.onClose = onClose
	ticker.name = name

	return ticker
}

func ReleaseTicker(ticker *Ticker) {
	tickerPool.Put(ticker)
}

func (tk *Ticker) Do(){
	//通知当前timer删除
	if tk.onClose!=nil {
		tk.onClose(tk.t)
	}
	tk.cb()
}

func (tk *Ticker) Close() {
	if tk.t != nil {
		tk.t.Close()
	}

	if tk.onClose!=nil {
		tk.onClose(tk.t)
	}

	ReleaseTicker(tk)
}

func (disp *Dispatcher) AfterFunc(d time.Duration, cb func(),onCloseTimer func(timer *timewheel.Timer),onAddTimer func(timer *timewheel.Timer)) *Timer {
	funName :=  runtime.FuncForPC(reflect.ValueOf(cb).Pointer()).Name()
	t := NewTimer(nil,cb,funName,onCloseTimer)
	t.t = timewheel.NewTimerEx(d,disp.ChanTimer,t)
	onAddTimer(t.t)

	return t
}

func (disp *Dispatcher) CronFunc(cronExpr *CronExpr, cb func(),onCloseTimer func(timer *timewheel.Timer),onAddTimer func(timer *timewheel.Timer))  *Cron {
	now := time.Now()
	nextTime := cronExpr.Next(now)
	if nextTime.IsZero() {
		return nil
	}

	funcName :=  runtime.FuncForPC(reflect.ValueOf(cb).Pointer()).Name()
	cron := NewCron(nil,nil,funcName,onCloseTimer)
	// callback
	var cbFunc func()
	cbFunc = func() {
		now := time.Now()
		nextTime := cronExpr.Next(now)
		if nextTime.IsZero() {
			cb()
			return
		}

		interval := nextTime.Sub(now)
		minTimeInterval := time.Millisecond*time.Duration(timewheel.GRANULARITY)
		if interval < minTimeInterval {
			interval =  minTimeInterval
		}

		cron.t = timewheel.NewTimerEx(interval,disp.ChanTimer,cron)
		onAddTimer(cron.t)
		cb()
	}
	cron.cb = cbFunc
	cron.t = timewheel.NewTimerEx(nextTime.Sub(now),disp.ChanTimer,cron)
	onAddTimer(cron.t)
	return cron
}

func (disp *Dispatcher) TickerFunc(d time.Duration, cb func(),onCloseTimer func(timer *timewheel.Timer),onAddTimer func(timer *timewheel.Timer))  *Ticker {
	funcName :=  runtime.FuncForPC(reflect.ValueOf(cb).Pointer()).Name()
	ticker := NewTicker(nil,nil,funcName,onCloseTimer)
	// callback
	var cbFunc func()
	cbFunc = func() {
		ticker.t = timewheel.NewTimerEx(d,disp.ChanTimer,ticker)
		onAddTimer(ticker.t)
		cb()
	}

	ticker.cb = cbFunc
	ticker.t = timewheel.NewTimerEx(d,disp.ChanTimer,ticker)
	onAddTimer(ticker.t)
	return ticker
}
