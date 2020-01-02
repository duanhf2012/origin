package originhttp

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/ioutil"
	"net/http"

	"os"
	"reflect"
	"runtime"
	"strings"
	"time"

	"github.com/duanhf2012/origin/sysmodule"

	"github.com/duanhf2012/origin/rpc"

	"github.com/duanhf2012/origin/cluster"
	"github.com/duanhf2012/origin/network"
	"github.com/duanhf2012/origin/service"
)

type HttpRedirectData struct {
	Url string
	//Cookies map[string]string
	
	CookieList []*http.Cookie
}

type HttpRequest struct {
	Header http.Header
	Body   string

	ParamStr string
	mapParam map[string]string
	URL string
	//Req http.Request
	
}


type HttpRespone struct {
	Respone []byte
	RedirectData HttpRedirectData
	//Resp http.ResponseWriter
}

type ServeHTTPRouterMux struct {
}
type ControllerMapsType map[string]reflect.Value

type HttpServerService struct {
	service.BaseService
	httpserver network.HttpServer
	port       uint16

	controllerMaps   ControllerMapsType
	certfile         string
	keyfile          string
	ishttps          bool
	httpfiltrateList []HttpFiltrate
}

type RouterMatchData struct {
	callpath   string
	matchURL   string
	routerType int8 //0表示函数调用  1表示静态资源
}

type RouterStaticResoutceData struct {
	localpath string
	method    string
}

type HTTP_METHOD int

const (
	METHOD_GET  HTTP_METHOD = iota
	METHOD_POST HTTP_METHOD = 1
	//METHOD_PUT  HTTP_METHOD = 2
)

var bPrintRequestTime bool

var postAliasUrl map[string]map[string]RouterMatchData //url地址，对应本service地址
var staticRouterResource map[string]RouterStaticResoutceData

func init() {
	postAliasUrl = make(map[string]map[string]RouterMatchData)
	postAliasUrl["GET"] = make(map[string]RouterMatchData)
	postAliasUrl["POST"] = make(map[string]RouterMatchData)

	staticRouterResource = make(map[string]RouterStaticResoutceData)
}

type HttpHandle func(request *HttpRequest, resp *HttpRespone) error

func AnalysisRouterUrl(url string) (string, error) {

	//替换所有空格
	url = strings.ReplaceAll(url, " ", "")
	if len(url) <= 1 || url[0] != '/' {
		return "", fmt.Errorf("url %s format is error!", url)
	}

	//去掉尾部的/
	return strings.Trim(url, "/"), nil
}

func (slf *HttpRequest) Query(key string) (string, bool) {
	if slf.mapParam == nil {
		slf.mapParam = make(map[string]string)
		//分析字符串
		slf.ParamStr = strings.Trim(slf.ParamStr, "/")
		paramStrList := strings.Split(slf.ParamStr, "&")
		for _, val := range paramStrList {
			param := strings.Split(val, "=")
			if len(param) == 2 {
				slf.mapParam[param[0]] = param[1]
			}
		}
	}

	ret, ok := slf.mapParam[key]
	return ret, ok
}

func Request(method HTTP_METHOD, url string, handle HttpHandle) error {
	fnpath := runtime.FuncForPC(reflect.ValueOf(handle).Pointer()).Name()

	sidx := strings.LastIndex(fnpath, "*")
	if sidx == -1 {
		return errors.New(fmt.Sprintf("http post func path is error, %s\n", fnpath))
	}

	eidx := strings.LastIndex(fnpath, "-fm")
	if sidx == -1 {
		return errors.New(fmt.Sprintf("http post func path is error, %s\n", fnpath))
	}
	callpath := fnpath[sidx+1 : eidx]
	ridx := strings.LastIndex(callpath, ")")
	if ridx == -1 {
		return errors.New(fmt.Sprintf("http post func path is error, %s\n", fnpath))
	}

	hidx := strings.LastIndex(callpath, "HTTP_")
	if hidx == -1 {
		return errors.New(fmt.Sprintf("http post func not contain HTTP_, %s\n", fnpath))
	}

	callpath = strings.ReplaceAll(callpath, ")", "")

	var r RouterMatchData
	var matchURL string
	var err error
	r.routerType = 0
	r.callpath = "_" + callpath
	matchURL, err = AnalysisRouterUrl(url)
	if err != nil {
		return err
	}

	var strMethod string
	if method == METHOD_GET {
		strMethod = "GET"
	} else if method == METHOD_POST {
		strMethod = "POST"
	} else {
		return fmt.Errorf("not support method.")
	}

	postAliasUrl[strMethod][matchURL] = r

	return nil
}

func Post(url string, handle HttpHandle) error {
	return Request(METHOD_POST, url, handle)
}

func Get(url string, handle HttpHandle) error {
	return Request(METHOD_GET, url, handle)
}

func (slf *HttpServerService) OnInit() error {
	slf.httpserver.Init(slf.port, &ServeHTTPRouterMux{}, 10*time.Second, 10*time.Second)
	if slf.ishttps == true {
		slf.httpserver.SetHttps(slf.certfile, slf.keyfile)
	}
	return nil
}

