# Origin Gin 与 HTTP Client 能力分析

> 目标版本：v3.2  
> 参考范围：v2 `sysmodule/netmodule/ginmodule`、`sysmodule/httpclientmodule` 与 v3 当前 HTTP、
> Service、Module、RPC、Await 实现

## 1. 结论

下一批系统组件分为两个独立包：

- `sysmodule/ginmodule`：由 Service 托管生命周期的 HTTP Server Module；
- `sysmodule/httpclient`：由业务代码构造和持有的 HTTP Client，不是 Module，不读取 Service YAML。

HTTP Client 与 TCP、WebSocket、KCP 的 Dialer 采用相同的所有权原则：地址、鉴权、TLS、代理和连接池
参数由代码明确注入。但 HTTP 与一次长连接 Dial 不同，底层 `http.Transport` 必须跨请求复用连接池，
不能每次请求创建新的 `http.Client` 或 `Transport`。

Gin 不加入 `network.Session` 外观。HTTP 请求/响应、路由和中间件与有序长连接消息的生命周期不同，
为了表面统一而增加适配层只会扩大接口和误用空间。

## 2. 评审原则

1. 当前代码公开外观是事实基线；设计文档与代码不一致时，先以已人工确认的代码外观为准，再修正文档；
2. 项目尚未对外发布，不迁移 v2 兼容层、废弃别名或旧函数名；
3. 优化必须解决已确认的正确性、安全性、易用性或维护问题；不为假设中的扩展场景增加抽象；
4. 设计控制在普通 HTTP API/管理接口需要的范围，不在首批实现 HTTP/3、反向代理、网关、缓存、
   自动重试、熔断、服务发现或通用 REST SDK；
5. 性能工作先保证连接池复用、有界请求/响应和无额外 goroutine；只有 Benchmark/Profile 证明存在
   稳定瓶颈后再优化，不引入 HTTP Body 或 Gin Context 对象池；
6. 每个公开能力必须同时具备单元测试、生命周期测试、Windows 验证、Ubuntu `-race` 验证和使用者教程。

## 3. v2 能力与问题

### 3.1 Gin Module

v2 已提供路由、中间件、自定义 Processor、HTTP/HTTPS 启停和所谓 Safe Handler，证明业务 HTTP Server
是必要能力。但以下实现不应迁移：

- `gin.Default()` 隐式安装全局日志和恢复中间件，无法自然接入 Origin 结构化日志；
- Safe Handler 把 `*gin.Context` 投递到 Service 后阻塞请求 goroutine，超时返回后任务仍可能继续写响应；
- Safe/普通方法虽然直观，但 v2 分别实现每个 Method，内部逻辑重复且命名不统一；
- 代理 Header 信任逻辑不完整，可能接受伪造的客户端 IP；
- 启动错误、异步 Serve 失败、优雅停止和请求耗尽没有形成一个可验证的生命周期状态机。

v2 的 `IGinProcessor` 不原样保留。鉴权、限流、审计等请求前后处理采用 Middleware 链，并由 HTTP Module
暴露全局、分组和单路由三个作用域。普通 Middleware 复用 Gin 生态并运行在请求 goroutine；只有必须读取
Service 串行状态的授权才使用 Safe Middleware。这样不再维护一个用途只限于鉴权的 Processor。

### 3.2 HTTP Client Module

v2 提供请求构造、Header 和读取响应 Body 的便捷能力，但生命周期并不需要 Module。主要问题包括：

- 默认关闭 TLS 证书校验；
- 无上限读取响应 Body；
- “同步请求”额外创建 goroutine 和 Channel，并存在计时资源泄漏风险；
- 请求缺少强制 Context/总超时，Header 所有权不清；
- 没有明确 Transport/连接池所有权，容易退化成每次请求新建 Client。

## 4. 必需能力

### 4.1 Gin Server Module

- 由 `Service.AddModule` 托管，启动时同步绑定 Listener，停止时优雅耗尽请求；
- 业务类型匿名嵌入 `ginmodule.Module`；配置、路由、分组、中间件、普通 Handler、Safe Handler、地址和统计
  全部从当前 HTTP Module 调用，不要求使用者取得 `*gin.Engine`；
- Module 与其 `RouterGroup`、`SafeRouterGroup` 直接提供 `Use`、`Group`、`Handle`、`GET`、`POST` 等常用
  Gin 风格接口；各方法统一委托一个内部注册入口，不复制路由逻辑；
- 普通与 Safe 路由统一使用 `METHOD(path, handler, middleware...)`，最终业务回调固定为第二个参数，后续
  Middleware 可省略；不增加 `AuthPOST`、`SafeAuthPOST` 等按 Method 成倍增长的鉴权专用方法；
- 提供严格 `ServerConfig`，包括监听地址、请求 Context/Header/读/写/空闲超时、Header/Body 上限、
  活动请求上限和可信代理；
