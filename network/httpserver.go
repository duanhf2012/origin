package network

import (
	"fmt"
	"net/http"
	"os"
	"time"
)

type HttpServer struct {
	port uint16

	handler      http.Handler
	readtimeout  time.Duration
	writetimeout time.Duration

	httpserver *http.Server
	certfile   string
	keyfile    string

	ishttps bool
}

func (slf *HttpServer) Init(port uint16, handler http.Handler, readtimeout time.Duration, writetimeout time.Duration) {
	slf.port = port
	slf.handler = handler
	slf.readtimeout = readtimeout
	slf.writetimeout = writetimeout
}

func (slf *HttpServer) HandleFunc(pattern string, handler func(http.ResponseWriter, *http.Request)) {
	http.HandleFunc(pattern, handler)
}

func (slf *HttpServer) Start() {
	go slf.startListen()
}

func (slf *HttpServer) startListen() error {
	listenPort := fmt.Sprintf(":%d", slf.port)
	slf.httpserver = &http.Server{
		Addr:           listenPort,
		Handler:        slf.handler,
		ReadTimeout:    10 * time.Second,
		WriteTimeout:   10 * time.Second,
		MaxHeaderBytes: 1 << 20,
	}

	var err error
	if slf.ishttps == true {
		err = slf.httpserver.ListenAndServeTLS(slf.certfile, slf.keyfile)
	} else {
		err = slf.httpserver.ListenAndServe()
	}

	if err != nil {
		fmt.Printf("http.ListenAndServe(%d, %v) error\n", slf.port, err)
		os.Exit(1)
	}

	return nil
}

func (slf *HttpServer) SetHttps(certfile string, keyfile string) bool {
	slf.certfile = certfile
	slf.keyfile = keyfile
	slf.ishttps = true
	return true
}
