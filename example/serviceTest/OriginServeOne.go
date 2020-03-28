package serviceTest

import (
	"fmt"
	"github.com/duanhf2012/originnet/service"
	"time"
)

type TestAsyn struct {
	a int
	b string
}

type OriginServerOne struct {
	service.Service
}

func (slf *OriginServerOne) OnInit() error {
	//slf.AfterFunc(time.Second,slf.testCall)
	//slf.AfterFunc(time.Second*5,slf.testCall)
	//slf.AfterFunc(time.Second*10, slf.testGRCall)
	//slf.AfterFunc(time.Second*15, slf.testGRCall)
	//slf.AfterFunc(time.Second, slf.testAsyncCall)
	slf.AfterFunc(time.Second, slf.testAsyncGRCall)
	return nil
}

func (slf *OriginServerOne) testCall() {
	a := 1
	b := 10
	slf.Call("OriginServerTwo.RPC_TestCall", &a, &b)
	fmt.Println(b)
}

func (slf *OriginServerOne) testGRCall()  {
	a := 1
	b := 10
	slf.GRCall("OriginServerTwo.RPC_TestCall", &a, &b)
	fmt.Println(b)
}

func (slf *OriginServerOne) testAsyncCall() {
	for i := 0; i < 100; i++ {
		in := i
		bT := time.Now()
		slf.AsyncCall("OriginServerTwo.RPC_TestAsyncCall", &in, func(reply *TestAsyn, err error) {
			eT := time.Since(bT)      // 从开始到当前所消耗的时间
			fmt.Println(reply, eT)
		})
		fmt.Println(in)
	}
}

func (slf *OriginServerOne) testAsyncGRCall() {
	for i := 0; i < 100; i++ {
		in := i
		bT := time.Now()
		slf.GRAsyncCall("OriginServerTwo.RPC_TestAsyncCall", &in, func(reply *TestAsyn, err error) {
			eT := time.Since(bT)
			fmt.Println(reply, eT)
		})
		fmt.Println(in)
	}
}