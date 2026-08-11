package ginmodule

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"runtime/debug"
	"strings"
	"sync"

	"github.com/duanhf2012/origin/v3/errs"
	originlog "github.com/duanhf2012/origin/v3/log"
	"github.com/duanhf2012/origin/v3/service"
	"github.com/gin-gonic/gin"
)

// Module 是业务 HTTP Module 可匿名嵌入的 Gin Server 生命周期与路由外观。
//
// 业务 Module 在 OnInit 中调用一次 Setup，随后只通过当前 Module 注册路由和 Middleware。
// Module 绑定 Service 后不得复制。
type Module struct {
	service.Module

	mu         sync.RWMutex
	address    string
	options    ServerOptions
	engine     *gin.Engine
	server     *http.Server
	listener   net.Listener
	serveDone  chan struct{}
	serveError error
	configured bool
	started    bool
	stopped    bool

	counters serverCounters
}

// Setup 校验并初始化当前 Module 私有的 Gin Engine 和 http.Server。
//
// Setup 只能在所属业务 Module.OnInit 中调用一次，并且必须先于 Use、Group 和路由注册。
func (module *Module) Setup(address string, options ServerOptions) error {
	if module == nil || module.Service() == nil {
		return errs.NewMessage(errs.CodeInvalidArgument, "ginmodule.Setup 只能在已绑定 Module.OnInit 中调用")
	}
	if err := validateAddress(address); err != nil {
		return err
	}
	if err := validateServerOptions(options); err != nil {
		return err
	}

	module.mu.Lock()
	defer module.mu.Unlock()
	if module.configured || module.started {
		return errs.NewMessage(errs.CodeInvalidArgument, "ginmodule.Setup 只能调用一次")
	}

	options.TrustedProxies = append([]string(nil), options.TrustedProxies...)
	if options.TLSConfig != nil {
		options.TLSConfig = options.TLSConfig.Clone()
	}
	engine := gin.New()
	engine.HandleMethodNotAllowed = true
	if err := engine.SetTrustedProxies(options.TrustedProxies); err != nil {
		return errs.Wrap(errs.CodeInvalidConfig, err)
	}

	module.address = address
	module.options = options
	module.engine = engine
	module.server = &http.Server{
		Addr:              address,
		Handler:           engine,
		ReadHeaderTimeout: options.ReadHeaderTimeout,
		ReadTimeout:       options.ReadTimeout,
		WriteTimeout:      options.WriteTimeout,
		IdleTimeout:       options.IdleTimeout,
		MaxHeaderBytes:    options.MaxHeaderBytes,
		TLSConfig:         options.TLSConfig,
		ErrorLog:          log.New(serverErrorWriter{module: module}, "", 0),
	}
	module.configured = true

	// 框架边界必须是私有 Engine 的第一个全局 Middleware。
	engine.Use(module.requestBoundary())
	return nil
}

// OnInit 验证直接作为 Module 使用时已经完成 Setup。
//
// 业务类型覆盖 OnInit 时应自行调用 Setup；该默认方法主要为错误装配提供明确诊断。
func (module *Module) OnInit() error {
	if module == nil {
		return errs.ErrInvalidArgument
	}
	module.mu.RLock()
	configured := module.configured
	module.mu.RUnlock()
	if !configured {
		return errs.NewMessage(errs.CodeInvalidConfig, "ginmodule 尚未调用 Setup")
	}
	return nil
}

// OnStart 同步绑定监听地址，成功后启动唯一 Serve goroutine。
func (module *Module) OnStart(ctx context.Context) error {
	if module == nil || ctx == nil {
		return errs.ErrInvalidArgument
	}
	module.mu.Lock()
	if !module.configured || module.server == nil || module.engine == nil {
		module.mu.Unlock()
		return errs.NewMessage(errs.CodeInvalidConfig, "ginmodule 尚未完成 Setup")
	}
	if module.started {
		module.mu.Unlock()
		return errs.NewMessage(errs.CodeInvalidArgument, "ginmodule 已经启动")
	}
	if module.stopped {
		module.mu.Unlock()
		return errs.NewMessage(errs.CodeInvalidArgument, "ginmodule 已经停止，不能重复启动")
	}
	address := module.address
	server := module.server
	tlsEnabled := module.options.TLSConfig != nil

	listener, err := (&net.ListenConfig{}).Listen(ctx, "tcp", address)
	if err != nil {
		module.mu.Unlock()
		return err
	}

	module.listener = listener
	module.started = true
	module.serveError = nil
	serveDone := make(chan struct{})
	module.serveDone = serveDone
	module.mu.Unlock()

	go func() {
		var serveErr error
		if tlsEnabled {
			serveErr = server.ServeTLS(listener, "", "")
		} else {
			serveErr = server.Serve(listener)
		}
		if errors.Is(serveErr, http.ErrServerClosed) {
			serveErr = nil
		}
		module.mu.Lock()
		module.serveError = serveErr
		module.listener = nil
		module.mu.Unlock()
		if serveErr != nil {
			module.Logger().Error(
				"ginmodule serve stopped unexpectedly",
				originlog.Err(serveErr),
			)
		}
		close(serveDone)
	}()
	return nil
}

