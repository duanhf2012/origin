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
    Address            string
    ReadHeaderTimeout  config.Duration
    ReadTimeout        config.Duration
    WriteTimeout       config.Duration
    IdleTimeout        config.Duration
    MaxHeaderBytes     config.ByteSize
    MaxRequestBodySize config.ByteSize
    MaxActiveRequests  int
    TrustedProxies     []string
}

func DefaultServerConfig() ServerConfig
func (ServerConfig) Options() (ServerOptions, error)

type ServerOptions struct {
    ReadHeaderTimeout  time.Duration
    ReadTimeout        time.Duration
    WriteTimeout       time.Duration
    IdleTimeout        time.Duration
    MaxHeaderBytes     int
    MaxRequestBodySize int64
    MaxActiveRequests  int
    TrustedProxies     []string
    TLSConfig          *tls.Config
}

func DefaultServerOptions() ServerOptions
```

`ServerConfig` 只保存可序列化且属于 Server 的字段。TLS 证书、动态证书回调等安全对象在
`Options()` 后通过代码设置。默认配置建议如下：

| 字段 | 默认值 | 说明 |
| --- | ---: | --- |
| `address` | `0.0.0.0:19093` | 示例起始值，生产环境按暴露范围修改 |
| `read_header_timeout` | `5s` | 防止慢速 Header 占用连接 |
| `read_timeout` | `15s` | 包含读取请求 Body；大上传应按实际业务调整 |
| `write_timeout` | `15s` | 普通 API 响应上限；SSE/长流式响应需单独设计或关闭 |
| `idle_timeout` | `60s` | Keep-Alive 空闲连接保留时间 |
| `max_header_bytes` | `1MiB` | 与 Go Server 默认量级一致 |
| `max_request_body_size` | `4MiB` | 普通 JSON/PB API 的安全起点，不代表上传接口最优值 |
| `max_active_requests` | `1024` | 当前 Server 的在途请求硬上限 |
| `trusted_proxies` | `[]` | 默认不信任任何转发代理 |

所有正时长和正容量必须大于零。首批不允许用零值隐式关闭边界；确有 SSE、超大上传等需求时，
应先补充对应测试和设计，而不是把普通 Server 默认值整体放开。

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
    PanicTotal       uint64
}
```

`NewServer` 创建私有 `gin.New()` Engine，并在返回前安装框架拥有的第一个全局 Middleware 边界。使用者通过
`Engine()` 注册 Gin 中间件和路由，注册必须在 `AddModule` 前完成。首批不接受外部 Engine，避免框架
边界因中间件注册顺序而失效；Gin 的路由、Render、Validator 等能力仍可通过返回的 Engine 配置。

框架边界只负责：

- 活动请求准入，超限立即返回 `503`；
- 使用 `http.MaxBytesReader` 限制请求 Body；
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

### 2.4 Handler 并发与 Service 状态

Gin Handler 运行在 `net/http` 请求 goroutine，不在 Service Scheduler 内。Handler 可以直接处理参数、
鉴权、编码和无共享状态的 I/O；需要访问 Service 串行状态时调用生成的 `CallXxx`：

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

不提供把 `*gin.Context` 投递到 Service 的方法。Gin Context、ResponseWriter 与请求 goroutine 同生共死；
跨 goroutine 传递会使取消、超时和响应提交的所有权变得不可靠。

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
func DefaultTransport() *http.Transport
func New(options Options) (*Client, error)

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
- 默认 Transport：每个 Client 独占一个连接池，启用系统代理和 HTTP/2；连接、TLS 握手、响应 Header、
  空闲连接均有界；每主机允许适合服务间调用的连接复用，而不是沿用每主机仅少量空闲连接的默认值；
- TLS 证书校验始终开启；需要自签证书时注入正确的 Root CA，不提供 `InsecureSkipVerify` 快捷开关。

首批实现前通过测试冻结具体 Transport 数值；选择标准库默认量级附近的保守值，不根据假设吞吐盲目调大。

### 3.2 请求与响应所有权

- `Do` 与 `http.Client.Do` 一致：返回成功时调用方必须读取并关闭 `Response.Body`；
- `DoBytes` 最多读取 `MaxResponseBodySize + 1` 字节，超限返回稳定错误并关闭 Body；成功结果克隆 Header，
  Body 完全归调用方；
- `DoBytes` 与标准库一致，不把 `4xx/5xx` 状态自动转换为 Go error，业务根据 `StatusCode` 处理；
- Client 不修改调用方 Request、Header 或 Body，不自动添加鉴权、Content-Type 或 Trace Header；
- 默认 Transport 由 Client 持有，`CloseIdleConnections` 只关闭该池的空闲连接；调用方注入的 Transport
  被视为共享资源，不由 Client 关闭；
- Client 关闭空闲连接后仍可继续使用，行为与标准库一致；活动请求由各自 Context 取消。

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

这会在等待 HTTP I/O 时释放 Service 执行权。若目标是同一个 Service 的 Gin Handler，Handler 可以通过
`CallXxx` 回到该 Service，形成可完成的自调用链。禁止在 Service Task 中直接阻塞调用自身 HTTP 接口。

## 4. 测试门禁

### 4.1 Gin Server

- 默认值、严格配置、非法地址/代理/容量/TLS；
- `127.0.0.1:0` 启动、真实地址、正常路由、404；
- 端口占用同步失败和部分启动回滚；
- Header/Body/活动请求上限及拒绝统计；
- panic 前后响应提交边界，panic 后 Server 仍可用；
- 客户端取消、读写超时、在途请求优雅停止、停止超时强制关闭；
- 默认不信任转发代理，显式 CIDR 后行为正确；
- TLS 握手与证书校验；并发请求在 `-race` 下无竞争。

### 4.2 HTTP Client

- 默认值与非法 Options；
- 同一 Client 顺序和并发请求复用底层连接；不同 Client 不共享可关闭连接池；
- Context 取消、总超时、重定向策略和 TLS 默认校验；
- `Do` Body 所有权，`DoBytes` 精确边界、超限、读失败和 Close 失败主错误语义；
- 注入 Transport 不被 Client 关闭；默认 Transport 的空闲连接可关闭；
- 不泄漏 goroutine、Timer、Body 或连接。

### 4.3 纵向验收

必须覆盖：Service Task → `Await` → HTTP Client → 自身 Gin Handler → 本地 `CallXxx` → 同一 Service
业务方法 → HTTP 响应。测试需要证明成功、业务错误、请求取消和停止四条路径均能收敛。

Windows 执行完整测试和 `go vet`；Ubuntu 执行完整测试、`go test -race`、覆盖率和 Example 启停。
Gin Server 与 HTTP Client 属于重点新包，公开行为分支尽量达到 100% 覆盖；无法覆盖的系统错误分支必须在
验收报告逐项说明，不能只报告总覆盖率。

## 5. 设计门禁

实施前只需确认以下公开结论：

1. 包名采用 `sysmodule/ginmodule` 与 `sysmodule/httpclient`；
2. Gin Server 创建并拥有 Engine，不接受外部 Engine；
3. 不迁移 Safe Handler，Service 状态统一通过生成的 `CallXxx` 进入；
4. HTTP Client 无 YAML、无 Module、无自动重试，提供 `Do` 与有界 `DoBytes`；
5. 默认限制普通 HTTP API，不为 SSE、超大上传或 HTTP/3 放宽边界。
