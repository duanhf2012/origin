package util

import (
	"fmt"
	"reflect"
	"runtime/debug"
)

func F(callback interface{}, args ...interface{}) {
	defer func() {
		if r := recover(); r != nil {
			var coreInfo string
			coreInfo = string(debug.Stack())
			coreInfo += "\n" + fmt.Sprintf("Core information is %v\n", r)
			if Log != nil {
				Log(5, coreInfo)
			} else {
				fmt.Print(coreInfo)
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
	go F(callback, args...)
}
