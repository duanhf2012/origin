# Origin Gin 与 HTTP Client 核心设计

> 目标版本：v3.2  
> 状态：待公开外观确认后实施  
> 能力依据：[Origin Gin 与 HTTP Client 能力分析](../proposals/Origin%20Gin与HTTP%20Client能力分析.md)

## 1. 包、组合与所有权

```text
业务 Service
└── 业务 HTTP Module（匿名嵌入 ginmodule.Module）
    ├── 私有 gin.Engine / http.Server / Listener
    ├── 普通与 Safe 路由及业务 Handler
    └── httpclient.Client（普通字段，跨请求复用）
```

- `sysmodule/ginmodule` 使用 Gin，但不加入 `sysmodule/network`；
- `ginmodule.Module` 是 HTTP Server 的唯一公开入口，同时拥有 Service Module 生命周期、路由和运行资源；
- 使用者不需要先取得 `Server` 或 `Engine`，配置、路由、分组、中间件、监听地址和统计都从当前业务
  HTTP Module 调用；
- `sysmodule/httpclient` 只依赖 Go 标准库，不依赖 Gin，也不嵌入 `service.Module`；
- 一个 `httpclient.Client` 应按调用目标或公共传输策略长期复用，不能按请求创建。

推荐的业务结构：

```go
type PlayerHTTPModule struct {
    ginmodule.Module

    // client 按调用目标长期复用；它不是子 Module，也不读取 Service YAML。
    client *httpclient.Client
    // players 只在 Service 工作协程访问。
    players map[int64]Player
}

func (module *PlayerHTTPModule) OnInit() error {
    config := ginmodule.DefaultServerConfig()
    // 从完整默认值开始严格覆盖；配置字段拼写错误会阻止 Service 启动。
    if err := module.GetServiceConfigStrict("http.server", &config); err != nil {
        return err
    }
    options, err := config.Options()
    if err != nil {
        return err
    }
    // Setup 只允许在当前 Module.OnInit 中调用一次，并且必须先于路由注册。
    if err := module.Setup(config.Address, options); err != nil {
        return err
    }

    module.GET("/health", module.health)

    // Group Middleware 在请求 goroutine 完成统一鉴权；失败时可直接 Abort，成功结果通过 Keys 快照传入
    // SafeContext。
    api := module.Group("/api", authenticate())
    api.SafePOST("/players", module.createPlayer)
    return nil
}
```

业务 Module 通常只覆盖 `OnInit`；被提升的 `ginmodule.Module.OnStart` 和 `OnStop` 负责监听与停止。若业务
确需覆盖 `OnStart` 或 `OnStop`，必须分别调用嵌入 Module 的同名方法，测试正常、失败和取消顺序。首批不为
这一罕见场景再增加一套生命周期 Hook 抽象。

## 2. Gin Module 外观

### 2.1 配置与运行期选项

```go
package ginmodule

type ServerConfig struct {
    Address                    string
    RequestTimeout             config.Duration
    ReadHeaderTimeout          config.Duration
    ReadTimeout                config.Duration
    WriteTimeout               config.Duration
    IdleTimeout                config.Duration
    MaxHeaderBytes             config.ByteSize
    MaxRequestBodySize         config.ByteSize
    MaxSafeResponseBodySize    config.ByteSize
    MaxActiveRequests          int
    TrustedProxies             []string
}

func DefaultServerConfig() ServerConfig
func (ServerConfig) Options() (ServerOptions, error)

type ServerOptions struct {
    RequestTimeout             time.Duration
    ReadHeaderTimeout          time.Duration
    ReadTimeout                time.Duration
    WriteTimeout               time.Duration
    IdleTimeout                time.Duration
    MaxHeaderBytes             int
    MaxRequestBodySize         int64
    MaxSafeResponseBodySize    int64
    MaxActiveRequests          int
    TrustedProxies             []string
    TLSConfig                  *tls.Config
    SafeErrorMapper            SafeErrorMapper
}

func DefaultServerOptions() ServerOptions
```

