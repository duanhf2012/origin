package coroutine

import (
	"fmt"
	"reflect"
	"runtime/debug"
)

func F(callback interface{},recoverNum int, args ...interface{}) {
	defer func() {
		if r := recover(); r != nil {
			var coreInfo string
			coreInfo = string(debug.Stack())
			coreInfo += "\n" + fmt.Sprintf("Core information is %v\n", r)
			fmt.Print(coreInfo)

			if recoverNum==-1 ||recoverNum-1 >= 0 {
				recoverNum -= 1
				go F(callback,recoverNum, args...)
			}
		}
	}()

	v := reflect.ValueOf(callback)
	if v.Kind() != reflect.Func {
		panic("not a function")
	}
	vargs := make([]reflect.Value, len(args))
	for i, arg := range args {
		vargs[i] = reflect.ValueOf(arg)
	}

	v.Call(vargs)
}

func Go(callback interface{}, args ...interface{}) {
	go F(callback,0, args...)
}

//-1表示一直恢复
func GoRecover(callback interface{},recoverNum int, args ...interface{}) {
	go F(callback,recoverNum, args...)
}