// OnStop 停止新请求并在调用方预算内等待在途请求；超时后强制关闭连接。
func (module *Module) OnStop(ctx context.Context) error {
	if module == nil || ctx == nil {
		return errs.ErrInvalidArgument
	}
	module.mu.RLock()
	started := module.started
	server := module.server
	serveDone := module.serveDone
	module.mu.RUnlock()
	if !started || server == nil {
		return nil
	}

	shutdownErr := server.Shutdown(ctx)
	var closeErr error
	if shutdownErr != nil {
		closeErr = server.Close()
	}

	select {
	case <-serveDone:
	case <-ctx.Done():
		if closeErr == nil {
			closeErr = server.Close()
		}
	}

	module.mu.Lock()
	serveErr := module.serveError
	module.listener = nil
	module.started = false
	module.stopped = true
	module.mu.Unlock()
	return errors.Join(shutdownErr, closeErr, serveErr)
}

// Addr 返回启动后的真实监听地址；未启动或已经停止时返回 nil。
func (module *Module) Addr() net.Addr {
	if module == nil {
		return nil
	}
	module.mu.RLock()
	listener := module.listener
	module.mu.RUnlock()
	if listener == nil {
		return nil
	}
	return listener.Addr()
}

// Stats 返回当前固定统计快照。
func (module *Module) Stats() ServerStats {
	if module == nil {
		return ServerStats{}
	}
	return module.counters.snapshot()
}

func (module *Module) requestBoundary() gin.HandlerFunc {
	return func(ctx *gin.Context) {
		module.counters.total.Add(1)
		if !module.acquireRequest() {
			module.counters.rejected.Add(1)
			ctx.AbortWithStatus(http.StatusServiceUnavailable)
			return
		}

		requestCtx, cancel := context.WithTimeout(ctx.Request.Context(), module.options.RequestTimeout)
		ctx.Request = ctx.Request.WithContext(requestCtx)
		if ctx.Request.Body != nil {
			ctx.Request.Body = http.MaxBytesReader(
				ctx.Writer,
				ctx.Request.Body,
				module.options.MaxRequestBodySize,
			)
		}
		defer func() {
			cancel()
			module.counters.active.Add(-1)
			if errors.Is(context.Cause(requestCtx), context.DeadlineExceeded) {
				module.counters.timedOut.Add(1)
			}
			if recovered := recover(); recovered != nil {
				module.counters.panics.Add(1)
				ctx.Abort()
				if !ctx.Writer.Written() {
					ctx.Status(http.StatusInternalServerError)
				}
				module.Logger().ErrorStack(
					"ginmodule request handler panic",
					originlog.String("method", ctx.Request.Method),
					originlog.String("path", ctx.Request.URL.Path),
					originlog.String("panic", fmt.Sprint(recovered)),
					originlog.String("panic_stack", string(debug.Stack())),
				)
			}
		}()
		ctx.Next()
	}
}

func (module *Module) acquireRequest() bool {
	maximum := int64(module.options.MaxActiveRequests)
	for {
		current := module.counters.active.Load()
		if current >= maximum {
			return false
		}
		if module.counters.active.CompareAndSwap(current, current+1) {
			return true
		}
	}
}

func (module *Module) requireEngine() *gin.Engine {
	if module == nil {
		panic("ginmodule: nil Module")
	}
	module.mu.RLock()
	engine := module.engine
	sealed := module.started || module.stopped
	module.mu.RUnlock()
	if engine == nil {
		panic("ginmodule: 必须先在 OnInit 中调用 Setup")
	}
	if sealed {
		panic("ginmodule: 路由只能在 OnInit 中注册")
	}
	return engine
}

type serverErrorWriter struct {
	module *Module
}

func (writer serverErrorWriter) Write(data []byte) (int, error) {
	if writer.module != nil {
		message := strings.TrimSpace(string(data))
		if message != "" {
			writer.module.Logger().Error("ginmodule http server error", originlog.String("detail", message))
		}
	}
	return len(data), nil
}

var _ service.IModule = (*Module)(nil)