`ServerConfig` 只保存可序列化且属于 Server 的字段。TLS 证书、动态证书回调等安全对象在
`Options()` 后通过代码设置。默认配置建议如下：

| 字段 | 默认值 | 说明 |
| --- | ---: | --- |
| `address` | `0.0.0.0:19093` | 示例起始值，生产环境按暴露范围修改 |
| `request_timeout` | `15s` | 请求 Context 总预算；Handler 必须向 RPC、数据库和下游 I/O 传递 Context |
| `read_header_timeout` | `5s` | 防止慢速 Header 占用连接 |
| `read_timeout` | `15s` | 包含读取请求 Body；大上传应按实际业务调整 |
| `write_timeout` | `20s` | 比请求预算稍长，为取消收敛和错误响应保留时间 |
| `idle_timeout` | `60s` | Keep-Alive 空闲连接保留时间 |
| `max_header_bytes` | `1MiB` | 请求 Header 上限，也作为 Safe Handler 缓冲响应 Header 上限 |
| `max_request_body_size` | `4MiB` | 普通 JSON/PB API 的安全起点，不代表上传接口最优值 |
| `max_safe_response_body_size` | `4MiB` | Safe Handler 缓冲响应 Body 上限；普通流式 Handler 不适用 |
| `max_active_requests` | `1024` | 当前 Server 的在途请求硬上限 |
| `trusted_proxies` | `[]` | 默认不信任任何转发代理 |

所有超时和容量必须大于零，并校验 `write_timeout > request_timeout`。首批不允许用零值隐式关闭边界；
确有 SSE、超大上传等需求时，应先补充对应测试和设计，而不是把普通 Server 默认值整体放开。

`request_timeout` 只通过首个框架 Middleware 为 `Request.Context()` 建立 Deadline，不把 Handler 放入新的
goroutine，也不承诺强制中断忽略 Context 的业务代码。Handler 应把 `ctx.Request.Context()` 传给生成的
`CallXxx`、数据库和下游客户端；业务错误响应格式仍由项目定义。

### 2.2 Module、路由与分组

```go
type Module struct {
    service.Module
    // unexported runtime fields
}

func (module *Module) Setup(address string, options ServerOptions) error
func (module *Module) Use(middleware ...gin.HandlerFunc) gin.IRoutes
func (module *Module) Group(path string, middleware ...gin.HandlerFunc) *RouterGroup
func (module *Module) Handle(method, path string, handler gin.HandlerFunc, middleware ...gin.HandlerFunc) gin.IRoutes
func (module *Module) GET(path string, handler gin.HandlerFunc, middleware ...gin.HandlerFunc) gin.IRoutes
func (module *Module) POST(path string, handler gin.HandlerFunc, middleware ...gin.HandlerFunc) gin.IRoutes
func (module *Module) PUT(path string, handler gin.HandlerFunc, middleware ...gin.HandlerFunc) gin.IRoutes
func (module *Module) PATCH(path string, handler gin.HandlerFunc, middleware ...gin.HandlerFunc) gin.IRoutes
func (module *Module) DELETE(path string, handler gin.HandlerFunc, middleware ...gin.HandlerFunc) gin.IRoutes
func (module *Module) HEAD(path string, handler gin.HandlerFunc, middleware ...gin.HandlerFunc) gin.IRoutes
func (module *Module) OPTIONS(path string, handler gin.HandlerFunc, middleware ...gin.HandlerFunc) gin.IRoutes
func (module *Module) NoRoute(handler gin.HandlerFunc, middleware ...gin.HandlerFunc)
func (module *Module) NoMethod(handler gin.HandlerFunc, middleware ...gin.HandlerFunc)
func (module *Module) Addr() net.Addr
func (module *Module) Stats() ServerStats

type RouterGroup struct {
    // unexported module and Gin group references
}

func (group *RouterGroup) Use(middleware ...gin.HandlerFunc)
func (group *RouterGroup) Group(path string, middleware ...gin.HandlerFunc) *RouterGroup
func (group *RouterGroup) SafeGroup(path string, middleware ...SafeMiddlewareFunc) *SafeRouterGroup
// RouterGroup 提供与 Module 相同的普通路由和 SafeGET/SafePOST 方法族。

type SafeRouterGroup struct {
    // unexported request group and Service-side middleware chain
}

func (group *SafeRouterGroup) Group(path string, middleware ...SafeMiddlewareFunc) *SafeRouterGroup
func (group *SafeRouterGroup) GET(path string, handler SafeHandlerFunc, middleware ...SafeMiddlewareFunc) gin.IRoutes
func (group *SafeRouterGroup) POST(path string, handler SafeHandlerFunc, middleware ...SafeMiddlewareFunc) gin.IRoutes
// SafeRouterGroup 的其他 HTTP Method 与 POST 具有相同参数顺序，且全部在 Service 工作协程执行。

type ServerStats struct {
    ActiveRequests   int64
    TotalRequests    uint64
    RejectedRequests uint64
    TimedOutRequests uint64
    PanicTotal       uint64
}
```

