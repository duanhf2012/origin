package httpclientmodule

import (
	"bytes"
	"crypto/tls"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"time"

	"github.com/duanhf2012/origin/service"
)

type HttpClientModule struct {
	service.Module
	client *http.Client
}

type HttpResponse struct {
	Err        error
	Header     http.Header
	StatusCode int
	Status     string
	Body       []byte
}

type SyncHttpResponse struct {
	resp chan HttpResponse
}

func (slf *SyncHttpResponse) Get(timeoutMs int) HttpResponse {
	timerC := time.NewTicker(time.Millisecond * time.Duration(timeoutMs)).C
	select {
	case <-timerC:
		break
	case rsp := <-slf.resp:
		return rsp
	}
	return HttpResponse{
		Err: fmt.Errorf("Getting the return result timeout [%d]ms", timeoutMs),
	}
}

func (m *HttpClientModule) Init(maxpool int, proxyUrl string) {
	type ProxyFun func(_ *http.Request) (*url.URL, error)
	var proxyFun ProxyFun
	if proxyUrl != "" {
		proxyFun = func(_ *http.Request) (*url.URL, error) {
			return url.Parse(proxyUrl)
		}
	}

	m.client = &http.Client{
		Transport: &http.Transport{
			DialContext: (&net.Dialer{
				Timeout:   5 * time.Second,
				KeepAlive: 30 * time.Second,
			}).DialContext,
			MaxIdleConns:        maxpool,
			MaxIdleConnsPerHost: maxpool,
			IdleConnTimeout:     60 * time.Second,
			Proxy:               proxyFun,
			TLSClientConfig:     &tls.Config{InsecureSkipVerify: true},
		},
		Timeout: 5 * time.Second,
	}
}

func (m *HttpClientModule) SetTimeOut(value time.Duration) {
	m.client.Timeout = value
}

func (m *HttpClientModule) SyncRequest(method string, url string, body []byte, header http.Header) SyncHttpResponse {
	ret := SyncHttpResponse{
		resp: make(chan HttpResponse, 1),
	}
	go func() {
		rsp := m.Request(method, url, body, header)
		ret.resp <- rsp
	}()
	return ret
}

func (m *HttpClientModule) Request(method string, url string, body []byte, header http.Header) HttpResponse {
	if m.client == nil {
		panic("Call the init function first")
	}
	ret := HttpResponse{}
	req, err := http.NewRequest(method, url, bytes.NewReader(body))
	if err != nil {
		ret.Err = err
		return ret
	}
	if header != nil {
		req.Header = header
	}
	rsp, err := m.client.Do(req)
	if err != nil {
		ret.Err = err
		return ret
	}
	defer rsp.Body.Close()

	ret.Body, err = io.ReadAll(rsp.Body)
	if err != nil {
		ret.Err = err
		return ret
	}
	ret.StatusCode = rsp.StatusCode
	ret.Status = rsp.Status
	ret.Header = rsp.Header

	return ret
}