func (slf *ServeHTTPRouterMux) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	methodRouter, bok := postAliasUrl[r.Method]
	if bok == false {
		writeRespone(w, http.StatusNotFound, fmt.Sprint("Can not support method."))
		return
	}

	url := strings.Trim(r.URL.Path, "/")
	var strCallPath string
	matchData, ok := methodRouter[url]
	if ok == true {
		strCallPath = matchData.callpath
	} else {
		//如果是资源处理
		for k, v := range staticRouterResource {
			idx := strings.Index(url, k)
			if idx != -1 {
				staticServer(k, v, w, r)
				return
			}
		}

		// 拼接得到rpc服务的名称
		vstr := strings.Split(url, "/")
		if len(vstr) < 2 {
			writeRespone(w, http.StatusNotFound, "Cannot find path.")
			return
		}
		strCallPath = "_" + vstr[0] + ".HTTP_" + vstr[1]
	}

	defer r.Body.Close()
	msg, err := ioutil.ReadAll(r.Body)
	if err != nil {
		writeRespone(w, http.StatusBadRequest, "")
		return
	}

	request := HttpRequest{r.Header, string(msg), r.URL.RawQuery, nil,r.URL.Path}
	var resp HttpRespone
	//resp.Resp = w
	timeFuncStart := time.Now()
	err = cluster.InstanceClusterMgr().Call(strCallPath, &request, &resp)

	timeFuncPass := time.Since(timeFuncStart)
	if bPrintRequestTime {
		service.GetLogger().Printf(service.LEVER_INFO, "HttpServer Time : %s url : %s\n", timeFuncPass, strCallPath)
	}
	if err != nil {
		writeRespone(w, http.StatusBadRequest, fmt.Sprint(err))
	} else {
		if resp.RedirectData.Url != ""{
			resp.redirects(&w,r)
		}else {
			writeRespone(w, http.StatusOK, string(resp.Respone))
		}
		
	}
}

// CkResourceDir 检查静态资源文件夹路径
func SetStaticResource(method HTTP_METHOD, urlpath string, dirname string) error {
	_, err := os.Stat(dirname)
	if err != nil {
		return err
	}
	matchURL, berr := AnalysisRouterUrl(urlpath)
	if berr != nil {
		return berr
	}

	var routerData RouterStaticResoutceData
	if method == METHOD_GET {
		routerData.method = "GET"
	} else if method == METHOD_POST {
		routerData.method = "POST"
	}
	routerData.localpath = dirname

	staticRouterResource[matchURL] = routerData
	return nil
}

func writeRespone(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(status)
	w.Write([]byte(msg))
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

func (slf *HttpServerService) SetPrintRequestTime(isPrint bool) {
	bPrintRequestTime = isPrint
}

func staticServer(routerUrl string, routerData RouterStaticResoutceData, w http.ResponseWriter, r *http.Request) {
	upath := r.URL.Path
	idx := strings.Index(upath, routerUrl)
	subPath := strings.Trim(upath[idx+len(routerUrl):], "/")

	destLocalPath := routerData.localpath + subPath

	writeResp := func(status int, msg string) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.WriteHeader(status)
		w.Write([]byte(msg))
	}

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
		/*
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

			writeResp(http.StatusOK, upath+fileName)*/
	}

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

func (slf *HttpRespone) WriteRespones(Code int32, Msg string, Data interface{}) {

	var StrRet string
	//判断是否有错误码
	if Code > 0 {
		StrRet = fmt.Sprintf(`{"RCode": %d,"RMsg":"%s"}`, Code, Msg)
	} else {
		if Data == nil {
			if Msg != "" {
				StrRet = fmt.Sprintf(`{"RCode": 0,"RMsg":"%s"}`, Msg)
			} else {
				StrRet = `{"RCode": 0}`
			}
		} else {
			if reflect.TypeOf(Data).Kind() == reflect.String {
				StrRet = fmt.Sprintf(`{"RCode": %d , "Data": "%s"}`, Code, Data)
			} else {
				JsonRet, Err := json.Marshal(Data)
				if Err != nil {
					service.GetLogger().Printf(sysmodule.LEVER_ERROR, "common WriteRespone Json Marshal Err %+v", Data)
				} else {
					StrRet = fmt.Sprintf(`{"RCode": %d , "Data": %s}`, Code, JsonRet)
				}
			}
		}
	}
	slf.Respone = []byte(StrRet)
}

func (slf *HttpRespone) Redirect(url string,cookieList []*http.Cookie) {
	slf.RedirectData.Url = url
	slf.RedirectData.CookieList = cookieList
}




func (slf *HttpRespone) redirects(w *http.ResponseWriter, req *http.Request) {
	if slf.RedirectData.CookieList != nil {
		for _,v := range slf.RedirectData.CookieList{
			http.SetCookie(*w,v)
		}
	}

	http.Redirect(*w, req, slf.RedirectData.Url,
		// see @andreiavrammsd comment: often 307 > 301
		http.StatusTemporaryRedirect)
}