- TLS 证书与动态安全策略通过代码注入，不写入通用 YAML；
- 默认不信任转发代理，具备 panic 边界、过载拒绝和最小固定统计；
- 暴露真实监听地址，支持测试使用 `127.0.0.1:0`；
- 普通 `POST` 等 Handler 保持标准 `net/http` 并发模型；`SafePOST` 等 Safe Handler 由框架自动投递到所属
  Service 工作协程，业务无需手写 Dispatch、闭包或响应提交；
- 全局和分组鉴权使用 Gin Middleware；成功后的鉴权结果通过请求期 `Context.Keys` 快照带入
  `SafeContext`。鉴权失败可以在投递前直接中止请求；需要访问 Service 串行状态的授权使用可选
  `SafeMiddlewareFunc`，单路由放在 Handler 后传入，多路由共享时使用 `SafeGroup`；
- `Group` Middleware 永远运行在 Gin/HTTP 请求 goroutine；`SafeGroup` Middleware 与其 GET/POST Handler
  永远运行在 Service 工作协程。两者允许嵌套，但执行位置不隐式变化。

Safe Handler 内部仍采用“请求快照 → Service Task → 响应提交”三段式，但这是实现细节：请求 goroutine
先复制有界 Body、Header、URL、Params、Query 和 Middleware Keys；Safe Handler 在 Service Scheduler
中通过私有 `SafeContext` 读取快照、绑定参数、访问业务状态并生成缓冲响应；最后仅由原请求 goroutine
提交冻结后的响应。请求取消后可以立即返回，排队或运行中的 Task 只能产生私有结果，不能继续写已经结束
的 ResponseWriter。

普通 Handler 如果只需调用已有 Service RPC，仍可直接使用生成的 `CallXxx`。框架保留 v2 中
`POST`/`SafePOST` 易于区分执行位置的外观意图，但不迁移其把 `*gin.Context` 与 ResponseWriter 投递进
Service 的实现，也不要求每个 HTTP 入口额外定义一个 RPC。

### 4.2 HTTP Client

- 代码构造、并发复用、无 YAML、无 Module 生命周期；
- `Do` 保留标准流式响应能力，由调用方关闭 Body；
- `DoBytes` 提供有界完整读取并自动关闭 Body；
- 请求使用调用方 `context.Context`，Client 同时提供非零总超时兜底；
- 默认校验 TLS，支持 HTTP/2，复用独占 Transport 连接池；连接、TLS 握手、响应 Header、空闲连接和
  每主机连接总数均有界；
- 提供独立 `TransportOptions` 覆盖生产常调的拨号、TLS、代理、响应 Header 和连接池字段，不把整个
  `http.Transport` 复制成第二套框架 API；
- 自定义 `RoundTripper`、重定向策略和 Cookie Jar 通过代码注入；
- 不增加框架级业务重试。HTTP 方法是否幂等、Body 能否重放和业务去重只能由业务判断；保留 Go
  Transport 对已复用失效连接执行的标准安全重试；
- 不提供异步 Channel 包装。普通 goroutine 直接调用；Service 任务使用 `Module.Await` 或
  `Service.Await`，在等待网络 I/O 时释放 Service 执行权；
- Client 不自动关闭 Transport；显式 `CloseIdleConnections` 只关闭空闲连接，不中断活动请求。注入共享
  Transport 时，调用方负责决定何时调用该方法。

## 5. 首批不做的能力

| 能力 | 不进入首批的原因 |
| --- | --- |
| v2 Safe Handler 的 Context/等待实现 | 保留 `SafePOST` 等直观外观；内部改为请求快照、Service 串行执行和冻结响应 |
| 对外暴露 `Engine()` 作为主要入口 | 常用路由、分组和 Middleware 由 Module 包装；有真实缺口时再增加受控扩展点 |
| HTTP Client Module/Service Config | Client 不拥有独立监听生命周期，地址与鉴权通常按调用目标变化 |
| 自动重试、熔断、负载均衡 | 会引入幂等、发现和策略语义，应由后续独立能力证明需求 |
| 自动 JSON/PB REST SDK | 标准库编码已足够；框架不应推断业务 Content-Type 和错误协议 |
| SSE、文件上传优化、HTTP/3 | 普通 HTTP API 验收后按真实需求建立独立设计 |
| Gin Context/HTTP Body 内存池 | 生命周期和收益不明确，错误池化容易保留大对象或产生数据竞争 |
| 抽取 Application 全部 HTTP Runtime | 当前 Admin/pprof 有独立错误和安全语义；首批强行共用会扩大回归范围 |

## 5.1 最终配置精简结论

