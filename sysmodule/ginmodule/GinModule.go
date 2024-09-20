package ginmodule

import (
	"context"
	"github.com/duanhf2012/origin/v2/event"
	"github.com/duanhf2012/origin/v2/log"
	"github.com/duanhf2012/origin/v2/service"
	"github.com/gin-gonic/gin"
	"log/slog"
	"net/http"
	"strings"
	"time"
	"io"
)

type IGinProcessor interface {
	Process(data *gin.Context) (*gin.Context, error)
}

type GinModule struct {
	service.Module

	*gin.Engine
	srv *http.Server

	listenAddr string
	handleTimeout time.Duration
	processor []IGinProcessor
}

func (gm *GinModule) Init(addr string, handleTimeout time.Duration,engine *gin.Engine) {
	gm.listenAddr = addr
	gm.handleTimeout = handleTimeout
	gm.Engine = engine
}

func (gm *GinModule) SetupDataProcessor(processor ...IGinProcessor) {
	gm.processor = processor
}

func (gm *GinModule) AppendDataProcessor(processor ...IGinProcessor) {
	gm.processor = append(gm.processor, processor...)
}

func (gm *GinModule) OnInit() error {
	if gm.Engine == nil {
		gm.Engine = gin.Default()
	}

	gm.srv = &http.Server{
		Addr:    gm.listenAddr,
		Handler: gm.Engine,
	}

	gm.Engine.Use(Logger())
	gm.Engine.Use(gin.Recovery())
	gm.GetEventProcessor().RegEventReceiverFunc(event.Sys_Event_Gin_Event, gm.GetEventHandler(), gm.eventHandler)
	return nil
}

func (gm *GinModule) eventHandler(ev event.IEvent) {
	ginEvent := ev.(*GinEvent)
	for _, handler := range ginEvent.handlersChain {
		handler(&ginEvent.c)
	}

	//ginEvent.chanWait <- struct{}{}
}

func (gm *GinModule) Start() {
	gm.srv.Addr = gm.listenAddr
	log.Info("http start listen", slog.Any("addr", gm.listenAddr))
	go func() {
		err := gm.srv.ListenAndServe()
		if err != nil {
			log.Error("ListenAndServe error", slog.Any("error", err.Error()))
		}
	}()
}

func (gm *GinModule) StartTLS(certFile, keyFile string) {
	log.Info("http start listen", slog.Any("addr", gm.listenAddr))
	go func() {
		err := gm.srv.ListenAndServeTLS(certFile, keyFile)
		if err != nil {
			log.Fatal("ListenAndServeTLS error", slog.Any("error", err.Error()))
		}
	}()
}

func (gm *GinModule) Stop(ctx context.Context) {
	if err := gm.srv.Shutdown(ctx); err != nil {
		log.Error("Server Shutdown", slog.Any("error", err))
	}
}

type SafeContext struct {
	*gin.Context
	chanWait      chan struct{}
}

func (c *SafeContext) JSONAndDone(code int, obj any) {
	c.Context.JSON(code,obj)
	c.Done()
}

func (c *SafeContext) AsciiJSONAndDone(code int, obj any){
	c.Context.AsciiJSON(code,obj)
	c.Done()
}

func (c *SafeContext) PureJSONAndDone(code int, obj any){
	c.Context.PureJSON(code,obj)
	c.Done()
}

func (c *SafeContext) XMLAndDone(code int, obj any){
	c.Context.XML(code,obj)
	c.Done()
}

func (c *SafeContext) YAMLAndDone(code int, obj any){
	c.Context.YAML(code,obj)
	c.Done()
}

func (c *SafeContext) TOMLAndDone(code int, obj any){
	c.Context.TOML(code,obj)
	c.Done()
}

func (c *SafeContext) ProtoBufAndDone(code int, obj any){
	c.Context.ProtoBuf(code,obj)
	c.Done()
}

func (c *SafeContext) StringAndDone(code int, format string, values ...any){
	c.Context.String(code,format,values...)
	c.Done()
}

func (c *SafeContext) RedirectAndDone(code int, location string){
	c.Context.Redirect(code,location)
	c.Done()
}

func (c *SafeContext) DataAndDone(code int, contentType string, data []byte){
	c.Context.Data(code,contentType,data)
	c.Done()
}

func (c *SafeContext) DataFromReaderAndDone(code int, contentLength int64, contentType string, reader io.Reader, extraHeaders map[string]string){
	c.DataFromReader(code,contentLength,contentType,reader,extraHeaders)
	c.Done()
}

func (c *SafeContext) HTMLAndDone(code int, name string, obj any){
	c.Context.HTML(code,name,obj)
	c.Done()
}

func (c *SafeContext) IndentedJSONAndDone(code int, obj any){
	c.Context.IndentedJSON(code,obj)
	c.Done()
}

func (c *SafeContext) SecureJSONAndDone(code int, obj any){
	c.Context.SecureJSON(code,obj)
	c.Done()
}

func (c *SafeContext) JSONPAndDone(code int, obj any){
	c.Context.JSONP(code,obj)
	c.Done()
}

