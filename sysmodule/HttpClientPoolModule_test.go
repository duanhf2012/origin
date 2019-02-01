package sysmodule_test

import (
	"fmt"
	"net/http"
	"testing"

	"github.com/duanhf2012/origin/sysmodule"
)

func TestHttpClientPoolModule(t *testing.T) {
	c := sysmodule.HttpClientPoolModule{}
	c.Init(10)

	rsp := c.Request(http.MethodGet, "https://www.baidu.com/", nil)
	fmt.Println(rsp.Err)
	fmt.Println(rsp.Header)
	fmt.Println(rsp.StatusCode)
	fmt.Println(rsp.Status)
	fmt.Println(string(rsp.Body))

	srsp := c.SyncRequest(http.MethodGet, "https://www.baidu.com/", nil)
	rsp1 := srsp.Get(1)
	fmt.Println(rsp1.Err)
	fmt.Println(rsp1.Header)
	fmt.Println(rsp1.StatusCode)
	fmt.Println(rsp1.Status)
	fmt.Println(string(rsp1.Body))
}