| 未增加的字段或便捷能力 | 处理方式 |
| --- | --- |
| Server `shutdown_timeout` | 使用 Application 传入 `OnStop(ctx)` 的统一停止预算，避免两套 Deadline |
| Server TLS 证书路径、私钥、自动证书 | 使用代码注入的 `tls.Config`，不把密钥路径和证书管理固化进通用配置 |
| Server `base_path`、CORS、鉴权、访问日志格式 | 继续使用 Gin Route/Middleware；这些属于业务或部署策略 |
| Server KeepAlive 开关、HTTP/2 细项 | 使用 Go 安全默认值；首批没有真实需求证明需要公开 |
| Client `base_url`、默认 Header、鉴权 | 写入具体 Request 或业务 Client，避免共享 Client 隐式污染请求 |
| Client GET/POST/JSON/PB 便捷方法 | 使用 `http.NewRequestWithContext` 和标准编码库，避免复制标准库 API |
| Client 业务重试、退避、熔断 | 需要业务幂等与上游策略，后续独立设计；不改变 Go Transport 的安全重试 |
| Client 全部 `http.Transport` 字段 | 常用字段进入 `TransportOptions`；罕见字段在首次请求前修改 `NewTransport` 返回值 |

最终 Server YAML 仅保留生产部署经常调整、且由 Server 唯一拥有的字段；HTTP Client 仍无 YAML，
请求级 Options 与连接级 TransportOptions 分层，避免一个大而混杂的配置结构。

## 6. 额外补充且有必要的能力

相对 v2，首批增加六项必要能力：

1. 默认禁用代理 Header 信任，只有显式可信代理 IP/CIDR 才接受转发客户端地址；
2. Server 请求并发、Header 和 Body 有界，Client 完整响应读取有界；
3. Server 为请求 Context 建立总截止时间，使 RPC、数据库和 HTTP 下游能够随请求取消；
4. `SafePOST` 等回调无感运行在 Service 串行上下文；原始 Gin Context 和 ResponseWriter 始终留在请求
   goroutine，Safe Handler 只接触请求快照和私有缓冲响应；
5. Server 启动绑定失败同步返回，停止等待在途请求，超时后强制释放 Listener；
6. 验证“Service 通过 Await 调 HTTP Client → 自身 Gin Safe Handler → 同一 Service”的完整自调用。

这些能力直接解决安全、过载、生命周期和死锁风险，不属于过度设计。

## 7. 调研依据

- Gin 官方推荐使用自定义 `http.Server` 配置超时，并通过 `Shutdown` 优雅停止；
- Gin 默认信任所有代理，公开部署必须禁用或显式设置可信代理；
- Gin 的 Middleware 原生支持全局、分组和单路由三个作用域，适合作为鉴权等横切逻辑的唯一扩展方式；
- Gin Context 会被复用，不能把原始对象交给异步 goroutine；Hertz 与 fasthttp 也都明确限制请求 Context
  生命周期，因此 Safe Handler 必须使用框架独立拥有的快照与缓冲响应；
- Go `http.Transport` 会缓存连接，应该复用且可以并发使用；请求 Context 是取消网络请求的标准边界；
- 当前 Gin v1.12.0 要求 Go 1.24，满足 Origin v3 当前 Go 1.26.5 基线。

参考：

- [Gin Server 配置](https://gin-gonic.com/en/docs/server-config/)
- [Gin Context 与取消](https://gin-gonic.com/en/docs/server-config/context/)
- [Gin Middleware](https://gin-gonic.com/en/docs/middleware/)
- [Gin 路由分组](https://gin-gonic.com/en/docs/routing/grouping-routes/)
- [Gin 在 goroutine 中使用 Context](https://gin-gonic.com/en/docs/middleware/goroutines-inside-a-middleware/)
- [Echo 路由与可选单路由 Middleware](https://echo.labstack.com/docs/routing)
- [Gin 可信代理配置](https://gin-gonic.com/en/docs/server-config/trusted-proxies/)
- [Gin 安全指南](https://gin-gonic.com/en/docs/middleware/security-guide/)
- [Hertz RequestContext](https://www.cloudwego.io/docs/hertz/tutorials/basic-feature/context/)
- [fasthttp RequestCtx](https://github.com/valyala/fasthttp)
- [Go net/http 文档](https://pkg.go.dev/net/http)
- [Gin Releases](https://github.com/gin-gonic/gin/releases)

## 8. 推荐实施顺序

遵循“改动大的先做、依赖能力先做、每个切片独立验收”的顺序：

1. 冻结公开外观、默认值、所有权和不做范围；
2. 实现 Gin Server 的监听生命周期、安全边界和严格配置；
3. 实现较小的 HTTP Client 与连接池所有权；
4. 补充同服务 HTTP 自调用纵向 Example；
5. 完成 Windows、Ubuntu、Race、覆盖率和故障注入验收；
6. 最后优化教程并进行一次跨代码、配置、Example 的一致性 Review。
