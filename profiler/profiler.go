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


type Analyzer struct {
	elem *list.Element
	profiler *Profiler
}


type Profiler struct {
	stack *list.List //Element
	stackLocker sync.RWMutex
	mapAnalyzer map[*list.Element]Analyzer
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

func (slf *Profiler) Push(tag string) *Analyzer{
	slf.stackLocker.Lock()
	defer slf.stackLocker.Unlock()

	pElem := slf.stack.PushBack(&Element{tagName:tag,pushTime:time.Now()})

	return &Analyzer{elem:pElem,profiler:slf}
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

func (slf *Analyzer) Pop(){
	slf.profiler.stackLocker.Lock()
	defer slf.profiler.stackLocker.Unlock()

	pElement := slf.elem.Value.(*Element)
	pElem,subTm := slf.profiler.check(pElement)
	slf.profiler.callNum+=1
	slf.profiler.totalCostTime += subTm
	if pElem != nil {
		slf.profiler.pushRecordLog(pElem)
	}
	slf.profiler.stack.Remove(slf.elem)
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
		for pElem!=nil {
			pElement := pElem.Value.(*Element)
			pExceptionElem,_ := prof.check(pElement)
			if pExceptionElem!=nil {
				prof.pushRecordLog(pExceptionElem)
			}
			pElem = pElem.Prev()
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