`Setup` 创建私有 `gin.New()` Engine，并先安装框架拥有的全局安全边界。它只能在业务 Module 的
`OnInit` 调用一次；`Use`、`Group` 和路由注册必须位于 `Setup` 之后、`OnInit` 返回之前。普通和 Safe 方法
最终都委托内部 `handle`/`safeHandle`，每个 HTTP Method 仅保留薄包装，不重复生命周期或调度逻辑。

普通与 Safe 路由统一采用 `METHOD(path, handler, middleware...)`：第二个参数始终是唯一最终业务回调，
后续 Middleware 全部可省略，内部按声明顺序放到 Handler 之前执行。该形式与 Echo 等成熟框架一致，也避免
把可变参数中的最后一项默认为业务 Handler。框架不增加 `AuthPOST`、`SafeAuthPOST` 等方法；鉴权只是
Middleware 的一种用途，按 Method 乘倍增加接口会产生大量重复外观。

例如 `POST(path, handler, auth, audit)` 和 `SafePOST(path, handler, auth, audit)` 都按
`auth before → audit before → handler → audit after → auth after` 执行；不传可选参数时直接执行 Handler。

首批不公开 `Engine()`，也不接受外部 Engine，避免使用者绕过 Module 外观或破坏框架 Middleware 顺序。
`Use`、`Group`、常用 HTTP Method、`NoRoute` 和 `NoMethod` 已覆盖普通 JSON/PB API。模板渲染、静态目录或
自定义 Validator 等能力只有出现真实用例后，才按最小接口补充；不预先暴露整个 Engine 作为万能逃生口。

框架边界只负责：

- 活动请求准入，超限立即返回 `503`；
- 使用 `http.MaxBytesReader` 限制请求 Body；
- 为 Request Context 建立统一截止时间，并在 Handler 返回后释放 Timer；
- 捕获未处理 panic，记录 Origin 结构化日志，并在尚未提交响应时返回 `500`；
- 维护低基数固定统计。

框架不默认记录每个访问日志，不读取业务 Header、Query 或 Body 写日志，也不替业务定义 JSON 错误格式。

### 2.3 生命周期

1. 业务 `OnInit` 调用 `Setup`，校验并冻结配置、代理列表和私有 Engine；`OnInit` 返回后禁止继续注册路由；
2. `OnStart` 同步执行 `net.Listen`，成功后才启动 Serve goroutine；绑定失败直接使 Service 启动失败；
3. TLS 使用构造期克隆后的 `tls.Config` 和标准 `ServeTLS` 路径，保留 Go 的 HTTP/2 自动协商；
4. `OnStop` 先停止新连接，再用传入 Context 执行 `http.Server.Shutdown`；
5. Context 到期时调用 `Close`，并等待 Serve goroutine 退出，不遗留 Listener；
6. `Addr()` 返回实际监听地址，未启动或已停止返回 `nil`。

Serve 在运行中意外退出时记录根因和结构化错误。首批不为 Module 新增自动重启；Server 失败应由进程级
健康检查和既有生命周期策略处理。

### 2.4 两种 Handler 模式

