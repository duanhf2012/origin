# Gin HTTP Module 使用指南

Gin HTTP Module 用于业务 HTTP API。业务类型匿名嵌入 `ginmodule.Module`，路由、鉴权和业务 Handler
集中写在同一个 Module；使用者不需要持有或操作底层 Gin Engine。

## 1. 先选择普通路由还是 Safe 路由

- `GET`、`POST` 等普通路由运行在 `net/http` 请求 goroutine。适合无 Service 共享状态的处理、流式响应、
  文件上传、反向代理，或调用已经并发安全的组件；
- `SafeGET`、`SafePOST` 等 Safe 路由由框架投递到所属 Service 工作协程。适合直接读写当前 Service 的
  串行业务状态；
- Token、签名、CORS、请求级限流等不依赖 Service 状态的检查，优先使用普通 Gin Middleware；
- 必须读取玩家表等 Service 状态的授权，使用 Safe Middleware 或 `SafeGroup`；
- Safe Handler 等待数据库、RPC 或 HTTP I/O 时仍需调用 `Await`，不要阻塞 Service 工作协程。

## 2. 在业务 Module 中完成装配

```go
type PlayerHTTPModule struct {
    ginmodule.Module
    players map[string]Player
}

func (module *PlayerHTTPModule) OnInit() error {
    cfg := ginmodule.DefaultServerConfig()
    if err := module.GetServiceConfigStrict("http.server", &cfg); err != nil {
        return err
    }
    options, err := cfg.Options()
    if err != nil {
        return err
    }
    if err := module.Setup(cfg.Address, options); err != nil {
        return err
    }

    // authenticateToken 在 HTTP 请求 goroutine 执行，不直接访问 Service 串行状态。
    api := module.Group("/api", authenticateToken())
    api.GET("/health", module.health)

    // authorizePlayer 和下面的 Safe Handler 在所属 Service 工作协程串行执行。
    players := api.SafeGroup("/players", module.authorizePlayer)
    players.POST("", module.createPlayer)
    players.GET("/:id", module.getPlayer)
    return nil
}
```

`Setup` 只能在 `OnInit` 调用一次。`Use`、`Group` 和路由注册必须位于 `Setup` 之后、`OnInit` 返回之前。
业务 Module 通常只覆盖 `OnInit`；嵌入 Module 的 `OnStart`、`OnStop` 自动完成监听和优雅停止。

## 3. 函数、回调参数与执行协程

这里的“执行协程”指并发访问域，不承诺固定 goroutine ID。`path`、`method`、状态码等普通参数不会被
“执行”；表中说明它们何时读取。真正会延后执行的是 Handler、Middleware、TLS 回调和错误映射器等
函数参数。

| 函数或函数族 | 调用位置 | 函数参数实际执行位置 | 参数与数据规则 | 可直接访问 Service 串行状态 |
| --- | --- | --- | --- | --- |
| `Setup(address, options)` | `OnInit` 的启动装配 goroutine | 不立即执行 `SafeErrorMapper`；TLS 回调以后由 `net/http` 握手 goroutine 调用 | `address` 同步读取；代理列表和 `TLSConfig` 被克隆；Mapper 必须并发安全 | 仅装配，不应处理业务状态 |
| `Use(middleware...)` | `OnInit` 的启动装配 goroutine | 每个 `gin.HandlerFunc` 在对应 HTTP 请求 goroutine 执行 | `*gin.Context` 只在当前请求链有效 | 否 |
| `Group(path, middleware...)`、`RouterGroup.Group` | `OnInit` 的启动装配 goroutine | `middleware` 在 HTTP 请求 goroutine 执行 | `path` 注册时读取；Middleware 前后逻辑都留在请求 goroutine | 否 |
| `Handle/GET/POST/PUT/PATCH/DELETE/HEAD/OPTIONS(path, handler, middleware...)` | `OnInit` 的启动装配 goroutine | `middleware` 与 `handler` 都在 HTTP 请求 goroutine 执行 | 先按声明顺序执行 Middleware，再执行 Handler；`*gin.Context` 不得交给其他 goroutine | 否 |
| `NoRoute/NoMethod(handler, middleware...)` | `OnInit` 的启动装配 goroutine | `middleware` 与 `handler` 都在 HTTP 请求 goroutine 执行 | 与普通路由相同 | 否 |
| `SafeGroup(path, middleware...)`、`RouterGroup.SafeGroup` | `OnInit` 的启动装配 goroutine | `SafeMiddlewareFunc` 在所属 Service 工作协程执行 | 普通父 Group 的 Gin Middleware 仍在请求 goroutine；不会被 SafeGroup 改变 | 是 |
| `SafeRouterGroup.Group(path, middleware...)` | `OnInit` 的启动装配 goroutine | 继承和新增的 Safe Middleware 都在所属 Service 工作协程执行 | 分组 Middleware 先于单路由 Middleware | 是 |
| `SafeHandle/SafeGET/SafePOST/SafePUT/SafePATCH/SafeDELETE(path, handler, middleware...)` | `OnInit` 的启动装配 goroutine | `middleware` 与最终 `handler` 在同一个 Service Task、同一个 Service 工作协程串行执行 | 传入的是请求快照 `*SafeContext`，不是原始 `*gin.Context` | 是 |
| `SafeContext` 的读取、绑定、响应和 `Next/Abort` 方法 | Safe Middleware 或 Safe Handler 内调用 | 当前 Service 工作协程立即执行 | `JSON(value)` 当场编码；`Data(data)` 当场复制；所有方法只在当前 Safe 链返回前有效 | 是 |
| `SafeErrorMapper(error)` | 框架内部延后调用 | HTTP 请求 goroutine | 可能并发调用；只能使用不可变或并发安全数据，不能读取 Service 串行状态 | 否 |
| `Addr()`、`Stats()` | 启动后任意业务或运维调用处 | 没有回调参数 | 返回并发安全快照；`Addr` 在未启动或已停止时返回 `nil` | 不涉及业务状态 |
| `OnStart(ctx)`、`OnStop(ctx)` | 由 Origin 生命周期调用 | 当前生命周期调用 goroutine；`Serve` 另有一个内部 goroutine | 使用者不应自行并发调用；停止截止后会强制关闭连接 | 不应放业务处理 |

