package network

import (
	"fmt"
	"net/http"
	"time"
)

type HttpServer struct {
	port uint16

	handler      http.Handler
	readtimeout  time.Duration
	writetimeout time.Duration

	httpserver *http.Server
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

	err := slf.httpserver.ListenAndServe()
	if err != nil {
		fmt.Printf("http.ListenAndServe(%d, nil) error\n", slf.port)
	}

	return nil
}
