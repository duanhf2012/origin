package util

import "time"

type Timer struct {
	lasttime     int64
	timeinterval int64

	setupZeroDBase time.Duration //0表示普通模式 1表示切换分钟模式
}

func (slf *Timer) GetTimerinterval() int64 {
	return slf.timeinterval
}

func (slf *Timer) SetupTimer(ms int32) {
	slf.lasttime = time.Now().UnixNano()
	slf.timeinterval = int64(ms) * 1e6
}

func (slf *Timer) SetupTimerDouble() {
	slf.lasttime = time.Now().UnixNano()
	slf.timeinterval *= 2
}

func (slf *Timer) SetTimerHalf() {
	slf.lasttime = time.Now().UnixNano()
	slf.timeinterval /= 2
}

//检查整点分钟数触发
func (slf *Timer) SetupZeroTimer(baseD time.Duration, interval int64) {
	timeNow := time.Now()
	nt := timeNow.Truncate(baseD)
	slf.lasttime = nt.UnixNano()
	slf.timeinterval = baseD.Nanoseconds() * interval
	slf.setupZeroDBase = baseD
}

func (slf *Timer) ResetStartTime() {
	slf.lasttime = 0
}

func (slf *Timer) CheckTimeOut() bool {
	now := time.Now()
	if slf.setupZeroDBase.Nanoseconds() == 0 {
		if now.UnixNano() > slf.lasttime+slf.timeinterval {
			slf.lasttime = now.UnixNano()
			return true
		}
	} else { //整点模式
		if now.UnixNano() > slf.lasttime+slf.timeinterval {
			slf.SetupZeroTimer(slf.setupZeroDBase, slf.timeinterval/slf.setupZeroDBase.Nanoseconds())
			return true
		}
	}

	return false
}
