package timewheel

import (
	"sync/atomic"
	"time"
)

//分别用位代表每个轮存在的轮子数(2^6,2^6,...2^8)
//--------------------------
//| 6 | 6 | 6 | 6 | 8 |
//--------------------------

var GRANULARITY int64 = 10//10ms
var OnTimerChannelSize int = 1000
var wheelBitSize =[]int{8,6,6,6,6} //32bit定时器

type wheelInfo struct {
	slotNum int     //slot数量
	threshold int64   //
	//mask int
	//bits int
}

//type OnTimerCB func()
var tWheel *timeWheel
var chanStartTimer chan *Timer
var chanStopTimer chan *Timer
const chanTimerLen int = 40960

func init(){
	tWheel = newTimeWheel()
	chanStartTimer = make(chan *Timer,chanTimerLen)
	chanStopTimer = make(chan *Timer,chanTimerLen)

	go timerRunning()
}

func timerRunning(){
	t := time.NewTicker(time.Microsecond*5)
	for {
		select{
			case startTimer:=<-chanStartTimer:
				tWheel.addTimer(startTimer)
			case stopTimer:=<-chanStopTimer:
				tWheel.delTimer(stopTimer)
			case <-t.C:
				tWheel.Tick()
		}
	}
}

func NewCBTimer(d time.Duration,f func(),c chan *Timer) *Timer{
	if c == nil {
		c = make(chan *Timer, 1)
	}
	timer := tWheel.newTimer(d.Milliseconds(),f,c)
	chanStartTimer<-timer
	return timer
}

func NewTimer(d time.Duration) *Timer{
	timer := tWheel.newTimer(d.Milliseconds()/GRANULARITY,nil,make(chan *Timer, 1))
	chanStartTimer<-timer
	return timer
}


type stNode struct {
	prev *Timer
	next *Timer
}

type Timer struct {
	stNode
	timerCB func()
	expireTicks int64     //到期滴答数
	isClose int32
	C chan *Timer
}

func (timer *Timer) Stop(){
	atomic.StoreInt32(&timer.isClose,1)
	chanStopTimer<-timer
}

func (timer *Timer) IsStop() bool {
	return atomic.LoadInt32(&timer.isClose) != 0
}

type Slots struct {
	timer *Timer
	restTicks int64
}

type stWheel struct {
	slots []*Slots
	slotIndex int
}

func (stw *stWheel) slotSize() int{
	return len(stw.slots)
}

func (s *Slots) addTimer(timer *Timer){
	timer.next = s.timer
	timer.prev = s.timer.prev
	s.timer.prev.next = timer
	s.timer.prev = timer
}

func (s *Slots) isEmpty() bool{
	return s.timer == s.timer.next
}

type timeWheel struct {
	wheels []*stWheel
	wheelInfos []*wheelInfo
	wheelSize int
	//chanTimer chan *Timer

	currentTime int64 //
	currentTicks int64  //当前检查的帧数
}

func newTimeWheel() *timeWheel{
	tWheel := &timeWheel{}
	tWheel.Set(wheelBitSize)
	//tWheel.chanTimer = make(chan *Timer,OnTimerChannelSize)
	tWheel.currentTime = GetNow()
	return tWheel
}


func (t *timeWheel) Set(wheelBitSize []int){
	t.wheelSize = len(wheelBitSize)
	t.wheelInfos = make([]*wheelInfo,len(wheelBitSize))
	t.wheels = make([]*stWheel,len(wheelBitSize))
	totalBitSize := 0
	for idx,bitSize := range wheelBitSize {
		totalBitSize += bitSize
		//1.轮子信息
		t.wheelInfos[idx] = &wheelInfo{}
		t.wheelInfos[idx].slotNum = 1 << bitSize
		t.wheelInfos[idx].threshold = 1<< totalBitSize

		//2.make轮子里面的slot
		t.wheels[idx] = &stWheel{}
		t.wheels[idx].slots = make([]*Slots,t.wheelInfos[idx].slotNum)
		for slotIdx,_ := range t.wheels[idx].slots {
			t.wheels[idx].slots[slotIdx] = t.newSlot(t.wheels[idx].slots[slotIdx])
			var perSlotTicks int64 = 1
			turns := 0
			if idx>0 {
				perSlotTicks = t.wheelInfos[idx-1].threshold
				turns = 1
			}
			s := ((1 << bitSize) - (slotIdx+turns))*int(perSlotTicks)
			t.wheels[idx].slots[slotIdx].restTicks = int64(s)
		}
	}
}

func (t *timeWheel) newSlot(slots *Slots) *Slots{
	if slots == nil {
		slots = &Slots{}
		timer := &Timer{}
		slots.timer = timer
	}

	slots.timer.next = slots.timer
	slots.timer.prev = slots.timer

	return slots
}

func GetNow() int64 {
	return time.Now().UnixNano()/int64(time.Millisecond)
}


func (t *timeWheel) newTimer(ticks int64,f func(),c chan *Timer) *Timer{
	return &Timer{timerCB: f,expireTicks:ticks+t.currentTicks,C:c}
}