普通 Gin Handler 运行在 `net/http` 请求 goroutine，不在 Service Scheduler 内。它适合流式响应、文件
上传、反向代理、无共享状态 I/O，或者通过生成的 `CallXxx` 调用已有 Service RPC：

```go
func (module *PlayerHTTPModule) getPlayer(ctx *gin.Context) {
    name, err := module.playerRPC.CallGetPlayer(ctx.Request.Context(), playerID)
    if err != nil {
        // 由项目自己的 HTTP 错误映射统一响应。
        return
    }
    ctx.JSON(http.StatusOK, gin.H{"name": name})
}
```

需要直接访问当前 Service 业务数据时使用 `SafePOST` 等 Safe 路由。框架自动完成投递，使用者无需编写
Dispatch、RPC、Task 闭包、等待 Channel 或响应提交代码。

### 2.5 Safe Handler 与 SafeContext

```go
type Response struct {
    StatusCode int
    Header     http.Header
    Body       []byte
}

type SafeHandlerFunc func(*SafeContext)
type SafeMiddlewareFunc func(*SafeContext)
type SafeErrorMapper func(error) Response

func (module *Module) SafeHandle(method, path string, handler SafeHandlerFunc, middleware ...SafeMiddlewareFunc) gin.IRoutes
func (module *Module) SafeGET(path string, handler SafeHandlerFunc, middleware ...SafeMiddlewareFunc) gin.IRoutes
func (module *Module) SafePOST(path string, handler SafeHandlerFunc, middleware ...SafeMiddlewareFunc) gin.IRoutes
func (module *Module) SafePUT(path string, handler SafeHandlerFunc, middleware ...SafeMiddlewareFunc) gin.IRoutes
func (module *Module) SafePATCH(path string, handler SafeHandlerFunc, middleware ...SafeMiddlewareFunc) gin.IRoutes
func (module *Module) SafeDELETE(path string, handler SafeHandlerFunc, middleware ...SafeMiddlewareFunc) gin.IRoutes
func (module *Module) SafeGroup(path string, middleware ...SafeMiddlewareFunc) *SafeRouterGroup

func (ctx *SafeContext) Context() context.Context
func (ctx *SafeContext) Request() *http.Request
func (ctx *SafeContext) Param(key string) string
func (ctx *SafeContext) Query(key string) string
func (ctx *SafeContext) GetQuery(key string) (string, bool)
func (ctx *SafeContext) GetHeader(key string) string
func (ctx *SafeContext) ClientIP() string
func (ctx *SafeContext) FullPath() string
func (ctx *SafeContext) Get(key string) (any, bool)
func (ctx *SafeContext) MustGet(key string) any
func (ctx *SafeContext) GetRawData() ([]byte, error)
func (ctx *SafeContext) ShouldBindJSON(value any) error
func (ctx *SafeContext) Header(key, value string)
func (ctx *SafeContext) Status(code int)
func (ctx *SafeContext) JSON(code int, value any)
func (ctx *SafeContext) String(code int, format string, values ...any)
func (ctx *SafeContext) Data(code int, contentType string, data []byte)
func (ctx *SafeContext) Next()
func (ctx *SafeContext) Abort()
func (ctx *SafeContext) AbortWithStatusJSON(code int, value any)
func (ctx *SafeContext) IsAborted() bool
```

推荐使用方式：

```go
// createPlayer 由 SafePOST 自动安排到当前 Service 工作协程。
func (module *PlayerHTTPModule) createPlayer(ctx *ginmodule.SafeContext) {
    var request CreatePlayerRequest
    if err := ctx.ShouldBindJSON(&request); err != nil {
        ctx.JSON(http.StatusBadRequest, gin.H{"error": "invalid request"})
        return
    }

    // 当前回调拥有 Service 执行权，可以直接安全读写业务状态。
    player := Player{ID: request.ID, Name: request.Name}
    module.players[player.ID] = player

    // JSON 在释放 Service 执行权前编码进私有缓冲区，不需要 Done 或手动通知请求 goroutine。
    ctx.JSON(http.StatusCreated, player)
}
```

#### 鉴权与自定义处理器

