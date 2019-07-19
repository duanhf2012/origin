package sysservice

import (
	"encoding/json"
	"fmt"
	"io"
	"io/ioutil"
	"net/http"
	"os"
	"reflect"
	"strings"
	"time"

	"github.com/duanhf2012/origin/sysmodule"
	"github.com/duanhf2012/origin/util/uuid"

	"github.com/duanhf2012/origin/rpc"
	"github.com/gorilla/mux"
	"github.com/gotoxu/cors"

	"github.com/duanhf2012/origin/cluster"
	"github.com/duanhf2012/origin/network"
	"github.com/duanhf2012/origin/service"
)

type HttpRequest struct {
	Header http.Header
	Body   string
}

type HttpRespone struct {
	Respone []byte
}

type ControllerMapsType map[string]reflect.Value

type HttpServerService struct {
	service.BaseService
	httpserver       network.HttpServer
	port             uint16
	PrintRequestTime bool
	controllerMaps   ControllerMapsType
	certfile         string
	keyfile          string
	ishttps          bool
	httpfiltrateList []HttpFiltrate
	resourcedir      string //静态资源下载路径
}

func (slf *HttpServerService) OnInit() error {
	//
	slf.httpserver.Init(slf.port, slf.initRouterHandler(), 10*time.Second, 10*time.Second)
	if slf.ishttps == true {
		slf.httpserver.SetHttps(slf.certfile, slf.keyfile)
	}
	return nil
}

// CkUploadImgDir 检查文件上传路径
func (slf *HttpServerService) CkResourceDir(dirname string) error {
	slf.resourcedir = dirname
	return nil
}

func (slf *HttpServerService) initRouterHandler() http.Handler {
	r := mux.NewRouter()
	r.HandleFunc("/{server:[a-zA-Z0-9]+}/{method:[a-zA-Z0-9]+}", func(w http.ResponseWriter, r *http.Request) {
		slf.httpHandler(w, r)
	})

	//获取静态文件资源
	r.HandleFunc("/"+slf.resourcedir+"/{filename:.*}", func(w http.ResponseWriter, r *http.Request) {
		slf.staticServer(w, r)
	})

	cors := cors.AllowAll()
	//return cors.Handler(gziphandler.GzipHandler(r))
	return cors.Handler(r)
}

type HttpFiltrate func(path string, w http.ResponseWriter, r *http.Request) error

func (slf *HttpServerService) AppendHttpFiltrate(fun HttpFiltrate) bool {
	slf.httpfiltrateList = append(slf.httpfiltrateList, fun)

	return false
}

func (slf *HttpServerService) OnRun() bool {

	slf.httpserver.Start()
	return false
}

func NewHttpServerService(port uint16) *HttpServerService {
	http := new(HttpServerService)

	http.port = port
	return http
}

func (slf *HttpServerService) OnDestory() error {
	return nil
}

func (slf *HttpServerService) OnSetupService(iservice service.IService) {
	rpc.RegisterName(iservice.GetServiceName(), "HTTP_", iservice)
}

func (slf *HttpServerService) OnRemoveService(iservice service.IService) {
	return
}

func (slf *HttpServerService) IsPrintRequestTime() bool {
	if slf.PrintRequestTime == true {
		return true
	}
	return false

}

func (slf *HttpServerService) SetPrintRequestTime(isPrint bool) {
	slf.PrintRequestTime = isPrint
}

func (slf *HttpServerService) staticServer(w http.ResponseWriter, r *http.Request) {
	writeResp := func(status int, msg string) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.WriteHeader(status)
		w.Write([]byte(msg))
	}
	nowpath, _ := os.Getwd()
	upath := r.URL.Path
	destLocalPath := nowpath + upath
	switch r.Method {
	//获取资源
	case "GET":
		//判断文件夹是否存在
		_, err := os.Stat(destLocalPath)
		if err == nil {
			http.ServeFile(w, r, destLocalPath)
		} else {
			writeResp(http.StatusNotFound, "")
			return
		}
	//上传资源
	case "POST":
		r.ParseMultipartForm(32 << 20) // max memory is set to 32MB
		resourceFile, resourceFileHeader, err := r.FormFile("file")
		if err != nil {
			fmt.Println(err)
			writeResp(http.StatusNotFound, err.Error())
			return
		}
		defer resourceFile.Close()
		//重新拼接文件名
		imgFormat := strings.Split(resourceFileHeader.Filename, ".")
		if len(imgFormat) != 2 {
			writeResp(http.StatusNotFound, "not a file")
			return
		}
		filePrefixName := uuid.Rand().HexEx()
		fileName := filePrefixName + "." + imgFormat[1]
		//创建文件
		localpath := fmt.Sprintf("%s%s", destLocalPath, fileName)
		localfd, err := os.OpenFile(localpath, os.O_WRONLY|os.O_CREATE, 0666)
		if err != nil {
			fmt.Println(err)
			writeResp(http.StatusNotFound, "upload fail")
			return
		}
		defer localfd.Close()

		io.Copy(localfd, resourceFile)
		writeResp(http.StatusOK, localpath)
	}

}

func (slf *HttpServerService) httpHandler(w http.ResponseWriter, r *http.Request) {
	writeError := func(status int, msg string) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.WriteHeader(status)
		w.Write([]byte(msg))
	}
	if r.Method != "POST" {
		//writeError(http.StatusMethodNotAllowed, "rpc: POST method required, received "+r.Method)
		//return
	}
	defer r.Body.Close()
	msg, err := ioutil.ReadAll(r.Body)
	if err != nil {
		writeError(http.StatusBadRequest, "rpc: ioutil.ReadAll "+err.Error())
		return
	}

	// 在这儿处理例外路由接口
	var errRet error
	for _, filter := range slf.httpfiltrateList {
		ret := filter(r.URL.Path, w, r)
		if ret == nil {
			errRet = nil
			break
		} else {
			errRet = ret
		}
	}

	if errRet != nil {
		w.Write([]byte(errRet.Error()))
		return
	}

	// 拼接得到rpc服务的名称
	vstr := strings.Split(r.URL.Path, "/")
	if len(vstr) != 3 {
		writeError(http.StatusBadRequest, "rpc: ioutil.ReadAll "+err.Error())
		return
	}
	strCallPath := "_" + vstr[1] + ".HTTP_" + vstr[2]

	request := HttpRequest{r.Header, string(msg)}
	var resp HttpRespone

	TimeFuncStart := time.Now()
	err = cluster.InstanceClusterMgr().Call(strCallPath, &request, &resp)

	TimeFuncPass := time.Since(TimeFuncStart)
	if slf.IsPrintRequestTime() {
		service.GetLogger().Printf(service.LEVER_INFO, "HttpServer Time : %s url : %s", TimeFuncPass, strCallPath)
	}

	w.Header().Set("Content-Type", "application/json;charset=utf-8")
	if err != nil {
		resp.Respone = []byte(fmt.Sprint(err))
	}
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

func (slf *HttpServerService) SetHttps(certfile string, keyfile string) bool {
	if certfile == "" || keyfile == "" {
		return false
	}

	slf.ishttps = true
	slf.certfile = certfile
	slf.keyfile = keyfile

	return true
}

//序列化后写入Respone
func (slf *HttpRespone) WriteRespne(v interface{}) error {
	StrRet, retErr := json.Marshal(v)
	if retErr != nil {
		slf.Respone = []byte(`{"Code": 2,"Message":"service error"}`)
		service.GetLogger().Printf(sysmodule.LEVER_ERROR, "Json Marshal Error:%v\n", retErr)
	} else {
		slf.Respone = StrRet
	}

	return retErr
}
