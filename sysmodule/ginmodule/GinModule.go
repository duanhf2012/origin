package ginmodule

import (
	"context"
	"datacenter/common/processor"
	"github.com/duanhf2012/origin/v2/event"
	"github.com/duanhf2012/origin/v2/log"
	"github.com/duanhf2012/origin/v2/service"
	"github.com/gin-gonic/gin"
	"log/slog"
	"net/http"
	"strings"
)

type GinModule struct {
	service.Module

	*GinConf
	*gin.Engine
	srv *http.Server

	processor []processor.IGinProcessor
}

type GinConf struct {
	Addr string
}

const Sys_Event_Gin_Event event.EventType = -11

func (gm *GinModule) Init(conf *GinConf, engine *gin.Engine) {
	gm.GinConf = conf
	gm.Engine = engine
}

func (gm *GinModule) SetupDataProcessor(processor ...processor.IGinProcessor) {
	gm.processor = processor
}

func (gm *GinModule) AppendDataProcessor(processor ...processor.IGinProcessor) {
	gm.processor = append(gm.processor, processor...)
}

func (gm *GinModule) OnInit() error {
	if gm.Engine == nil {
		gm.Engine = gin.Default()
	}

	gm.srv = &http.Server{
		Addr:    gm.Addr,
		Handler: gm.Engine,
	}

	gm.Engine.Use(Logger())
	gm.Engine.Use(gin.Recovery())
	gm.GetEventProcessor().RegEventReceiverFunc(Sys_Event_Gin_Event, gm.GetEventHandler(), gm.eventHandler)
	return nil
}

func (gm *GinModule) eventHandler(ev event.IEvent) {
	ginEvent := ev.(*GinEvent)
	for _, handler := range ginEvent.handlersChain {
		handler(ginEvent.c)
	}

	ginEvent.chanWait <- struct{}{}
}

func (gm *GinModule) Start() {
	log.Info("http start listen", slog.Any("addr", gm.Addr))
	go func() {
		err := gm.srv.ListenAndServe()
		if err != nil {
			log.Error("ListenAndServe error", slog.Any("error", err.Error()))
		}
	}()
}

func (gm *GinModule) StartTLS(certFile, keyFile string) {
	log.Info("http start listen", slog.Any("addr", gm.Addr))
	go func() {
		err := gm.srv.ListenAndServeTLS(certFile, keyFile)
		if err != nil {
			log.Fatal("ListenAndServeTLS error", slog.Any("error", err.Error()))
		}
	}()
}

func (gm *GinModule) Stop(ctx context.Context) {
	if err := gm.srv.Shutdown(ctx); err != nil {
		log.SError("Server Shutdown", slog.Any("error", err))
	}
}

type GinEvent struct {
	handlersChain gin.HandlersChain
	chanWait      chan struct{}
	c             *gin.Context
}

func (ge *GinEvent) GetEventType() event.EventType {
	return Sys_Event_Gin_Event
}

func (gm *GinModule) handleMethod(httpMethod, relativePath string, handlers ...gin.HandlerFunc) gin.IRoutes {
	return gm.Engine.Handle(httpMethod, relativePath, func(c *gin.Context) {
		for _, p := range gm.processor {
			_, err := p.Process(c)
			if err != nil {
				return
			}
		}

		var ev GinEvent
		chanWait := make(chan struct{})
		ev.chanWait = chanWait
		ev.handlersChain = handlers
		ev.c = c
		gm.NotifyEvent(&ev)

		<-chanWait
	})
}

func (gm *GinModule) SafeGET(relativePath string, handlers ...gin.HandlerFunc) gin.IRoutes {
	return gm.handleMethod(http.MethodGet, relativePath, handlers...)
}

func (gm *GinModule) SafePOST(relativePath string, handlers ...gin.HandlerFunc) gin.IRoutes {
	return gm.handleMethod(http.MethodPost, relativePath, handlers...)
}

func (gm *GinModule) SafeDELETE(relativePath string, handlers ...gin.HandlerFunc) gin.IRoutes {
	return gm.handleMethod(http.MethodDelete, relativePath, handlers...)
}

func (gm *GinModule) SafePATCH(relativePath string, handlers ...gin.HandlerFunc) gin.IRoutes {
	return gm.handleMethod(http.MethodPatch, relativePath, handlers...)
}

func (gm *GinModule) SafePut(relativePath string, handlers ...gin.HandlerFunc) gin.IRoutes {
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