func ReleaseTimer(timer *Timer) {
}
/*
func (t *timeWheel) AddTimer(milSeconds int,onTimer OnTimerCB) *Timer {
	ticks := milSeconds / GRANULARITY
	timer := t.newTimer(milSeconds,ticks,onTimer)
	return t.addTimer(timer)
}
*/

func (t *timeWheel) addTimer(timer *Timer) *Timer {
	ticks := timer.expireTicks - t.currentTicks
	var slot *Slots
	for wheelIndex,info :=  range t.wheelInfos {
		if ticks < info.threshold {
			var subValue int64
			var offSet int64
			index := 0
			if wheelIndex != 0 {
				subValue =  t.getWheelSum(wheelIndex)//t.wheelInfos[wheelIndex-1].threshold*t.wheels[wheelIndex].slotIndex
				offSet = t.wheelInfos[wheelIndex-1].threshold
				index = (t.wheels[wheelIndex].slotIndex+int((ticks-subValue)/offSet)) % t.wheelInfos[wheelIndex].slotNum///(ticks - subValue + offSet)>>bits&info.mask
			}else{
				index = (t.wheels[wheelIndex].slotIndex + int(ticks))%t.wheelInfos[wheelIndex].slotNum
			}

			slot = t.wheels[wheelIndex].slots[index]
			break
		}
	}

	//插入到指定位置
	if slot == nil {
		return nil
	}

	slot.addTimer(timer)
	return timer
}

func (t *timeWheel) delTimer(timer *Timer) {
	timer.prev.next = timer.next
	timer.next.prev = timer.prev
	//ReleaseTimer(timer)
}


func (t *timeWheel) Tick(){
	nowTime := GetNow()

	loop := (nowTime - t.currentTime)/int64(GRANULARITY)
	if loop> 0 {
		t.currentTime = nowTime
	}

	for i:=int64(0);i<loop;i++{
		t.TickOneFrame()
	}
}

func (t *timeWheel) TickOneFrame(){
	//1.往前走一个Tick
	t.currentTicks += 1

	//2.将当前slot全部超时处理
	slot := t.wheels[0].slots[t.wheels[0].slotIndex]
	bEmpty := true
	for currTimer := slot.timer.next;currTimer!=slot.timer;currTimer = currTimer.next {
		bEmpty = false
		if currTimer.IsStop() == true {
			continue
		}
		select {
		case currTimer.C<-currTimer:
		}
	}

	//重新构建
	if bEmpty == false {
		t.wheels[0].slots[t.wheels[0].slotIndex] = t.newSlot(slot)
	}

	//3.指针转动
	t.wheels[0].slotIndex+=1
	if t.wheels[0].slotIndex >= t.wheels[0].slotSize() {
		t.wheels[0].slotIndex = 0
		t.cascade(1)
	}
}

func (t *timeWheel) getCurrentSlot(wheelIndex int) *Slots{
	return t.wheels[wheelIndex].slots[t.wheels[wheelIndex].slotIndex]
}

func (t *timeWheel) getWheelSum(wheelIndex int) int64{
	var sum int64
	for i:=0;i<wheelIndex;i++{
		sum += t.getCurrentSlot(i).restTicks
	}
/*
	if sum != t.getWheelSumEx(wheelIndex) {
		sum = 0
	}
*/
	return sum
}
/*
func (t *timeWheel) getWheelSumEx(wheelIndex int) int{
	sum := 0
	for i:=0;i<wheelIndex;i++{
		if i == 0 {
			sum += t.wheels[i].slotSize() - t.wheels[i].slotIndex
		}else{
			sum += (t.wheels[i].slotSize() - (t.wheels[i].slotIndex+1))*t.wheelInfos[i-1].threshold
		}
	}

	return sum
}
*/
func (t *timeWheel) cascade(wheelIndex int) {
	if wheelIndex<1 || wheelIndex>=t.wheelSize {
		return
	}

	//1.取得对应的轮子上的slot
	wheel := t.wheels[wheelIndex]
	slot := wheel.slots[wheel.slotIndex]

	//2.将当前的slot遍历并重新加入
	currentTimer := slot.timer.next
	bEmpty := true
	for ;currentTimer!=slot.timer; {
		nextTimer:=currentTimer.next
		//如果到时
		if currentTimer.expireTicks<= t.currentTicks {
			if currentTimer.IsStop() == false {
				select {
				case currentTimer.C<-currentTimer:
				}
			}
		}else{//否则重新添加，会加到下一级轮中
			t.addTimer(currentTimer)
		}
		currentTimer = nextTimer
		bEmpty = false
	}

	if bEmpty == false {
		wheel.slots[wheel.slotIndex] = t.newSlot(wheel.slots[wheel.slotIndex])
	}


	//如果轮子到了最大值，需要跳轮
	wheel.slotIndex++
	if wheel.slotIndex>=wheel.slotSize() {
		wheel.slotIndex = 0
		t.cascade(wheelIndex+1)
	}
}
