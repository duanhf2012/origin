# Origin Gin 与 HTTP Client 核心设计

> 目标版本：v3.2  
> 状态：待公开外观确认后实施  
> 能力依据：[Origin Gin 与 HTTP Client 能力分析](../proposals/Origin%20Gin与HTTP%20Client能力分析.md)

## 1. 包与所有权

```text
业务 Service
└── 业务 HTTP Module
    ├── ginmodule.Server       Service 托管，拥有 Listener、http.Server 和 gin.Engine
    └── httpclient.Client      普通字段，业务代码持有并跨请求复用
```

- `sysmodule/ginmodule` 使用 Gin，但不加入 `sysmodule/network`；
- `sysmodule/httpclient` 只依赖 Go 标准库，不依赖 Gin，也不嵌入 `service.Module`；
- 业务路由、HTTP Client 和 Handler 集中放在业务 Module，Service 只负责装配；
- 一个 `httpclient.Client` 应按调用目标或公共传输策略长期复用，不能按请求创建。

## 2. Gin Server 外观

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
    MaxServiceResponseBodySize config.ByteSize
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
    MaxServiceResponseBodySize int64
    MaxActiveRequests          int
    TrustedProxies             []string
    TLSConfig                  *tls.Config
    ServiceErrorMapper         ServiceErrorMapper
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
| `max_header_bytes` | `1MiB` | 请求 Header 上限，也作为 `ServiceHandler` 缓冲响应 Header 上限 |
| `max_request_body_size` | `4MiB` | 普通 JSON/PB API 的安全起点，不代表上传接口最优值 |
| `max_service_response_body_size` | `4MiB` | `ServiceHandler` 缓冲响应 Body 上限；普通流式 Handler 不适用 |
| `max_active_requests` | `1024` | 当前 Server 的在途请求硬上限 |
| `trusted_proxies` | `[]` | 默认不信任任何转发代理 |

所有超时和容量必须大于零，并校验 `write_timeout > request_timeout`。首批不允许用零值隐式关闭边界；
确有 SSE、超大上传等需求时，应先补充对应测试和设计，而不是把普通 Server 默认值整体放开。

`request_timeout` 只通过首个框架 Middleware 为 `Request.Context()` 建立 Deadline，不把 Handler 放入新的
goroutine，也不承诺强制中断忽略 Context 的业务代码。Handler 应把 `ctx.Request.Context()` 传给生成的
`CallXxx`、数据库和下游客户端；业务错误响应格式仍由项目定义。

### 2.2 Server

```go
type Server struct {
    service.Module
    // unexported runtime fields
}

func NewServer(address string, options ServerOptions) (*Server, error)
func (server *Server) Engine() *gin.Engine
func (server *Server) Addr() net.Addr
func (server *Server) Stats() ServerStats

type ServerStats struct {
    ActiveRequests   int64
    TotalRequests    uint64
    RejectedRequests uint64
    TimedOutRequests uint64
    PanicTotal       uint64
}
```

`NewServer` 创建私有 `gin.New()` Engine，并在返回前安装框架拥有的第一个全局 Middleware 边界。使用者通过
`Engine()` 注册 Gin 中间件和路由，注册必须在 `AddModule` 前完成。首批不接受外部 Engine，避免框架
边界因中间件注册顺序而失效；Gin 的路由、Render、Validator 等能力仍可通过返回的 Engine 配置。

框架边界只负责：

- 活动请求准入，超限立即返回 `503`；
- 使用 `http.MaxBytesReader` 限制请求 Body；
- 为 Request Context 建立统一截止时间，并在 Handler 返回后释放 Timer；
- 捕获未处理 panic，记录 Origin 结构化日志，并在尚未提交响应时返回 `500`；
- 维护低基数固定统计。

框架不默认记录每个访问日志，不读取业务 Header、Query 或 Body 写日志，也不替业务定义 JSON 错误格式。

### 2.3 生命周期

1. `OnInit` 冻结配置和代理列表；
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
func (module *HTTPModule) getPlayer(ctx *gin.Context) {
    name, err := module.players.CallGetPlayer(ctx.Request.Context(), playerID)
    if err != nil {
        // 由项目自己的 HTTP 错误映射统一响应。
        return
    }
    ctx.JSON(http.StatusOK, gin.H{"name": name})
}
```

需要直接访问当前 Service 业务数据时使用 `ServiceHandler`。它不把 `*gin.Context` 或 ResponseWriter
传入 Service，而是明确拆分请求准备、串行业务和响应提交。

### 2.5 ServiceHandler

```go
type Response struct {
    StatusCode int
    Header     http.Header
    Body       []byte
}

type ServiceTask func(context.Context) (Response, error)
type ServiceErrorMapper func(error) Response

func (server *Server) ServiceHandler(
    prepare func(*gin.Context) ServiceTask,
) gin.HandlerFunc

func JSONResponse(statusCode int, value any) (Response, error)
```

推荐使用方式：

```go
server.Engine().POST(
    "/players",
    server.ServiceHandler(module.prepareCreatePlayer),
)

