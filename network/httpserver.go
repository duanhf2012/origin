package network

import (
	"fmt"
	"net/http"
)

type HttpServer struct {
	port uint16
}

func (slf *HttpServer) Init(port uint16) {
	slf.port = port
}

func (slf *HttpServer) HandleFunc(pattern string, handler func(http.ResponseWriter, *http.Request)) {
	http.HandleFunc(pattern, handler)
}

func (slf *HttpServer) Start() {
	go slf.startListen()
}

func (slf *HttpServer) startListen() error {
	listenPort := fmt.Sprintf(":%d", slf.port)
	err := http.ListenAndServe(listenPort, nil)
	if err != nil {
		fmt.Printf("http.ListenAndServe(%d, nil) error\n", slf.port)
	}

	return nil
}

func (slf *WebsocketServer) Stop() {

}
