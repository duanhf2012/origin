package util

import (
	"fmt"
	"reflect"
	"sync/atomic"
)

type CoroutineFun func()
type Task struct {
	fun   *reflect.Value
	vargs []reflect.Value
}

var _taskChannel chan Task
var _totalConroutineNum int32 //当前空闲协程数
var _currConroutineNum int32  //当前申请的协程数
var _maxConroutineNum int32   //最大允许协程数

func F(callback interface{}, args ...interface{}) {
	v := reflect.ValueOf(callback)
	if v.Kind() != reflect.Func {
		panic("not a function")
	}
	vargs := make([]reflect.Value, len(args))
	for i, arg := range args {
		vargs[i] = reflect.ValueOf(arg)
	}

	vrets := v.Call(vargs)

	fmt.Print("\tReturn values: ", vrets)
}

func newConroutine() {
	go func() {
		fmt.Print("+")
		atomic.AddInt32(&_currConroutineNum, 1)
		for {
			atomic.AddInt32(&_totalConroutineNum, 1)
			task := <-_taskChannel
			fmt.Print(".")
			atomic.AddInt32(&_totalConroutineNum, -1)
			task.fun.Call(task.vargs)

			if atomic.LoadInt32(&_currConroutineNum) > _maxConroutineNum {
				atomic.AddInt32(&_currConroutineNum, -1)
				break
			}
		}
	}()
}

func InitConroutinePool(initConroutineNum int32, maxConroutineNum int32) {
	_maxConroutineNum = maxConroutineNum
	var i int32
	for ; i < initConroutineNum; i++ {
		newConroutine()
	}

	_taskChannel = make(chan Task, 10240)
}

func Go(callback interface{}, args ...interface{}) {
	v := reflect.ValueOf(callback)
	if v.Kind() != reflect.Func {
		panic("not a function")
	}

	vargs := make([]reflect.Value, len(args))
	for i, arg := range args {
		vargs[i] = reflect.ValueOf(arg)
	}

	//当前协程数不足增加
	if atomic.LoadInt32(&_totalConroutineNum) <= 0 {
		newConroutine()
	}

	_taskChannel <- Task{&v, vargs}
}

func DebugInfo() {
	fmt.Printf("_taskChannel:%d,_totalConroutineNum:%d,_currConroutineNum:%d,_maxConroutineNum:%d\n", len(_taskChannel), _totalConroutineNum, _currConroutineNum, _maxConroutineNum)
}

func GetChanelCount() int {
	return len(_taskChannel)
}
