package sysmodule

import (
	"bytes"
	"fmt"
	"io/ioutil"
	"net"
	"net/http"
	"time"

	"github.com/duanhf2012/origin/service"
)

type HttpClientPoolModule struct {
	service.BaseModule
	client *http.Client
}

type HttpRespone struct {
	Err        error
	Header     http.Header
	StatusCode int
	Status     string
	Body       []byte
}

type SyncHttpRespone struct {
	resp chan *HttpRespone
}

func (slf *SyncHttpRespone) Get(timeoutMs int) *HttpRespone {
	timerC := time.NewTicker(time.Millisecond * time.Duration(timeoutMs)).C
	select {
	case <-timerC:
		break
	case rsp := <-slf.resp:
		return rsp
	}
	return &HttpRespone{
		Err: fmt.Errorf("Getting the return result timeout [%d]ms", timeoutMs),
	}
}

func (slf *HttpClientPoolModule) Init(maxpool int) {
	slf.client = &http.Client{
		Transport: &http.Transport{
			DialContext: (&net.Dialer{
				Timeout:   5 * time.Second,
				KeepAlive: 30 * time.Second,
			}).DialContext,
			MaxIdleConns:        maxpool,
			MaxIdleConnsPerHost: maxpool,
			IdleConnTimeout:     60 * time.Second,
		},
	}
}

func (slf *HttpClientPoolModule) SyncRequest(method string, url string, body []byte) SyncHttpRespone {
	ret := SyncHttpRespone{
		resp: make(chan *HttpRespone, 1),
	}
	go func() {
		rsp := slf.Request(method, url, body)
		ret.resp <- rsp
	}()
	return ret
}

func (slf *HttpClientPoolModule) Request(method string, url string, body []byte) *HttpRespone {
	if slf.client == nil {
		panic("Call the init function first")
	}
	ret := &HttpRespone{}
	req, err := http.NewRequest(method, url, bytes.NewReader(body))
	if err != nil {
		ret.Err = err
		return ret
	}
	rsp, err := slf.client.Do(req)
	if err != nil {
		ret.Err = err
		return ret
	}
	defer rsp.Body.Close()

	ret.Body, err = ioutil.ReadAll(rsp.Body)
	if err != nil {
		ret.Err = err
		return ret
	}
	ret.StatusCode = rsp.StatusCode
	ret.Status = rsp.Status
	ret.Header = rsp.Header

	return ret
}