最容易误用的两点：普通 `Group` 下面注册 `SafePOST` 时，父 Group Middleware 仍在请求 goroutine；
`SafeErrorMapper` 也在请求 goroutine，因此不能借它访问 Service 数据。

## 4. 鉴权分层

```go
// 请求 goroutine：尽早验证 Token，失败请求不会进入 Service 队列。
api := module.Group("/api", authenticateToken())

// Service 工作协程：读取当前 Service 的权限表。
players := api.SafeGroup("/players", module.authorizePlayer)
players.POST("", module.createPlayer)
```

普通 Middleware 可以用 `ctx.Set("principal", immutablePrincipal)` 保存请求独占或不可变的鉴权结果。
框架会在投递前复制 Keys，Safe Handler 通过 `ctx.Get`/`MustGet` 读取。不要把随后仍会修改的 map、slice
或指针放入 Keys。

```go
func (module *PlayerHTTPModule) authorizePlayer(ctx *ginmodule.SafeContext) {
    principal := ctx.MustGet("principal").(Principal)
    if !module.permissions[principal.ID].CanCreatePlayer {
        ctx.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "forbidden"})
        return
    }
    ctx.Next()
}
```

Safe Middleware 成功时必须调用 `Next`；失败时写入响应并 `Abort`。最终 Safe Handler 不调用 `Next`。

## 5. SafeContext 的所有权

- 原始 `*gin.Context`、ResponseWriter 和 Body Reader 始终留在请求 goroutine；
- `SafeContext.Request()` 是当前 Safe 回调独占的请求克隆；`Context()` 同时继承 HTTP 取消/Deadline 和
  Service 执行令牌；
- Params、URL、Header、Keys、客户端 IP 和 Body 在投递前冻结；`GetRawData` 返回 Body 副本；
- `JSON`、`String`、`Data` 只写 Service 侧私有缓冲区，Safe 链结束后由请求 goroutine 提交；
- 一个 Safe 请求只能最终渲染一次。`Data` 会复制输入切片，回调返回后可以安全复用调用方缓冲区；
- 不要保存 `SafeContext`，也不要把它交给新 goroutine。Safe 路由不用于 SSE、流式上传、Hijack、反向代理
  或大文件下载，这些场景使用普通路由。

## 6. Server 配置起始值

```yaml
services:
  PlayerService:
    http:
      server:
        # 监听地址；生产环境按真实暴露范围配置主机部分。
        address: "0.0.0.0:19093"
        # 单请求 Context 总预算；Safe 排队和业务处理都包含在内。
        request_timeout: 15s
        # 读完整 Header 的最长时间，用于限制慢速请求头攻击。
        read_header_timeout: 5s
        # 读完整请求（含 Body）的最长时间。
        read_timeout: 15s
        # 写响应的最长时间；必须大于 request_timeout，给框架提交响应留出余量。
        write_timeout: 20s
        # HTTP Keep-Alive 空闲连接保留时间，不是业务心跳。
        idle_timeout: 60s
        # 请求 Header 与 Safe 响应 Header 的字节上限。
        max_header_bytes: 1M
        # 单请求 Body 上限；按真实 JSON/PB 接口收紧。
        max_request_body_size: 4M
        # Safe Handler 私有响应 Body 上限；流式或大响应应改用普通路由。
        max_safe_response_body_size: 4M
        # 当前 Module 同时处理的在途请求硬上限，超限立即返回 503。
        max_active_requests: 1024
        # 允许提供 X-Forwarded-For 等客户端地址的代理 IP/CIDR；空列表表示不信任代理。
        trusted_proxies: []
```

未准备调整的字段可从 YAML 删除，由 `DefaultServerConfig` 补齐。第一次接入通常只需要确认监听地址、
请求/响应大小、请求总预算和并发上限。只有部署在受控反向代理后才填写 `trusted_proxies`，不要配置成
信任任意来源。

TLS 证书、动态证书回调和 `SafeErrorMapper` 是运行期对象，不进入 YAML；先调用 `cfg.Options()`，再通过
代码设置 `options.TLSConfig` 或 `options.SafeErrorMapper`，最后调用 `Setup`。

## 7. 状态码与故障边界

- 活动请求超过上限或 Service 队列拒绝 Safe Task：`503`；
- Safe 请求达到 `request_timeout`：`504`；
- 请求 Body 超过上限：Safe 路由返回 `413`；普通路由读取 Body 时得到标准库错误，由业务决定响应；
- 未处理 panic、非法 Safe 响应或默认未识别错误：`500`；
- 客户端主动取消后不再尝试写响应；已经开始的 Safe Task 只能完成私有结果，不能晚写 ResponseWriter。

`Stats()` 提供当前活动请求、累计请求、拒绝、超时和 panic 计数。它们用于低成本运行观测，不替代按
路由、状态码和延迟维度建设的业务指标。