// prepareCreatePlayer 在 HTTP 请求 goroutine 执行，只读取请求并生成独立 DTO。
func (module *HTTPModule) prepareCreatePlayer(ctx *gin.Context) ginmodule.ServiceTask {
    var request CreatePlayerRequest
    if err := ctx.ShouldBindJSON(&request); err != nil {
        ctx.JSON(http.StatusBadRequest, gin.H{"error": "invalid request"})
        return nil
    }
    return func(taskCtx context.Context) (ginmodule.Response, error) {
        // 该闭包在所属 Service 的串行上下文执行，可以直接安全访问 module 业务数据。
        player, err := module.createPlayer(taskCtx, request)
        if err != nil {
            return ginmodule.Response{}, err
        }
        // JSON 在 Service 执行权释放前编码，避免返回值引用随后会变化的业务数据。
        return ginmodule.JSONResponse(http.StatusOK, player)
    }
}
```

固定执行契约：

1. `prepare` 在请求 goroutine 执行，可以使用全部 Gin 绑定、参数、鉴权和 Middleware 数据，但不能读取
   或修改需要 Service 串行保护的业务状态；
2. 参数错误可由 `prepare` 直接写响应并返回 nil；返回 nil 且尚未提交响应视为 Handler 契约错误；
3. `ServiceTask` 被投递到 Server 所属 Service 的有界 FIFO，只接收合并后的 Context；该 Context 保留
   Service Task 执行令牌，同时继承 HTTP 请求的 Value、Deadline 和取消；
4. `ServiceTask` 可以直接访问所属业务 Module 数据，可以调用同步 Event；等待数据库、HTTP 或其他异步
   I/O 时仍须使用 `Await`，避免阻塞 Service；
5. `ServiceTask` 不得捕获或使用 `*gin.Context`、ResponseWriter、原始 Request Body 等请求期可变对象；
   只捕获已完成绑定且由当前请求独占的 DTO；
6. Task 返回后，框架在释放 Service 执行权前验证并克隆 Header/Body。只允许最终状态码，响应 Header
   总量受 `MaxHeaderBytes` 限制，并拒绝 `Connection`、`Keep-Alive`、`Proxy-Authenticate`、
   `Proxy-Authorization`、`TE`、`Trailer`、`Transfer-Encoding`、`Upgrade`、`Content-Length`；
7. 原请求 goroutine 只提交已经冻结的 `Response`。请求取消可以立即结束等待；排队 Task 开始前发现取消
   会跳过业务处理，已运行 Task 只能完成私有结果，不能晚写响应；
8. 每次调用只分配一个有界结果槽，不为请求创建辅助 goroutine。是否需要进一步减少闭包、Channel 或
   Body 复制，必须由 Benchmark/Profile 决定。

`ServiceErrorMapper` 是代码注入的纯错误映射函数，必须并发安全且不能访问可变 Service 数据。业务 Task
错误在 Service 执行权释放前映射成 `Response`；调度拒绝和请求 Deadline 也使用同一映射。默认规则不返回
错误详情：Deadline 为 `504`，Service 未就绪、停止中或队列满为 `503`，其余为 `500`。客户端主动取消
不再写响应。

Task panic 会先生成安全 `500` 结果，再重新交给 Service Scheduler 的 panic 边界记录和统计，不能让请求
一直等待到超时。`Response` Body 超过 `MaxServiceResponseBodySize`、状态码非法或 Header 非法均按内部
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

这会在等待 HTTP I/O 时释放 Service 执行权。若目标是同一个 Service 的 Gin `ServiceHandler`，原 Task
释放执行权后，HTTP 入口投递的新 ServiceTask 才能运行，形成可完成的自调用链。禁止在 Service Task 中
直接阻塞调用自身 HTTP 接口。

## 4. 测试门禁

### 4.1 Gin Server

- 默认值、严格配置、非法地址/代理/容量/TLS、请求 Context 到期统计；
- `127.0.0.1:0` 启动、真实地址、正常路由、404；
- 端口占用同步失败和部分启动回滚；
- Header/Body/活动请求上限及拒绝统计；
- `ServiceHandler` 三阶段线程归属、串行数据访问、响应复制、非法 Header/状态/大小；
- Prepare 返回 nil、调度拒绝、排队取消、运行中取消、Deadline、Task error 与 Task panic；
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

必须覆盖：Service Task → `Await` → HTTP Client → 自身 Gin `ServiceHandler` → 同一 ServiceTask → HTTP
响应。测试需要证明成功、业务错误、请求取消、队列过载和停止路径均能收敛，且没有晚写响应或 Service
自调用死锁。

Windows 执行完整测试和 `go vet`；Ubuntu 执行完整测试、`go test -race`、覆盖率和 Example 启停。
Gin Server 与 HTTP Client 属于重点新包，公开行为分支尽量达到 100% 覆盖；无法覆盖的系统错误分支必须在
验收报告逐项说明，不能只报告总覆盖率。

## 5. 设计门禁

实施前只需确认以下公开结论：

1. 包名采用 `sysmodule/ginmodule` 与 `sysmodule/httpclient`；
2. Gin Server 创建并拥有 Engine，不接受外部 Engine；
3. 不迁移 v2 Safe Handler，实现三段式 `ServiceHandler`；普通 Handler 仍可通过生成的 `CallXxx` 进入；
4. HTTP Client 无 YAML、无 Module、无自动重试，提供 `Do` 与有界 `DoBytes`；
5. 默认限制普通 HTTP API，不为 SSE、超大上传或 HTTP/3 放宽边界。
