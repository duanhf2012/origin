package util_test

import (
	"fmt"
	"testing"

	"github.com/duanhf2012/origin/util"
)

func ExampleMap(t *testing.T) {
	maps := util.NewMapEx()
	for i := 0; i < 10000; i++ {
		maps.Set(i, i)
	}

	for i := 0; i < 10000; i++ {
		ret := maps.Get(i)
		if ret.(int) != i {
			fmt.Printf("cannot find i:%d\n", i)
		}
	}

	for i := 0; i < 10000; i++ {
		maps.LockSet(i, func(key interface{}, val interface{}) interface{} {
			return val.(int) + 1
		})
	}

	for i := 0; i < 10000; i++ {
		ret := maps.Get(i)
		if ret.(int) != i {
			fmt.Printf("cannot find i:%d\n", i)
		}
	}
}