不新增 `IGinProcessor`。鉴权采用主流 Web 框架的 Middleware 链，并由 Module 外观提供三个作用域：

```go
module.Use(requestIDMiddleware())                              // 请求协程：全局
private := module.Group("/api", authenticateToken())         // 请求协程：分组鉴权
private.POST("/upload", module.upload, uploadPermission())    // 请求协程：单路由鉴权，可省略
private.SafePOST("/players", module.createPlayer)             // Service 协程：无额外 Safe 鉴权
```

- Token、签名、mTLS、CORS、限流等不读取 Service 串行状态的逻辑写成 `gin.HandlerFunc`；失败时调用
  `AbortWithStatusJSON`，成功时通过 `ctx.Set("principal", Principal{...})` 放入只读的请求期结果；
- Safe 适配器在 Middleware 前置逻辑完成后浅复制 `Context.Keys`，`SafeContext.Get` 可在 Service 中读取。
  放入 Keys 的值必须是本请求独占或不可变值，不能由 Middleware 的后置逻辑并发修改；
- 如果授权判断必须读取玩家在线表等 Service 状态，把它作为可选 `SafeMiddlewareFunc` 放在最终 Handler
  后面；框架会先执行 Middleware，成功时由它调用 `Next()` 进入下一层，失败时调用
  `AbortWithStatusJSON`。所有 Safe Middleware 和最终 Handler 都在同一个 Service Task 中执行：

```go
private.SafePOST("/players", module.createPlayer, module.authorizePlayer)

func (module *PlayerHTTPModule) authorizePlayer(ctx *ginmodule.SafeContext) {
    principal := ctx.MustGet("principal").(Principal)
    if !module.permissions[principal.ID].CanCreatePlayer {
        ctx.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "forbidden"})
        return
    }
    ctx.Next()
}
```

同一套 Service 状态授权被多个 Safe 路由复用时，使用 `SafeGroup`，不在每个路由重复传入：

```go
// authenticateToken 仍在请求 goroutine 执行，先拒绝无效 Token，避免占用 Service 队列。
api := module.Group("/api", authenticateToken())

// authorizePlayer 及 players.POST 的最终 Handler 都在 Service 工作协程执行。
players := api.SafeGroup("/players", module.authorizePlayer)
players.POST("", module.createPlayer)
players.GET("/:id", module.getPlayer)
```

`Group` 的 Middleware 永远运行在 Gin/HTTP 请求 goroutine；即使其中注册 `SafePOST`，也只是在完成请求级
Middleware 后，由内部适配器投递 Safe 回调。`SafeGroup` 返回独立 `SafeRouterGroup`，其 Middleware 与
GET/POST 等最终 Handler 永远运行在 Service 工作协程。两者可以像上例嵌套，但不会隐式改变彼此的执行
位置。

完整执行顺序固定为：框架请求边界 → Module/Group Middleware 前置逻辑（请求 goroutine）→ 请求快照与
投递 → SafeGroup/路由 Safe Middleware 前置逻辑（Service 工作协程）→ Safe Handler → Safe Middleware
后置逻辑 → 冻结并提交响应 → Module/Group Middleware 后置逻辑（请求 goroutine）。

这同时覆盖“插入自定义鉴权处理器”和“复用 Gin 生态”两个需求：常规鉴权不必重新发明接口，只有确实依赖
Service 状态的授权才进入 Safe 链。首批不提供内置 JWT、Session 或 RBAC；这些策略与项目密钥、声明和
存储强相关，强行统一会扩大安全责任和配置面。

固定执行契约：

1. 原始 `*gin.Context`、ResponseWriter 和 Body Reader 永不离开请求 goroutine；Safe 适配器只投递独立
   请求快照，`SafeContext.Request()` 返回的也是绑定合并 Context 的私有克隆；
2. 请求 Body 在投递前读取并受 `MaxRequestBodySize` 限制；Params、URL、Header、Trailer、Keys 和 Body
   均在请求 goroutine 冻结。首批只做普通 JSON/PB API，不把 Safe Handler 用于流式上传、SSE、Hijack、
   反向代理或大文件下载；这些场景使用普通 Handler；
