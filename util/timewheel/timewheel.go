package timewheel

import (
	"sync"
	"sync/atomic"
	"time"
)

//分别用位代表每个轮存在的轮子数(2^6,2^6,...2^8)
//--------------------------
//| 6 | 6 | 6 | 6 | 8 |
//--------------------------
//根据游戏定时器，将第一轮控制在12位，即40960ms以内。
var GRANULARITY int64 = 10         //定时器的最小单位，每格10ms
//var wheelBitSize =[]int{8,6,6,6,6} //32bit定时器
var wheelBitSize =[]int{12,7,6,5,2} //32bit定时器

type wheelInfo struct {
	slotNum int       //轮子slot数量
	threshold int64   //轮子最大表示数字范围,如果是第一个轮子2^8,第二个轮子是2^6...
}

var tWheel *timeWheel           //时间实例化对象指针
var chanStartTimer chan *Timer  //开始定时器Channel
var chanStopTimer chan *Timer   //停止定时器Channel
const chanTimerLen int = 40960  //Channel
var timerPool = sync.Pool{New: func() interface{}{
	return &Timer{}
}}


//构造时间轮对象与相关初始化
func init(){
	tWheel = newTimeWheel()
	chanStartTimer = make(chan *Timer,chanTimerLen)
	chanStopTimer = make(chan *Timer,chanTimerLen)

	go timerRunning()
}