func (c *SafeContext) Done(){
	c.chanWait <- struct{}{}
}

type GinEvent struct {
	handlersChain []SafeHandlerFunc
	c SafeContext
}

type SafeHandlerFunc func(*SafeContext)

func (ge *GinEvent) GetEventType() event.EventType {
	return event.Sys_Event_Gin_Event
}

func (gm *GinModule) handleMethod(httpMethod, relativePath string, handlers ...SafeHandlerFunc) gin.IRoutes {
	return gm.Engine.Handle(httpMethod, relativePath, func(c *gin.Context) {
		for _, p := range gm.processor {
			_, err := p.Process(c)
			if err != nil {
				return
			}
		}

		var ev GinEvent
		chanWait := make(chan struct{},2)
		ev.c.chanWait = chanWait
		ev.handlersChain = handlers
		ev.c.Context = c
		gm.NotifyEvent(&ev)

		ctx,cancel := context.WithTimeout(context.Background(), gm.handleTimeout)
		defer cancel()

		select{
		case <-ctx.Done():
			log.Error("GinModule process timeout", slog.Any("path", c.Request.URL.Path))
			c.AbortWithStatus(http.StatusRequestTimeout)
		case <-chanWait:
		}
	})
}

// GET 回调处理是在gin协程中
func (gm *GinModule) GET(relativePath string, handlers ...gin.HandlerFunc) gin.IRoutes {
	return gm.Engine.GET(relativePath, handlers...)
}

// POST 回调处理是在gin协程中
func (gm *GinModule) POST(relativePath string, handlers ...gin.HandlerFunc) gin.IRoutes {
	return gm.Engine.POST(relativePath, handlers...)
}

// DELETE 回调处理是在gin协程中
func (gm *GinModule) DELETE(relativePath string, handlers ...gin.HandlerFunc) gin.IRoutes {
	return gm.Engine.DELETE(relativePath, handlers...)
}

// PATCH 回调处理是在gin协程中
func (gm *GinModule) PATCH(relativePath string, handlers ...gin.HandlerFunc) gin.IRoutes {
	return gm.Engine.PATCH(relativePath, handlers...)
}

// Put 回调处理是在gin协程中
func (gm *GinModule) Put(relativePath string, handlers ...gin.HandlerFunc) gin.IRoutes {
	return gm.Engine.PUT(relativePath, handlers...)
}

// SafeGET 回调处理是在service协程中
func (gm *GinModule) SafeGET(relativePath string, handlers ...SafeHandlerFunc) gin.IRoutes {
	return gm.handleMethod(http.MethodGet, relativePath, handlers...)
}

// SafePOST 回调处理是在service协程中
func (gm *GinModule) SafePOST(relativePath string, handlers ...SafeHandlerFunc) gin.IRoutes {
	return gm.handleMethod(http.MethodPost, relativePath, handlers...)
}

// SafeDELETE 回调处理是在service协程中
func (gm *GinModule) SafeDELETE(relativePath string, handlers ...SafeHandlerFunc) gin.IRoutes {
	return gm.handleMethod(http.MethodDelete, relativePath, handlers...)
}

// SafePATCH 回调处理是在service协程中
func (gm *GinModule) SafePATCH(relativePath string, handlers ...SafeHandlerFunc) gin.IRoutes {
	return gm.handleMethod(http.MethodPatch, relativePath, handlers...)
}

// SafePut 回调处理是在service协程中
func (gm *GinModule) SafePut(relativePath string, handlers ...SafeHandlerFunc) gin.IRoutes {
	return gm.handleMethod(http.MethodPut, relativePath, handlers...)
}

func GetIPWithProxyHeaders(c *gin.Context) string {
	// 尝试从 X-Real-IP 头部获取真实 IP
	ip := c.GetHeader("X-Real-IP")

	// 如果 X-Real-IP 头部不存在，则尝试从 X-Forwarded-For 头部获取
	if ip == "" {
		ip = c.GetHeader("X-Forwarded-For")
	}

	// 如果两者都不存在，则使用默认的 ClientIP 方法获取 IP
	if ip == "" {
		ip = c.ClientIP()
	}

	return ip
}

func GetIPWithValidatedProxyHeaders(c *gin.Context) string {
	// 获取代理头部
	proxyHeaders := c.Request.Header.Get("X-Real-IP,X-Forwarded-For")

	// 分割代理头部，取第一个 IP 作为真实 IP
	ips := strings.Split(proxyHeaders, ",")
	ip := strings.TrimSpace(ips[0])

	// 如果 IP 格式合法，则使用获取到的 IP，否则使用默认的 ClientIP 方法获取
	if isValidIP(ip) {
		return ip
	} else {
		ip = c.ClientIP()
		return ip
	}
}

// isValidIP 判断 IP 格式是否合法
func isValidIP(ip string) bool {
	// 此处添加自定义的 IP 格式验证逻辑
	// 例如，使用正则表达式验证 IP 格式
	// ...
	if ip == "" {
		return false
	}

	return true
}