3. Safe 链被投递到所属 Service 的有界 FIFO，合并 Context 保留 Service Task 执行令牌，并继承 HTTP
   请求的 Value、Deadline 和取消；
4. Safe Middleware 按注册顺序形成洋葱链；必须调用 `Next` 才会进入下一层或最终 Handler，`Abort` 会阻止
   后续执行。最终 Handler 不需要也不应调用 `Next`；
5. Safe Handler 可以直接访问所属业务 Module 数据和调用同步 Event；等待数据库、HTTP 或其他异步 I/O
   时仍须使用 `Await`，避免阻塞 Service；
6. `JSON`、`String` 和 `Data` 只写私有缓冲响应，不触碰真实 ResponseWriter；回调返回即自动完成，不提供
   v2 的 `Done`/`JSONAndDone` 方法。Header 和 Status 可在渲染前调整；一次 Safe 请求只允许一次最终
   `JSON`/`String`/`Data` 渲染，多次渲染属于 Handler 契约错误；没有显式渲染时返回 `200` 空响应；
7. Safe 链返回后，框架在释放 Service 执行权前验证并冻结 Header/Body。只允许最终状态码，响应 Header
   总量受 `MaxHeaderBytes` 限制，并拒绝 `Connection`、`Keep-Alive`、`Proxy-Authenticate`、
   `Proxy-Authorization`、`TE`、`Trailer`、`Transfer-Encoding`、`Upgrade`、`Content-Length`；
8. 原请求 goroutine 只提交已经冻结的响应。请求取消可以立即结束等待；排队 Task 开始前发现取消
   会跳过业务处理，已运行 Task 只能完成私有结果，不能晚写响应；
9. 每次调用只分配一个有界结果槽，不为请求创建辅助 goroutine。是否需要进一步减少闭包、Channel 或
   Body 复制，必须由 Benchmark/Profile 决定。

`SafeErrorMapper` 只处理调度拒绝、Deadline、编码失败和内部契约错误，不替业务定义错误协议。它必须是
并发安全的纯函数，不能访问可变 Service 数据。默认规则不返回错误详情：Deadline 为 `504`，Service
未就绪、停止中或队列满为 `503`，其余为 `500`。客户端主动取消不再写响应。

Safe Handler panic 会先生成安全 `500` 结果，再重新交给 Service Scheduler 的 panic 边界记录和统计，
不能让请求一直等待到超时。响应 Body 超过 `MaxSafeResponseBodySize`、状态码非法或 Header 非法均按内部
错误处理。

## 3. HTTP Client 外观

```go
package httpclient

type Options struct {
    Timeout             time.Duration
    MaxResponseBodySize int64
    Transport           http.RoundTripper
    CheckRedirect       func(*http.Request, []*http.Request) error
    Jar                 http.CookieJar
}

func DefaultOptions() Options
func New(options Options) (*Client, error)

type TransportOptions struct {
    DialTimeout            time.Duration
    DialKeepAlive          time.Duration
    TLSHandshakeTimeout    time.Duration
    ResponseHeaderTimeout  time.Duration
    IdleConnTimeout        time.Duration
    MaxIdleConns           int
    MaxIdleConnsPerHost    int
    MaxConnsPerHost        int
    MaxResponseHeaderBytes int64
    Proxy                   func(*http.Request) (*url.URL, error)
    TLSConfig               *tls.Config
}

func DefaultTransportOptions() TransportOptions
func NewTransport(options TransportOptions) (*http.Transport, error)

type Response struct {
    StatusCode int
    Header     http.Header
    Body       []byte
}

var ErrResponseBodyTooLarge error

func (client *Client) Do(request *http.Request) (*http.Response, error)
func (client *Client) DoBytes(request *http.Request) (Response, error)
func (client *Client) CloseIdleConnections()
```

### 3.1 默认值

- `Timeout`：`30s`，覆盖连接、重定向和读取响应 Body 的总时间；请求 Context 可以提供更短预算；
- `MaxResponseBodySize`：`4MiB`，只作用于 `DoBytes`；
- `CheckRedirect`：`nil`，采用标准库最多连续 10 次跳转的策略；服务间调用不允许跳转时，由使用者返回
  `http.ErrUseLastResponse`；
