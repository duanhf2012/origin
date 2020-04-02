package timer

import (
	"fmt"
	"github.com/duanhf2012/origin/log"
	"reflect"
	"runtime"
	"time"
)

// one dispatcher per goroutine (goroutine not safe)
type Dispatcher struct {
	ChanTimer chan *Timer
}

func NewDispatcher(l int) *Dispatcher {
	disp := new(Dispatcher)
	disp.ChanTimer = make(chan *Timer, l)
	return disp
}

// Timer
type Timer struct {
	t  *time.Timer
	cb func()
	cbex func(*Timer)
	name string
}

func (t *Timer) Stop() {
	t.t.Stop()
	t.cb = nil
}

func (t *Timer) GetFunctionName() string {
	return t.name
}

func (t *Timer) Cb() {
	defer func() {
		if r := recover(); r != nil {
			buf := make([]byte, 40960)
			l := runtime.Stack(buf, false)
			err := fmt.Errorf("%v: %s", r, buf[:l])
			log.Error("core dump info:%+v\n",err)
		}
	}()

	if t.cbex!=nil {
		t.cbex(t)
	}else{
		t.cb()
	}

}

func (disp *Dispatcher) AfterFunc(d time.Duration, cb func()) *Timer {
	t := new(Timer)
	t.cb = cb
	t.name = reflect.TypeOf(cb).Name()

	t.t = time.AfterFunc(d, func() {
		disp.ChanTimer <- t
	})

	return t
}

func (disp *Dispatcher) AfterFuncEx(funName string,d time.Duration, cbex func(timer *Timer)) *Timer {
	t := new(Timer)
	t.cbex = cbex
	t.name = funName//reflect.TypeOf(cbex).Name()
	//t.name = runtime.FuncForPC(reflect.ValueOf(cbex).Pointer()).Name()
	t.t = time.AfterFunc(d, func() {
		disp.ChanTimer <- t
	})
	return t
}

// Cron
type Cron struct {
	t *Timer
}

func (c *Cron) Stop() {
	if c.t != nil {
		c.t.Stop()
	}
}

func (disp *Dispatcher) CronFunc(cronExpr *CronExpr, _cb func()) *Cron {
	c := new(Cron)

	now := time.Now()
	nextTime := cronExpr.Next(now)
	if nextTime.IsZero() {
		return c
	}

	// callback
	var cb func()
	cb = func() {
		defer _cb()

		now := time.Now()
		nextTime := cronExpr.Next(now)
		if nextTime.IsZero() {
			return
		}
		c.t = disp.AfterFunc(nextTime.Sub(now), cb)
	}

	c.t = disp.AfterFunc(nextTime.Sub(now), cb)
	return c
}


func (disp *Dispatcher) CronFuncEx(cronExpr *CronExpr, _cb func(*Cron)) *Cron {
	c := new(Cron)

	now := time.Now()
	nextTime := cronExpr.Next(now)
	if nextTime.IsZero() {
		return c
	}

	// callback
	var cb func()
	cb = func() {
		defer _cb(c)

		now := time.Now()
		nextTime := cronExpr.Next(now)
		if nextTime.IsZero() {
			return
		}
		c.t = disp.AfterFunc(nextTime.Sub(now), cb)
	}

	c.t = disp.AfterFunc(nextTime.Sub(now), cb)
	return c
}