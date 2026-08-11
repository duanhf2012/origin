// 本示例展示 Gin 业务 Module、普通/Safe Middleware 和同 Service HTTP 自调用。
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/duanhf2012/origin/v3/application"
	"github.com/duanhf2012/origin/v3/service"
	"github.com/duanhf2012/origin/v3/sysmodule/ginmodule"
	"github.com/duanhf2012/origin/v3/sysmodule/httpclient"
	"github.com/gin-gonic/gin"
)

// app 是当前示例唯一的 Application。
var app = application.New()

type player struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type createPlayerRequest struct {
	Name string `json:"name" binding:"required"`
}

// PlayerHTTPModule 集中保存 HTTP Server、Client、路由回调和 Service 串行业务状态。
type PlayerHTTPModule struct {
	ginmodule.Module
	client      *httpclient.Client
	players     map[string]player
	permissions map[string]bool
}

// OnInit 在启动装配 goroutine 中完成配置、Client 和路由注册；它不处理单次 HTTP 请求。
func (module *PlayerHTTPModule) OnInit() error {
	serverConfig := ginmodule.DefaultServerConfig()
	if err := module.GetServiceConfigStrict("http.server", &serverConfig); err != nil {
		return err
	}
	serverOptions, err := serverConfig.Options()
	if err != nil {
		return err
	}
	if err := module.Setup(serverConfig.Address, serverOptions); err != nil {
		return err
	}

	// HTTP Client 没有 YAML 和 Module 生命周期；创建一次并在并发请求之间复用。
	client, err := httpclient.New(httpclient.DefaultOptions())
	if err != nil {
		return err
	}
	module.client = client
	module.players = make(map[string]player)
	module.permissions = map[string]bool{"demo": true}

	// health 和普通 GET Handler 都在 net/http 请求 goroutine 执行，不直接访问 players。
	module.GET("/health", module.health)
	// authenticateToken 在请求 goroutine 执行，先过滤无效请求，避免占用 Service 队列。
	api := module.Group("/api", authenticateToken())
	// authorizePlayer 及本分组 Handler 都在所属 Service 工作协程串行执行。
	players := api.SafeGroup("/players", module.authorizePlayer)
	players.POST("/:id", module.createPlayer)
	players.GET("/:id", module.getPlayer)
	return nil
}

// OnStop 先停止 Gin Server，再关闭 Client 的空闲连接；活动请求由各自 Context 收敛。
func (module *PlayerHTTPModule) OnStop(ctx context.Context) error {
	err := module.Module.OnStop(ctx)
	module.client.CloseIdleConnections()
	return err
}

// health 在 HTTP 请求 goroutine 执行，只返回无共享状态的健康结果。
func (module *PlayerHTTPModule) health(ctx *gin.Context) {
	ctx.JSON(http.StatusOK, gin.H{"status": "ok"})
}

// authenticateToken 返回普通 Gin Middleware；参数 ctx 只在当前 HTTP 请求 goroutine 和请求链内有效。
func authenticateToken() gin.HandlerFunc {
	return func(ctx *gin.Context) {
		if ctx.GetHeader("Authorization") != "Bearer demo" {
			ctx.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "invalid token"})
			return
		}
		// 字符串是不可变请求快照值，SafeContext 可以安全读取。
		ctx.Set("principal", "demo")
		ctx.Next()
	}
}

// authorizePlayer 的 ctx 参数在所属 Service 工作协程执行，可以读取 permissions 串行状态。
func (module *PlayerHTTPModule) authorizePlayer(ctx *ginmodule.SafeContext) {
	principal := ctx.MustGet("principal").(string)
	if !module.permissions[principal] {
		ctx.AbortWithStatusJSON(http.StatusForbidden, map[string]string{"error": "forbidden"})
		return
	}
	ctx.Next()
}

// createPlayer 的 ctx 与 request 参数都只在当前 Service 工作协程和回调期间使用。
func (module *PlayerHTTPModule) createPlayer(ctx *ginmodule.SafeContext) {
	var request createPlayerRequest
	if err := ctx.ShouldBindJSON(&request); err != nil {
		ctx.JSON(http.StatusBadRequest, map[string]string{"error": "invalid request"})
		return
	}
	created := player{ID: ctx.Param("id"), Name: request.Name}
	module.players[created.ID] = created
	// JSON 在 Service 工作协程立即编码到私有缓冲区，之后由请求 goroutine 提交。
	ctx.JSON(http.StatusCreated, created)
}

// getPlayer 在 Service 工作协程读取 players；SafeContext 不得保存或交给新 goroutine。
func (module *PlayerHTTPModule) getPlayer(ctx *ginmodule.SafeContext) {
	current, exists := module.players[ctx.Param("id")]
	if !exists {
		ctx.JSON(http.StatusNotFound, map[string]string{"error": "player not found"})
		return
	}
	ctx.JSON(http.StatusOK, current)
}

// callSelf 从 Service Timer Task 发起 HTTP 自调用。
func (module *PlayerHTTPModule) callSelf(ctx context.Context) {
	payload, err := json.Marshal(createPlayerRequest{Name: "Origin"})
	if err != nil {
		module.Logger().Error(err.Error())
		return
	}
	var response httpclient.Response
	// Await 的 fn 仍运行在原 Task goroutine，但调用前已经释放 Service 执行权。
	err = module.Await(ctx, func(waitCtx context.Context) error {
		request, requestErr := http.NewRequestWithContext(
			waitCtx,
			http.MethodPost,
			"http://"+module.Addr().String()+"/api/players/42",
			bytes.NewReader(payload),
		)
		if requestErr != nil {
			return requestErr
		}
		request.Header.Set("Authorization", "Bearer demo")
		request.Header.Set("Content-Type", "application/json")
		// DoBytes 在当前 Await 等待函数的 goroutine 中执行；网络连接由标准 Transport 管理。
		response, requestErr = module.client.DoBytes(request)
		return requestErr
	})
	if err != nil {
		module.Logger().Error("HTTP self-call failed: " + err.Error())
		return
	}
	// Await 返回后当前 Task 已恢复 Service 执行权，可以再次读取 players。
	created := module.players["42"]
	module.Logger().Info(fmt.Sprintf(
		"HTTP self-call status=%d body=%s service_player=%s",
		response.StatusCode,
		response.Body,
		created.Name,
	))
}

// HTTPService 只负责装配业务 Module，并在 Service 进入 Running 后触发一次自调用。
type HTTPService struct {
	service.Service
	module *PlayerHTTPModule
}

func (target *HTTPService) OnInit() error {
	target.module = &PlayerHTTPModule{}
	return target.AddModule(target.module)
}

func (target *HTTPService) OnStart(context.Context) error {
	// Timer 回调运行在 Service 工作协程；100ms 后 Gin Listener 已完成启动。
	if id := target.AfterFunc(100*time.Millisecond, func(ctx context.Context, _ service.TimerID) {
		target.module.callSelf(ctx)
	}); id == 0 {
		return fmt.Errorf("schedule HTTP self-call failed")
	}
	return nil
}

func init() { app.Setup(&HTTPService{}) }

func main() { app.Start() }
