package httpservice

import (
	"fmt"
	"github.com/duanhf2012/origin/event"
	"github.com/duanhf2012/origin/network"
	"github.com/duanhf2012/origin/service"
	"github.com/duanhf2012/origin/util/uuid"
	jsoniter "github.com/json-iterator/go"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

var json = jsoniter.ConfigCompatibleWithStandardLibrary

var DefaultReadTimeout time.Duration = time.Second * 10
var DefaultWriteTimeout time.Duration = time.Second * 10
var DefaultProcessTimeout time.Duration = time.Second * 10

//http redirect
type HttpRedirectData struct {
	Url        string
	CookieList []*http.Cookie
}

type HTTP_METHOD int

const (
	METHOD_NONE HTTP_METHOD = iota
	METHOD_GET
	METHOD_POST

	METHOD_INVALID
)

type HttpHandle func(session *HttpSession)
type routerMatchData struct {
	matchURL   string
	httpHandle HttpHandle
}

type routerServeFileData struct {
	matchUrl  string
	localPath string
	method    HTTP_METHOD
}

type IHttpRouter interface {
	GET(url string, handle HttpHandle) bool
	POST(url string, handle HttpHandle) bool
	Router(session *HttpSession)

	SetServeFile(method HTTP_METHOD, urlpath string, dirname string) error
	SetFormFileKey(formFileKey string)
	GetFormFileKey() string
	AddHttpFiltrate(FiltrateFun HttpFiltrate) bool
}

type HttpRouter struct {
	pathRouter       map[HTTP_METHOD]map[string]routerMatchData //url地址，对应本service地址
	serveFileData    map[string]*routerServeFileData
	httpFiltrateList []HttpFiltrate

	formFileKey string
}

type HttpSession struct {
	httpRouter IHttpRouter
	r          *http.Request
	w          http.ResponseWriter

	//parse result
	mapParam map[string]string
	body     []byte

	//processor result
	statusCode   int
	msg          []byte
	fileData     *routerServeFileData
	redirectData *HttpRedirectData
	sessionDone  chan *HttpSession
}

type HttpService struct {
	service.Service

	httpServer     network.HttpServer
	postAliasUrl   map[HTTP_METHOD]map[string]routerMatchData //url地址，对应本service地址
	httpRouter     IHttpRouter
	listenAddr     string
	corsHeader     *CORSHeader
	processTimeout time.Duration
	manualStart 	bool
}

type HttpFiltrate func(session *HttpSession) bool //true is pass

type CORSHeader struct {
	AllowCORSHeader map[string][]string
}

func (httpService *HttpService) AddFiltrate(FiltrateFun HttpFiltrate) bool {
	return httpService.httpRouter.AddHttpFiltrate(FiltrateFun)
}

func NewHttpHttpRouter() IHttpRouter {
	httpRouter := &HttpRouter{}
	httpRouter.pathRouter = map[HTTP_METHOD]map[string]routerMatchData{}
	httpRouter.serveFileData = map[string]*routerServeFileData{}
	httpRouter.formFileKey = "file"
	for i := METHOD_NONE + 1; i < METHOD_INVALID; i++ {
		httpRouter.pathRouter[i] = map[string]routerMatchData{}
	}

	return httpRouter
}

func (slf *HttpSession) GetRawQuery() string{
	return slf.r.URL.RawQuery
}

func (slf *HttpSession) Query(key string) (string, bool) {
	if slf.mapParam == nil {
		slf.mapParam = make(map[string]string)

		paramStr := strings.Trim(slf.r.URL.RawQuery, "/")
		paramStrList := strings.Split(paramStr, "&")
		for _, val := range paramStrList {
			index := strings.Index(val, "=")
			if index >= 0 {
				slf.mapParam[val[0:index]] = val[index+1:]
			}
		}
	}

	ret, ok := slf.mapParam[key]
	return ret, ok
}

func (slf *HttpSession) GetBody() []byte {
	return slf.body
}

func (slf *HttpSession) GetMethod() HTTP_METHOD {
	return slf.getMethod(slf.r.Method)
}

func (slf *HttpSession) GetPath() string {
	return strings.Trim(slf.r.URL.Path, "/")
}

func (slf *HttpSession) SetHeader(key, value string) {
	slf.w.Header().Set(key, value)
}

func (slf *HttpSession) AddHeader(key, value string) {
	slf.w.Header().Add(key, value)
}

func (slf *HttpSession) GetHeader(key string) string {
	return slf.r.Header.Get(key)
}

func (slf *HttpSession) DelHeader(key string) {
	slf.r.Header.Del(key)
}

func (slf *HttpSession) WriteStatusCode(statusCode int) {
	slf.statusCode = statusCode
}

func (slf *HttpSession) Write(msg []byte) {
	slf.msg = msg
}

func (slf *HttpSession) WriteJsonDone(statusCode int, msgJson interface{}) error {
	msg, err := json.Marshal(msgJson)
	if err != nil {
		return err
	}

	slf.statusCode = statusCode
	slf.Write(msg)
	slf.Done()
	return err
}

func (slf *HttpSession) flush() {
	slf.w.WriteHeader(slf.statusCode)
	if slf.msg != nil {
		slf.w.Write(slf.msg)
	}
}

func (slf *HttpSession) Done() {
	slf.sessionDone <- slf
}

func (slf *HttpSession) getMethod(method string) HTTP_METHOD {
	switch method {
	case "POST":
		return METHOD_POST
	case "GET":
		return METHOD_GET
	}

	return METHOD_INVALID
}

func (slf *HttpRouter) analysisRouterUrl(url string) (string, error) {

	//替换所有空格
	url = strings.ReplaceAll(url, " ", "")
	if len(url) <= 1 || url[0] != '/' {
		return "", fmt.Errorf("url %s format is error!", url)
	}

	//去掉尾部的/
	return strings.Trim(url, "/"), nil
}

func (slf *HttpSession) Handle() {
	slf.httpRouter.Router(slf)
}

func (slf *HttpRouter) SetFormFileKey(formFileKey string) {
	slf.formFileKey = formFileKey
}

func (slf *HttpRouter) GetFormFileKey() string {
	return slf.formFileKey
}

func (slf *HttpRouter) GET(url string, handle HttpHandle) bool {
	return slf.regRouter(METHOD_GET, url, handle)
}

func (slf *HttpRouter) POST(url string, handle HttpHandle) bool {
	return slf.regRouter(METHOD_POST, url, handle)
}

func (slf *HttpRouter) regRouter(method HTTP_METHOD, url string, handle HttpHandle) bool {
	mapRouter, ok := slf.pathRouter[method]
	if ok == false {
		return false
	}

	mapRouter[strings.Trim(url, "/")] = routerMatchData{httpHandle: handle}
	return true
}

func (slf *HttpRouter) Router(session *HttpSession) {
	if slf.httpFiltrateList != nil {
		for _, fun := range slf.httpFiltrateList {
			if fun(session) == false {
				//session.done()
				return
			}
		}
	}

	urlPath := session.GetPath()
	for {
		mapRouter, ok := slf.pathRouter[session.GetMethod()]
		if ok == false {
			break
		}

		v, ok := mapRouter[urlPath]
		if ok == false {
			break
		}

		v.httpHandle(session)
		return
	}

	for k, v := range slf.serveFileData {
		idx := strings.Index(urlPath, k)
		if idx != -1 {
			session.fileData = v
			session.Done()
			return
		}
	}

	session.WriteStatusCode(http.StatusNotFound)
	session.Done()
}

func (httpService *HttpService) HttpEventHandler(ev event.IEvent) {
	ev.(*event.Event).Data.(*HttpSession).Handle()
}

func (httpService *HttpService) SetHttpRouter(httpRouter IHttpRouter, eventHandler event.IEventHandler) {
	httpService.httpRouter = httpRouter
	httpService.RegEventReceiverFunc(event.Sys_Event_Http_Event, eventHandler, httpService.HttpEventHandler)
}

func (slf *HttpRouter) SetServeFile(method HTTP_METHOD, urlpath string, dirname string) error {
	_, err := os.Stat(dirname)
	if err != nil {
		return err
	}
	matchURL, aErr := slf.analysisRouterUrl(urlpath)
	if aErr != nil {
		return aErr
	}

	var routerData routerServeFileData
	routerData.method = method
	routerData.localPath = dirname
	routerData.matchUrl = matchURL
	slf.serveFileData[matchURL] = &routerData

	return nil
}

func (slf *HttpRouter) AddHttpFiltrate(FiltrateFun HttpFiltrate) bool {
	slf.httpFiltrateList = append(slf.httpFiltrateList, FiltrateFun)
	return false
}

func (slf *HttpSession) Redirect(url string, cookieList []*http.Cookie) {
	redirectData := &HttpRedirectData{}
	redirectData.Url = url
	redirectData.CookieList = cookieList

	slf.redirectData = redirectData
}

func (slf *HttpSession) redirects() {
	if slf.redirectData == nil {
		return
	}

	if slf.redirectData.CookieList != nil {
		for _, v := range slf.redirectData.CookieList {
			http.SetCookie(slf.w, v)
		}
	}

	http.Redirect(slf.w, slf.r, slf.redirectData.Url,
		http.StatusTemporaryRedirect)
}

func (httpService *HttpService) OnInit() error {
	iConfig := httpService.GetServiceCfg()
	if iConfig == nil {
		return fmt.Errorf("%s service config is error!", httpService.GetName())
	}
	httpCfg := iConfig.(map[string]interface{})
	addr, ok := httpCfg["ListenAddr"]
	if ok == false {
		return fmt.Errorf("%s service config is error!", httpService.GetName())
	}
	var readTimeout time.Duration = DefaultReadTimeout
	var writeTimeout time.Duration = DefaultWriteTimeout

	if cfgRead, ok := httpCfg["ReadTimeout"]; ok == true {
		readTimeout = time.Duration(cfgRead.(float64)) * time.Millisecond
	}

	if cfgWrite, ok := httpCfg["WriteTimeout"]; ok == true {
		writeTimeout = time.Duration(cfgWrite.(float64)) * time.Millisecond
	}

	if manualStart, ok := httpCfg["ManualStart"]; ok == true {
		httpService.manualStart = manualStart.(bool)
	}else{
		manualStart =false
	}

	httpService.processTimeout = DefaultProcessTimeout
	if cfgProcessTimeout, ok := httpCfg["ProcessTimeout"]; ok == true {
		httpService.processTimeout = time.Duration(cfgProcessTimeout.(float64)) * time.Millisecond
	}

	httpService.httpServer.Init(addr.(string), httpService, readTimeout, writeTimeout)
	//Set CAFile
	caFileList, ok := httpCfg["CAFile"]
	if ok == false {
		return nil
	}
	iCaList := caFileList.([]interface{})
	var caFile []network.CAFile
	for _, i := range iCaList {
		mapCAFile := i.(map[string]interface{})
		c, ok := mapCAFile["Certfile"]
		if ok == false {
			continue
		}
		k, ok := mapCAFile["Keyfile"]
		if ok == false {
			continue
		}

		if c.(string) != "" && k.(string) != "" {
			caFile = append(caFile, network.CAFile{
				CertFile: c.(string),
				Keyfile:  k.(string),
			})
		}
	}
	httpService.httpServer.SetCAFile(caFile)

	if httpService.manualStart == false {
		httpService.httpServer.Start()
	}

	return nil
}

func (httpService *HttpService) StartListen() {
	if httpService.manualStart {
		httpService.httpServer.Start()
	}
}

func (httpService *HttpService) SetAllowCORS(corsHeader *CORSHeader) {
	httpService.corsHeader = corsHeader
}

func (httpService *HttpService) ProcessFile(session *HttpSession) {
	uPath := session.r.URL.Path
	idx := strings.Index(uPath, session.fileData.matchUrl)
	subPath := strings.Trim(uPath[idx+len(session.fileData.matchUrl):], "/")

	destLocalPath := session.fileData.localPath + "/" + subPath

	switch session.GetMethod() {
	case METHOD_GET:
		//判断文件夹是否存在
		_, err := os.Stat(destLocalPath)
		if err == nil {
			http.ServeFile(session.w, session.r, destLocalPath)
		} else {
			session.WriteStatusCode(http.StatusNotFound)
			session.flush()
			return
		}
	//上传资源
	case METHOD_POST:
		// 在这儿处理例外路由接口
		session.r.ParseMultipartForm(32 << 20) // max memory is set to 32MB
		resourceFile, resourceFileHeader, err := session.r.FormFile(session.httpRouter.GetFormFileKey())
		if err != nil {
			session.WriteStatusCode(http.StatusNotFound)
			session.flush()
			return
		}
		defer resourceFile.Close()
		//重新拼接文件名
		imgFormat := strings.Split(resourceFileHeader.Filename, ".")
		if len(imgFormat) < 2 {
			session.WriteStatusCode(http.StatusNotFound)
			session.flush()
			return
		}
		filePrefixName := uuid.Rand().HexEx()
		fileName := filePrefixName + "." + imgFormat[len(imgFormat)-1]
		//创建文件
		localPath := fmt.Sprintf("%s%s", destLocalPath, fileName)
		localFd, err := os.OpenFile(localPath, os.O_WRONLY|os.O_CREATE, 0666)
		if err != nil {
			session.WriteStatusCode(http.StatusNotFound)
			session.flush()
			return
		}
		defer localFd.Close()
		io.Copy(localFd, resourceFile)
		session.WriteStatusCode(http.StatusOK)
		session.Write([]byte(uPath + "/" + fileName))
		session.flush()
	}
}

func NewAllowCORSHeader() *CORSHeader {
	header := &CORSHeader{}
	header.AllowCORSHeader = map[string][]string{}
	header.AllowCORSHeader["Access-Control-Allow-Origin"] = []string{"*"}
	header.AllowCORSHeader["Access-Control-Allow-Methods"] = []string{"POST, GET, OPTIONS, PUT, DELETE"}
	header.AllowCORSHeader["Access-Control-Allow-Headers"] = []string{"Content-Type"}

	return header
}

func (slf *CORSHeader) AddAllowHeader(key string, val string) {
	slf.AllowCORSHeader["Access-Control-Allow-Headers"] = append(slf.AllowCORSHeader["Access-Control-Allow-Headers"], fmt.Sprintf("%s,%s", key, val))
}

func (slf *CORSHeader) copyTo(header http.Header) {
	for k, v := range slf.AllowCORSHeader {
		for _, val := range v {
			header.Add(k, val)
		}
	}
}

func (httpService *HttpService) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if httpService.corsHeader != nil {
		if origin := r.Header.Get("Origin"); origin != "" {
			httpService.corsHeader.copyTo(w.Header())
		}
	}
	if r.Method == "OPTIONS" {
		return
	}

	session := &HttpSession{sessionDone: make(chan *HttpSession, 1), httpRouter: httpService.httpRouter, statusCode: http.StatusOK}
	session.r = r
	session.w = w

	defer r.Body.Close()
	body, err := io.ReadAll(r.Body)
	if err != nil {
		session.WriteStatusCode(http.StatusGatewayTimeout)
		session.flush()
		return
	}
	session.body = body

	httpService.GetEventHandler().NotifyEvent(&event.Event{Type: event.Sys_Event_Http_Event, Data: session})
	ticker := time.NewTicker(httpService.processTimeout)
	select {
	case <-ticker.C:
		session.WriteStatusCode(http.StatusGatewayTimeout)
		session.flush()
		break
	case <-session.sessionDone:
		if session.fileData != nil {
			httpService.ProcessFile(session)
		} else if session.redirectData != nil {
			session.redirects()
		} else {
			session.flush()
		}
	}
}