- `Jar`：`nil`，默认不自动持久化 Cookie；
- `Transport`：`nil` 时由 Client 调用 `NewTransport(DefaultTransportOptions())` 创建私有连接池；非 nil
  表示使用调用方提供且可能共享的 `RoundTripper`；
- 框架创建的 Transport 始终校验 TLS；需要自签证书时注入正确的 Root CA，`NewTransport` 拒绝
  `InsecureSkipVerify=true`。完全自定义的 `RoundTripper` 由调用方承担安全责任。

默认 `TransportOptions`：

| 字段 | 默认值 | 说明 |
| --- | ---: | --- |
| `dial_timeout` | `5s` | DNS/TCP 单次建连预算，请求 Context 更早到期时以 Context 为准 |
| `dial_keep_alive` | `30s` | TCP KeepAlive 探测周期，不是 HTTP 连接池空闲时间 |
| `tls_handshake_timeout` | `10s` | TLS 握手预算 |
| `response_header_timeout` | `15s` | 请求 Body 写完后等待响应 Header 的预算 |
| `idle_conn_timeout` | `90s` | HTTP Keep-Alive 空闲连接保留时间 |
| `max_idle_conns` | `128` | 全部目标合计的空闲连接上限 |
| `max_idle_conns_per_host` | `16` | 单目标保留的空闲连接上限；标准库默认 2 对服务间并发通常偏小 |
| `max_conns_per_host` | `64` | 单目标正在拨号、活动和空闲连接总上限，达到后请求等待可用连接 |
| `max_response_header_bytes` | `1MiB` | 单次响应 Header 上限 |
| `proxy` | `http.ProxyFromEnvironment` | 遵循 `HTTP_PROXY`、`HTTPS_PROXY`、`NO_PROXY` |
| `tls_config` | `nil` | 使用系统根证书；非 nil 时构造器克隆配置 |

`NewTransport` 还固定 `ExpectContinueTimeout=1s`、启用透明 gzip 和 `ForceAttemptHTTP2=true`。这些属于
安全互操作默认值，不再增加低价值的同名包装字段；高级使用者可以在首次请求前修改返回的
`*http.Transport`。`MaxIdleConns >= MaxIdleConnsPerHost`、`MaxConnsPerHost >= MaxIdleConnsPerHost`，
全部时间和容量必须为正。默认值只是普通服务间 API 的安全起点，连接并发必须结合上游容量和压测调整。

框架不增加业务级重试、退避或熔断。Go Transport 仍可能在已复用连接发生网络错误时，对可重放且被识别为
幂等的请求执行标准安全重试；业务不得把它误认为应用层可靠投递保证。

### 3.2 请求与响应所有权

- `Do` 与 `http.Client.Do` 一致：返回成功时调用方必须读取并关闭 `Response.Body`；
- `DoBytes` 最多读取 `MaxResponseBodySize + 1` 字节，超限返回稳定错误并关闭 Body；成功结果克隆 Header，
  Body 完全归调用方；
- `DoBytes` 与标准库一致，不把 `4xx/5xx` 状态自动转换为 Go error，业务根据 `StatusCode` 处理；
- `Do` 保留标准库 `*url.Error`、Context 和网络错误链；`DoBytes` 只补充
  `ErrResponseBodyTooLarge` 以及读取/关闭 Body 的错误，不重新发明 HTTP 错误码映射；
- Client 不修改调用方 Request、Header 或 Body，不自动添加鉴权、Content-Type 或 Trace Header；
- `DoBytes` 的大小上限作用于透明解压后的 Body，避免小压缩包展开后无界占用内存；
- Client 不自动关闭连接池；显式 `CloseIdleConnections` 转发给底层 Transport，只关闭空闲连接，不中断
  活动请求。注入共享 Transport 时，调用方必须统一决定调用时机；
- 关闭空闲连接后 Client 仍可继续使用，行为与标准库一致；活动请求由各自 Context 取消。

### 3.3 在 Service 中调用