//定时器运行与驱动
func timerRunning(){
	t := time.NewTicker(time.Millisecond*10)
	for {
		/*
		if test == true {
			testTimerRunning()
		}*/

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
/*
var test bool = false
func testTimerRunning(){
	for {
		select {
		case startTimer := <-chanStartTimer:
			tWheel.addTimer(startTimer)
		case stopTimer := <-chanStopTimer:
			tWheel.delTimer(stopTimer)
		default:
			tWheel.TickOneFrame()
		}
	}
}
*/



func NewTimerEx(d time.Duration,c chan *Timer,additionData interface{}) *Timer{
	if c == nil {
		c = make(chan *Timer, 1)
	}
	timer := tWheel.newTimer(d.Milliseconds()/GRANULARITY,additionData,c)
	chanStartTimer<-timer
	return timer
}

func NewTimer(d time.Duration) *Timer{
	timer := tWheel.newTimer(d.Milliseconds()/GRANULARITY,nil,make(chan *Timer, 1))
	chanStartTimer<-timer
	return timer
}

//链表结点
type stNode struct {
	prev *Timer
	next *Timer
}

//定时器结构体
type Timer struct {
	stNode
	expireTicks  int64       //到期滴答数
	end        int32       //是否已经关闭0表示开启状态,1表示关闭
	bClose       bool        //是否关闭
	C            chan *Timer //定时器管道
	AdditionData interface{} //定时器附加数据
}

//停止停时器
func (timer *Timer) Close(){
	timer.bClose = true
	if timer.bClose == true {
		return
	}

	//将关闭标志设为1关闭状态
	if atomic.SwapInt32(&timer.end,1) == 0 {
		chanStopTimer<-timer
	}
}

//定时器是否已经停止
func (timer *Timer) IsClose() bool {
	return timer.bClose
}

func (timer *Timer) IsEnd() bool{
	return atomic.LoadInt32(&timer.end) !=0
}

func (timer *Timer) doTimeout(){
	if atomic.SwapInt32(&timer.end,1) != 0 {
		return
	}
	timer.prev = nil
	timer.next = nil
	select {
		case timer.C <- timer:
	}
}

//每个时间轮上的刻度
type slots struct {
	timer *Timer     //定时器链表头
	restTicks int64  //当前刻度走完一圈剩余时间ticks
}

//时间轮子
type stWheel struct {
	slots []*slots   //刻度切片
	slotIndex int    //当前指针所在的位置索引
}

//获取当前轮的总刻度数
func (stw *stWheel) slotSize() int{
	return len(stw.slots)
}

//添加定时器到slots上
func (s *slots) addTimer(timer *Timer){
	timer.next = s.timer
	timer.prev = s.timer.prev
	s.timer.prev.next = timer
	s.timer.prev = timer
}

//当前slots上是否没有定时器
func (s *slots) isEmpty() bool{
	return s.timer == s.timer.next
}

func (s *slots) makeEmpty() {
	s.timer.next = s.timer
	s.timer.prev = s.timer
}


//时间轮结构体
type timeWheel struct {
	wheels []*stWheel         //所有的轮子的切片
	wheelInfos []*wheelInfo   //所有轮子的信息，预计算存储
	wheelSize int             //轮子数

	currentTime int64         //当前已经走进的的自然时间
	currentTicks int64        //当前已经走进的ticks数
}

//构建时间轮对象
func newTimeWheel() *timeWheel{
	tWheel := &timeWheel{}
	tWheel.set(wheelBitSize)
	tWheel.currentTime = GetNow()
	return tWheel
}

//设置n位定时器
func (t *timeWheel) set(wheelBitSize []int){
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
		t.wheels[idx].slots = make([]*slots,t.wheelInfos[idx].slotNum)
		for slotIdx,_ := range t.wheels[idx].slots {
			t.wheels[idx].slots[slotIdx] = t.newSlot(t.wheels[idx].slots[slotIdx])

			//计算当前slot走完剩余的ticks数，以下turns有个特殊处理
			//第一个轮子idx==0时每个slot的刻度代表1,从第二个轮子开始即idx>0根据
			//t.wheelInfos[idx-1].threshold获得每个刻度值
			//turns代表由于前一轮已经走一圈了,那么当前slotIdex应该加1，即：slotIdx+turns
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

//构建一个slot(轮子上的刻度)
func (t *timeWheel) newSlot(slot *slots) *slots{
	//如果是不存在的slot申请内存
	if slot == nil {
		slot = &slots{}
		timer := &Timer{}
		slot.timer = timer
	}

	//构建双向循环链表
	slot.timer.next = slot.timer
	slot.timer.prev = slot.timer

	return slot
}


//获取当前时间戳ms
func GetNow() int64 {
	return time.Now().UnixNano()/int64(time.Millisecond)
}

//创建定时器 ticks表示多少个ticks单位到期, additionData定时器附带数据, c到时通知的channel
func (t *timeWheel) newTimer(ticks int64,additionData interface{},c chan *Timer) *Timer{
	pTimer := timerPool.Get().(*Timer)
	pTimer.end = 0
	pTimer.bClose = false
	pTimer.C = c
	pTimer.AdditionData = additionData
	pTimer.expireTicks = ticks+t.currentTicks
	return pTimer
}

func ReleaseTimer(timer *Timer) {
	timerPool.Put(timer)
}

//添加定时器
func (t *timeWheel) addTimer(timer *Timer) *Timer {
	//1.计算到期时间ticks
	ticks := timer.expireTicks - t.currentTicks
	if ticks<=0 {
		timer.doTimeout()
		return timer
	}
	//2.for遍历通过ticks找到适合的轮子插入,从底轮子往高找
	var slot *slots
	for wheelIndex,info :=  range t.wheelInfos {
		if ticks < info.threshold {
			var restTicks int64
			var slotTicks int64
			var slotIndex int
			//如果不是第0个轮子
			if wheelIndex != 0 {
				//计算前面所有的轮子剩余ticks数总和(即：当前轮子还有多少个ticks会移动到下一个)
				restTicks =  t.getWheelSum(wheelIndex)
				//当前轮子每个刻度的ticks数
				slotTicks = t.wheelInfos[wheelIndex-1].threshold
				//计算当前落到哪个slotIndex中
				slotIndex = (t.wheels[wheelIndex].slotIndex+int((ticks-restTicks)/slotTicks)) % t.wheelInfos[wheelIndex].slotNum
			}else{
				slotIndex = (t.wheels[wheelIndex].slotIndex + int(ticks))%t.wheelInfos[wheelIndex].slotNum
			}
			//取得slot对象指针
			slot = t.wheels[wheelIndex].slots[slotIndex]
			break
		}
	}

	//3.如果都找不到失败
	if slot == nil {
		panic("cannot find slot!")
		//return nil
	}

	//4.添加定时器timer到链表
	slot.addTimer(timer)
	return timer
}

//删除定时器
func (t *timeWheel) delTimer(timer *Timer) {
	if timer.next == nil {
		return
	}
	timer.prev.next = timer.next
	timer.next.prev = timer.prev
	ReleaseTimer(timer)
}

//按照自然时间走动时间差计算loop，并且进行Tick
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

//Tick一帧
func (t *timeWheel) TickOneFrame(){
	//1.往前走一个Tick
	t.currentTicks += 1

	//2.将当前slot全部到时处理
	var nextTimer *Timer
	slot := t.wheels[0].slots[t.wheels[0].slotIndex]
	for currTimer := slot.timer.next;currTimer!=slot.timer; {
		nextTimer = currTimer.next
		//如果当前定时器已经停止,不做任何处理.否则放入到定时器的channel
		if currTimer.IsEnd() == true {
			currTimer = nextTimer
			continue
		}
		currTimer.doTimeout()
		currTimer = nextTimer
	}

	//3.将timer全部清空处理
	t.wheels[0].slots[t.wheels[0].slotIndex].makeEmpty()

	//4.指针转动
	t.wheels[0].slotIndex+=1

	//5.如果当前刻度转完一轮时,从0表示，并处理下一个轮子的计算
	if t.wheels[0].slotIndex >= t.wheels[0].slotSize() {
		t.wheels[0].slotIndex = 0
		t.cascade(1)
	}
}

//获得当前轮子的slots
func (t *timeWheel) getCurrentSlot(wheelIndex int) *slots{
	return t.wheels[wheelIndex].slots[t.wheels[wheelIndex].slotIndex]
}

//获取当前轮wheelIndex转动所需要的ticks数量
func (t *timeWheel) getWheelSum(wheelIndex int) int64{
	var ticks int64
	//遍历前面n个轮子
	for i:=0;i<wheelIndex;i++{
		ticks += t.getCurrentSlot(i).restTicks
	}

	return ticks
}

//转动下一个轮子(即wheelIndex>0的轮子)
func (t *timeWheel) cascade(wheelIndex int) {
	if wheelIndex<1 || wheelIndex>=t.wheelSize {
		return
	}

	//1.取得对应的轮子上的slot
	wheel := t.wheels[wheelIndex]
	slot := wheel.slots[wheel.slotIndex]

	//2.将当前的slot遍历并重新加入
	currentTimer := slot.timer.next
	for ;currentTimer!=slot.timer; {
		//先保存一个定时器指针,预防链表迭代失效问题
		nextTimer:=currentTimer.next
		//如果到时,直接送到channel
		if currentTimer.expireTicks<= t.currentTicks {
			if currentTimer.IsEnd() == false {
				currentTimer.doTimeout()
			}
		}else{//否则重新添加，会加到下一级轮中
			t.addTimer(currentTimer)
		}
		currentTimer = nextTimer
	}
	//3.将当前轮清空
	wheel.slots[wheel.slotIndex].makeEmpty()

	//4.如果当前轮子跳过一轮,需要跳动到下一时间轮
	wheel.slotIndex++
	if wheel.slotIndex>=wheel.slotSize() {
		wheel.slotIndex = 0
		t.cascade(wheelIndex+1)
	}
}
