package sysservice

import (
	"fmt"
	"io/ioutil"
	"net/http"
	"reflect"
	"strings"
	"time"

	"github.com/duanhf2012/origin/rpc"
	"github.com/gorilla/mux"
	"github.com/gotoxu/cors"

	"github.com/duanhf2012/origin/cluster"
	"github.com/duanhf2012/origin/network"
	"github.com/duanhf2012/origin/service"
)

type HttpRequest struct {
	Body string
}

type HttpRespone struct {
	Respone []byte
}

type ControllerMapsType map[string]reflect.Value

type HttpServerService struct {
	service.BaseService
	httpserver network.HttpServer
	port       uint16

	controllerMaps ControllerMapsType
}

func (slf *HttpServerService) OnInit() error {
	slf.httpserver.Init(slf.port, slf.initRouterHandler(), 10*time.Second, 10*time.Second)
	return nil
}

func (slf *HttpServerService) initRouterHandler() http.Handler {
	r := mux.NewRouter()
	r.HandleFunc("/{server:[a-zA-Z0-9]+}/{method:[a-zA-Z0-9]+}", func(w http.ResponseWriter, r *http.Request) {
		slf.httpHandler(w, r)
	})

	cors := cors.AllowAll()
	//return cors.Handler(gziphandler.GzipHandler(r))
	return cors.Handler(r)
}

func (slf *HttpServerService) OnRun() error {

	slf.httpserver.Start()
	return nil
}

func NewHttpServerService(port uint16) *HttpServerService {
	http := new(HttpServerService)

	http.port = port
	http.Init(http, 0)
	return http
}

func (slf *HttpServerService) OnDestory() error {
	return nil
}

func (slf *HttpServerService) OnSetupService(iservice service.IService) {
	//
	rpc.RegisterName(iservice.GetServiceName(), "HTTP_", iservice)
}

func (slf *HttpServerService) OnRemoveService(iservice service.IService) {
	return
}

func (slf *HttpServerService) httpHandler(w http.ResponseWriter, r *http.Request) {
	writeError := func(status int, msg string) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.WriteHeader(status)
		w.Write([]byte(msg))
	}
	if r.Method != "POST" {
		writeError(http.StatusMethodNotAllowed, "rpc: POST method required, received "+r.Method)
		return
	}
	defer r.Body.Close()
	msg, err := ioutil.ReadAll(r.Body)
	if err != nil {
		writeError(http.StatusBadRequest, "rpc: ioutil.ReadAll "+err.Error())
		return
	}
	fmt.Printf("PATH: %s\n%s\n", r.URL.Path, string(msg))
	// 在这儿处理例外路由接口

	// 拼接得到rpc服务的名称
	vstr := strings.Split(r.URL.Path, "/")
	if len(vstr) != 3 {
		writeError(http.StatusBadRequest, "rpc: ioutil.ReadAll "+err.Error())
		return
	}
	strCallPath := "_" + vstr[1] + ".HTTP_" + vstr[2]

	request := HttpRequest{string(msg)}
	var resp HttpRespone

	cluster.InstanceClusterMgr().Call(strCallPath, &request, &resp)
	w.Header().Set("Content-Type", "application/json;charset=utf-8")
	w.Write([]byte(resp.Respone))
}

func (slf *HttpServerService) GetMethod(strCallPath string) (*reflect.Value, error) {
	value, ok := slf.controllerMaps[strCallPath]
	if ok == false {
		err := fmt.Errorf("not find api")
		return nil, err
	}

	return &value, nil
}
