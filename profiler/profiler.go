package profiler

import (
	"container/list"
	"fmt"
	"github.com/duanhf2012/origin/log"
	"sync"
	"time"
)

//最大超长时间，一般可以认为是死锁或者死循环，或者极差的性能问题
var Default_MaxOverTime time.Duration = 5*time.Second
//超过该时间将会监控报告
var Default_OverTime time.Duration = 10*time.Millisecond
var Default_MaxRecordNum int = 100 //最大记录条数

type Element struct {
	tagName string
	pushTime time.Time
}

type RecordType int
const  (
	MaxOverTime_Type = 1
	OverTime_Type =2
	)


type Record struct {
	RType RecordType
	CostTime time.Duration
	RecordName string
}

type Profiler struct {
	stack *list.List //Element
	stackLocker sync.RWMutex

	record *list.List   //Record

	callNum int //调用次数
	totalCostTime time.Duration //总消费时间长

	maxOverTime time.Duration
	overTime time.Duration
	maxRecordNum int
}

var mapProfiler map[string]*Profiler

func init(){
	mapProfiler = map[string]*Profiler{}
}

func RegProfiler(profilerName string) *Profiler {
	if _,ok :=mapProfiler[profilerName];ok==true {
		return nil
	}

	pProfiler :=  &Profiler{stack:list.New(),record:list.New(),maxOverTime:Default_MaxOverTime,overTime:Default_OverTime}
	mapProfiler[profilerName] =pProfiler
	return pProfiler
}

func (slf *Profiler) Push(tag string) {
	slf.stackLocker.Lock()
	slf.stack.PushBack(&Element{tagName:tag,pushTime:time.Now()})
	slf.stackLocker.Unlock()
}

func (slf *Profiler) pushRecordLog(record *Record){
	if slf.record.Len()>=Default_MaxRecordNum{
		front := slf.stack.Front()
		if front!=nil {
			slf.stack.Remove(front)
		}
	}

	slf.record.PushBack(record)
}

func (slf *Profiler) check(pElem *Element) (*Record,time.Duration) {
	if pElem == nil {
		return nil,0
	}

	subTm := time.Now().Sub(pElem.pushTime)
	if subTm < slf.overTime {
		return nil,subTm
	}

	record := Record{
		RType:      OverTime_Type,
		CostTime:   subTm,
		RecordName: pElem.tagName,
	}

	if subTm>slf.maxOverTime {
		record.RType = MaxOverTime_Type
	}

	return &record,subTm
}

func (slf *Profiler) Pop() {
	slf.stackLocker.Lock()

	back := slf.stack.Back()
	if back!=nil && back.Value!=nil {
		pElement := back.Value.(*Element)
		pElem,subTm := slf.check(pElement)
		slf.callNum+=1
		slf.totalCostTime += subTm
		if pElem != nil {
			slf.pushRecordLog(pElem)
		}
		slf.stack.Remove(back)
	}

	slf.stackLocker.Unlock()
}

type ReportFunType func(name string,callNum int,costTime time.Duration,record *list.List)

var reportFunc ReportFunType =DefaultReportFunction

func SetReportFunction(reportFun ReportFunType) {
	reportFunc = reportFun
}

func DefaultReportFunction(name string,callNum int,costTime time.Duration,record *list.List){
	if record.Len()<=0 {
		return
	}

	var strReport string
	strReport = "Profiler report tag "+name+":\n"
	var average int64
	if callNum>0 {
		average = costTime.Milliseconds()/int64(callNum)
	}

	strReport += fmt.Sprintf("process count %d,take time %d Milliseconds,average %d Milliseconds/per.\n",callNum,costTime.Milliseconds(),average)
	elem := record.Front()
	var strTypes string
	for elem!=nil {
		pRecord := elem.Value.(*Record)
		if  pRecord.RType == MaxOverTime_Type {
			strTypes = "very slow process"
		}else{
			strTypes = "slow process"
		}

		strReport += fmt.Sprintf("%s:%s is take %d Milliseconds\n",strTypes,pRecord.RecordName,pRecord.CostTime.Milliseconds())
		elem = elem.Next()
	}

	log.Release(strReport)
}

func Report() {
	var record *list.List
	for name,prof := range mapProfiler{
		prof.stackLocker.RLock()

		//取栈顶，是否存在异常MaxOverTime数据
		pElem := prof.stack.Back()
		if pElem!=nil && pElem.Value!=nil{
			pRecord,_ := prof.check(pElem.Value.(*Element))
			if pRecord!=nil {
				prof.pushRecordLog(pRecord)
			}
		}

		if prof.record.Len() == 0 {
			prof.stackLocker.RUnlock()
			continue
		}

		record = prof.record
		prof.record = list.New()
		prof.stackLocker.RUnlock()

		DefaultReportFunction(name,prof.callNum,prof.totalCostTime,record)
	}
}