HTTP Client 不创建异步 API。Service Task 内使用 `Await`：

```go
var response httpclient.Response
err := module.Await(ctx, func(waitCtx context.Context) error {
    request, err := http.NewRequestWithContext(waitCtx, http.MethodGet, targetURL, nil)
    if err != nil {
        return err
    }
    response, err = module.client.DoBytes(request)
    return err
})
```

这会在等待 HTTP I/O 时释放 Service 执行权。若目标是同一个 Service 的 Gin Safe Handler，原 Task
释放执行权后，HTTP 入口投递的新 Safe Task 才能运行，形成可完成的自调用链。禁止在 Service Task 中
直接阻塞调用自身 HTTP 接口。

## 4. 测试门禁

### 4.1 Gin Server

- 默认值、严格配置、非法地址/代理/容量/TLS、请求 Context 到期统计；
- `127.0.0.1:0` 启动、真实地址、正常路由、404；
- 端口占用同步失败和部分启动回滚；
- Header/Body/活动请求上限及拒绝统计；
- Module/RouterGroup/SafeRouterGroup 的普通与 Safe 路由、Middleware 继承、执行顺序和重复路由行为；
- `SafeContext` 请求快照、JSON 绑定、Keys 鉴权结果、Safe Middleware Next/Abort、串行数据访问和响应冻结；
- 调度拒绝、排队取消、运行中取消、Deadline、响应编码错误与 Safe Handler panic；
- panic 前后响应提交边界，panic 后 Server 仍可用；
- 客户端取消、读写超时、在途请求优雅停止、停止超时强制关闭；
- 默认不信任转发代理，显式 CIDR 后行为正确；
- TLS 握手与证书校验；并发请求在 `-race` 下无竞争。

### 4.2 HTTP Client

- 默认值与非法 Options；
- `TransportOptions` 的连接、TLS、代理、响应 Header 和连接池边界，以及非法字段组合；
- 同一 Client 顺序和并发请求复用底层连接；不同 Client 不共享可关闭连接池；
- Context 取消、总超时、重定向策略和 TLS 默认校验；
- `Do` Body 所有权，`DoBytes` 精确边界、超限、读失败和 Close 失败主错误语义；
- 显式关闭默认或注入 Transport 的空闲连接不影响活动请求；
- 不泄漏 goroutine、Timer、Body 或连接。

### 4.3 纵向验收

必须覆盖：Service Task → `Await` → HTTP Client → 自身 Gin `SafePOST` → 同一 Service Task → HTTP
响应。测试需要证明成功、业务错误、请求取消、队列过载和停止路径均能收敛，且没有晚写响应或 Service
自调用死锁。

Windows 执行完整测试和 `go vet`；Ubuntu 执行完整测试、`go test -race`、覆盖率和 Example 启停。
Gin Server 与 HTTP Client 属于重点新包，公开行为分支尽量达到 100% 覆盖；无法覆盖的系统错误分支必须在
验收报告逐项说明，不能只报告总覆盖率。

## 5. 设计门禁

实施前只需确认以下公开结论：

1. 包名采用 `sysmodule/ginmodule` 与 `sysmodule/httpclient`；
2. 业务类型匿名嵌入 `ginmodule.Module`，常用路由、中间件、分组和运行信息只从当前 Module 调用；私有
   Engine 不作为主要入口，也不接受外部 Engine；
3. 保留 `POST`/`SafePOST` 的直观区分；Safe 回调无感运行于 Service 工作协程，内部使用请求快照和缓冲
   响应，不传递原始 `*gin.Context`/ResponseWriter；
4. 普通和 Safe 路由均采用 `METHOD(path, handler, middleware...)`；普通鉴权运行在请求 goroutine，必须
   访问 Service 状态的授权使用 Safe Middleware 或 SafeGroup；不增加按 Method 区分的鉴权专用接口；
5. HTTP Client 无 YAML、无 Module、无自动重试，提供 `Do` 与有界 `DoBytes`；
6. 默认限制普通 HTTP API，不为 SSE、超大上传或 HTTP/3 放宽边界。
