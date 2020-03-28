package serviceTest

import (
	"fmt"
	"github.com/duanhf2012/originnet/service"
	"time"
)

type OriginServerTwo struct {
	service.Service
}

func (slf *OriginServerTwo) RPC_TestCall(a *int,b *int) error {
	fmt.Printf("OriginServerTwo\n")
	*a = *b*2
	//slf.AfterFunc(time.Second,slf.Test)
	return nil
}

func (slf *OriginServerTwo) RPC_TestAsyncCall(a *int, reply *TestAsyn) error {
	fmt.Printf("OriginServerTwo async start sleep %d\n", *a)
	time.Sleep(time.Second)

	reply.a = *a
	reply.b = "fuck!"
	return nil
}
