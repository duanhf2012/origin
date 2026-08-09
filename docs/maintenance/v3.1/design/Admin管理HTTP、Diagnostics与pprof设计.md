# Admin 管理 HTTP、Diagnostics 与 pprof 设计

> 状态：已确认，允许实施
> 基线：v3.0
> 目标：v3.1.0
> 兼容性：以 Admin Server 取代 v3.0 独立 Diagnostics HTTP Server；保留本地 `Application.Diagnostics()` 快照能力和独立 pprof Listener
> 确认日期：2026-08-09

## 1. 目标与边界

v3.0 的 Diagnostics HTTP Listener 只提供一个只读 JSON 端点。v3.1 把新的 Listener 定位为
Application 所有的进程内管理控制面，使它既能提供内置诊断和生命周期控制，也能承载业务
自定义的 Application 或 Service 管理操作。

本次能力必须满足：

1. HTTP Listener、路由、安全策略、超时、限流、审计和关闭由 Application 统一拥有；
2. 读取或修改 Service 普通字段的回调必须进入目标 Service 的唯一执行槽；
3. 使用者只区分 GET 和 POST，不再学习 Read、Write、Action 三套抽象；
4. GET 必须无副作用，POST 可以修改数据、触发工作或返回异步受理结果；
5. Diagnostics、Application/Node/Service Retire 与 Resume 作为内置端点提供；
6. pprof 继续使用独立 Listener，不混入 Admin Server；
7. Admin Server 默认关闭；无认证时只允许绑定环回地址；
8. 所有队列、请求体、响应体、执行时间和并发数量必须有界；
9. 不引入第三方 HTTP Router、认证库、指标库或包级可变全局状态；
10. 第 10 章教程改为完整管理与诊断章节，原 Diagnostics 与 pprof 降为其中的小节，并提供
    可独立运行的完整 examples。

本设计不提供跨 Service 事务、动态脚本、任意反射调用、运行期路由热注册、浏览器管理 UI、
历史诊断存储或官方 Prometheus Exporter。复杂跨 Service 工作流由业务自建 ManagementService，
使用既有 RPC、事件、Await 和补偿逻辑编排。

## 2. 方案与所有权

### 2.1 不采用内置 AdminService 所有 Listener

如果全部 HTTP 请求先进入一个框架内置 Service，该 Service 的队列会成为进程级串行瓶颈；
它的失败、排空或停止也会使 Application 诊断和控制一起失效。Application 启动阶段尚未存在
Running Service，内置 Service 同样无法自然覆盖启动观测。

因此所有权固定为：

```text
Application
├── Admin Server：Listener、路由、安全、限流、审计和 HTTP 生命周期
├── Diagnostics：Application、Runtime、BufferPool、Node 与 Service 快照
├── 内置控制：Application / Node / Service Retire、Resume
├── Application 自定义端点：只允许操作并发安全数据或调用公开控制方法
└── Node[]
    └── Service[]
        └── 自定义端点：由 Admin Server 投递到该 Service 的唯一执行槽

Application
└── pprof Server：独立 Listener、独立暴露策略、按需短时开启
```

Admin Server 不直接读写 Service 普通字段。HTTP 请求先完成认证、参数限制和路由，再通过目标
Service 的有界调度器执行回调。业务回调取得与 Timer、Event、RPC Handler 相同的串行执行权，
可以安全读取和修改自身状态，也可以使用 Await 暂时释放执行权。

### 2.2 可选业务 ManagementService

Origin 不内置全局 AdminService。业务确有跨 Service 编排、长期任务状态、幂等记录或补偿流程
时，可以把普通业务 Service 设计为 ManagementService，并为它声明 Admin 端点。该 Service
通过生成 RPC、事件或公开管理能力通知其他 Service，不直接访问其他 Service 私有字段。

## 3. `admin` 公共包

新增 `admin` 公共包。它依赖标准库、`errs` 和 `service` 的最小公开调度能力，不依赖
`application` 或 `node`，避免包循环。Application 负责把 Endpoint 与真实 Service 实例绑定。

### 3.1 Provider 与 Endpoint

业务 Service 可选实现：

```go
type Provider interface {
    AdminEndpoints() []Endpoint
}
```

统一处理器签名为：

```go
type Handler func(
    ctx context.Context,
    request Request,
) (Response, error)

func Get(name string, handler Handler, options ...Option) Endpoint
func Post(name string, handler Handler, options ...Option) Endpoint
```

GET 和 POST 是唯一公开方法：

| 方法 | 语义 | 默认成功响应 | 框架策略 |
| --- | --- | --- | --- |
| GET | 只读且无副作用 | `200` | read 权限、普通访问审计、拒绝 Body |
| POST | 修改状态或触发工作 | `204` | write 权限、完整操作审计、限制 JSON Body |

POST 可以用 Response 显式返回 `200`、`202` 或 `204`。不提供 PUT、PATCH、DELETE；退休、
恢复、重载、刷新和删除缓存等管理动作统一使用命名清晰的 POST 端点。

Handler 返回零值 Response 时使用 Endpoint 的默认成功码；`WithSuccessStatus` 只改变这个默认
值。Handler 通过 JSON/Empty 返回显式 Response 时，以显式状态和 Body 为准。这样普通通知
可以直接返回零值，查询和需要结果的写操作仍能明确返回 JSON。

Endpoint 名称必须是 63 字节以内的小写 kebab-case，在同一作用域中唯一。Get/Post 只构造
冷路径描述符；空名称、空 Handler、非法 Option 和重复名称在 Application 路由冻结时统一
返回配置错误，不 panic。

### 3.2 Request

Service 回调不接收 `http.ResponseWriter`、原始 `*http.Request` 或可继续读取的 Body。Request
只保存已经脱离网络生命周期的有界值：

```go
type Request struct {
    RequestID string
    Principal Principal
    Query     url.Values
    Header    http.Header
    Body      []byte
}

func (request Request) DecodeJSON(target any) error
```

Query、Header 和 Body 在入队前独立复制。DecodeJSON 使用严格单值 JSON：拒绝空 Body、未知
字段、尾随第二个 JSON 值和目标 nil；错误映射为 400。业务确实需要非 JSON 数据时可读取
有界 Body 自行解析，但不得保存 Request 或 Body 到回调之后。

Principal 是认证层形成的不可变身份摘要，只保存 Subject、Role 和有界属性，不保存 Token、
密码或原始凭证。

### 3.3 Response

Response 保存已经脱离业务可变对象的状态码、Header 和 Body：

```go
type Response struct { /* immutable encoded response */ }

func JSON(status int, value any) (Response, error)
func Empty(status int) Response
```

`JSON` 在当前回调仍持有 Service 执行权时完成编码，因此业务返回的 Map、Slice 或指针不会在
HTTP goroutine 中与后续业务任务竞争。编码后 Body 受端点响应上限约束。响应只允许 2xx 成功
码；错误统一通过 error 返回并由框架映射，避免业务同时返回错误 Body 和成功状态。

不支持 Service 管理端点流式响应、Hijack、WebSocket 或 Server-Sent Events。大数据导出应先
生成独立文件或对象存储结果，再通过普通业务通道传输，不能长期占用 Service 唯一执行槽。

### 3.4 Endpoint Option

首版只提供真实需要的有界选项：

```go
WithTimeout(value time.Duration)
WithMaxBodyBytes(value int64)
WithMaxResponseBytes(value int64)
WithSuccessStatus(status int) // 主要用于 POST 的 200/202/204
```

默认值为：端点总超时 15 秒、请求 Body 1 MiB、响应 Body 4 MiB；GET 的 Body 上限固定为零。
所有值必须为正且不能超过框架硬上限，防止单个业务端点绕过 Server 资源边界。

## 4. 使用者外观

### 4.1 Service 查询和修改

```go
func (service *PlayerService) AdminEndpoints() []admin.Endpoint {
    return []admin.Endpoint{
        admin.Get("summary", service.adminSummary),
        admin.Post("reload-logic", service.adminReloadLogic),
        admin.Post(
            "refresh-player",
            service.adminRefreshPlayer,
            admin.WithSuccessStatus(http.StatusAccepted),
        ),
    }
}
```

GET 回调可以直接读取当前 Service 字段：

```go
func (service *PlayerService) adminSummary(
    context.Context,
    admin.Request,
) (admin.Response, error) {
    return admin.JSON(http.StatusOK, PlayerSummary{
        OnlinePlayers: len(service.players),
        LogicVersion:  service.logicVersion,
    })
}
```

POST 回调可以解析输入并修改字段。需要读取文件、数据库或远端配置时先在 Await 中只操作
局部变量，Await 返回并重新取得执行权后再提交 Service 状态：

```go
func (service *PlayerService) adminReloadLogic(
    ctx context.Context,
    request admin.Request,
) (admin.Response, error) {
    var input ReloadLogicRequest
    if err := request.DecodeJSON(&input); err != nil {
        return admin.Response{}, err
    }

    var next LogicConfig
    if err := service.Await(ctx, func(waitCtx context.Context) error {
        loaded, err := loadLogicConfig(waitCtx, input.Source)
        if err == nil {
            next = loaded
        }
        return err
    }); err != nil {
        return admin.Response{}, err
    }

    service.logicConfig = next
    service.logicVersion++
    return admin.JSON(http.StatusOK, ReloadLogicResponse{
        Version: service.logicVersion,
    })
}
```

使用者仍只需 `app.Setup(&PlayerService{})`。Application 构建真实实例后自动发现 Provider，
不要求业务再次注册路径或保存 Application 全局指针。

`AdminEndpoints` 在真实 Service 实例完成 Runtime 绑定后、OnInit 之前调用一次。该方法只能
声明静态 Endpoint 并返回绑定到当前实例的方法值，不能读取尚未初始化的业务字段、执行 I/O、
启动 goroutine 或修改生命周期。路由冻结后不再调用，也不允许运行期增删 Endpoint。

### 4.2 Application 自定义端点

进程入口可以在首次执行命令前登记不属于某个 Service 的端点：

```go
func (app *Application) RegisterAdminEndpoint(endpoint admin.Endpoint) error
```

路由位于 Application 自定义命名空间。Handler 在 HTTP 请求 goroutine 中并发执行，只能读取
不可变或并发安全数据，或调用 Application/Node/Service 已有的并发安全公开方法；不得直接
读写 Service 普通字段。需要 Service 数据时使用 Service Provider 端点。

### 4.3 自动路由

固定路由为：

```text
GET  /admin/v1/diagnostics
GET  /admin/v1/diagnostics?detail=full

POST /admin/v1/application/retire
POST /admin/v1/application/resume
POST /admin/v1/nodes/{node}/retire
POST /admin/v1/nodes/{node}/resume
POST /admin/v1/nodes/{node}/services/{service}/retire
POST /admin/v1/nodes/{node}/services/{service}/resume

GET  /admin/v1/application/endpoints/{endpoint}
POST /admin/v1/application/endpoints/{endpoint}
GET  /admin/v1/nodes/{node}/services/{service}/endpoints/{endpoint}
POST /admin/v1/nodes/{node}/services/{service}/endpoints/{endpoint}
```

`/admin/v1` 和 `/debug/origin` 由框架保留。业务只提供 Endpoint 名称，不提供任意绝对路径，
从结构上消除路由覆盖、通配符优先级和路径逃逸问题。

## 5. 请求执行语义

Service 端点完整流程为：

```text
HTTP 请求
→ 全局并发额度
→ 认证、授权和 RequestID
→ 方法、Content-Type、Body 与路由校验
→ 定位 Node、Service 和 Endpoint
→ 复制有界 Request
→ 投递目标 Service 的有界 FIFO
→ 取得 Service 唯一执行权
→ 执行业务 Handler，可使用 Await/RPC/Event
→ 在执行权内编码有界 Response
→ 释放执行权
→ 记录审计并返回 HTTP
```

Service Retired 时仍允许明确管理端点执行，与精确管理 RPC 语义一致。Starting、Stopping、
Stopped、Failed 或尚未建立 Scheduler 时拒绝执行。

HTTP Context、端点 Timeout 和 Service/Application 生命周期共同形成操作 Context。请求在入队
前取消时不提交；已入队但尚未执行时，任务取得执行权后先检查取消并跳过 Handler；Handler
已经提交业务修改后发生取消不自动回滚。审计必须区分未执行、执行成功、执行失败和调用方
已取消但结果未知。

同一个 Service 的 Admin、RPC、Timer、Event 和普通 Dispatch 共用现有有界队列与唯一执行槽，
不建立第二条旁路队列。队列满快速返回 429，不阻塞 HTTP goroutine等待空位。

## 6. Admin Server 生命周期与兼容

### 6.1 接口与命令行

```go
func (app *Application) StartAdminServer(address string) error
func (app *Application) StopAdminServer(ctx context.Context) error
func (app *Application) AdminAddress() (string, bool)
```

```text
--admin 127.0.0.1:6061
```

`--admin` 是唯一管理 HTTP 启动参数，固定安装 Diagnostics、生命周期控制和已注册的自定义
端点。删除 `--diagnostics`、`StartDiagnosticsServer`、`StopDiagnosticsServer`、
`DiagnosticsAddress` 和旧 `/debug/origin/diagnostics` 路由，避免两套 Listener 模式、互斥规则和
运行时外观。需要本地快照的代码继续直接调用 `Application.Diagnostics()`。

相同请求地址重复 Start 幂等成功；已启动时改用不同地址返回状态冲突，调用方必须先 Stop
再 Start。运行中允许关闭、重开 Admin Listener；路由在首次启动前已经冻结，不随 Listener
重启改变。

### 6.2 启动顺序

生命周期调整为：

```text
冻结 Service 类型目录
→ 加载配置并初始化日志等资源
→ 构建全部 Node/Service 实例并绑定 Runtime
→ 收集、校验并冻结 Admin 路由
→ 绑定 Admin Listener
→ 依次执行 Node/Service OnInit 与 OnStart
```

Listener 仍在任何业务生命周期回调前开放，因此可以观察 OnInit/OnStart；Service 路由已经
存在，但在目标 Scheduler 未就绪时返回 503。Admin 绑定失败发生在 OnInit 前，Application
按已创建资源逆序回滚。

正常停止保持 Admin 与 pprof 可用，直到全部 Node 停止完成；随后先关闭 Admin，
再关闭 pprof、Crash 和日志。Stop Context 耗尽时必须强制 Close Listener，不能泄漏端口和
goroutine。

### 6.3 pprof

pprof 不注册到 Admin Server。`--pprof`、`StartPprof`、`PprofAddress`、`StopPprof` 和独立
Listener 语义保持不变。这样持续开启的 Admin 入口不会自动扩大为可导出调用栈、Heap、CPU
Profile 和 Trace 的高敏感入口，也允许两者采用不同暴露时长和网络保护策略。

## 7. 安全、授权与审计

完整 Admin Server 包含任意业务写操作，默认策略必须从 v3.0 的“非环回警告”收紧：

1. 没有 Guard 时只允许绑定环回地址；非环回绑定直接失败；
2. CORS 默认关闭，不设置跨域允许头；
3. 不使用 Cookie Session，不从 Query 读取 Token；
4. 框架不记录 Authorization、Cookie、请求 Body、响应 Body或业务字段；
5. 所有 POST、认证失败、授权失败和异常结果都写结构化审计日志；
6. GET 只记录 RequestID、身份、端点、目标、状态、时长和响应大小。

Application 可以在启动前设置唯一 Guard：

```go
type Guard interface {
    Authorize(
        ctx context.Context,
        request *http.Request,
        operation Operation,
    ) (Principal, error)
}

func (app *Application) SetAdminGuard(guard admin.Guard) error
```

Operation 包含 Method、固定 Endpoint 名称、read/write 权限和 Application/Node/Service 目标，
不包含 Body。Guard 可以完成认证与授权，失败分别映射 401 或 403。无 Guard 的环回模式使用
固定 `local` Principal。业务需要 TLS、mTLS、OIDC 或统一网关时可在环回地址前部署受认证代理，
或实现 Guard；Origin 首版不复制完整身份系统。

## 8. HTTP 资源边界与错误映射

Admin Server 使用私有 `http.Server` 和私有 `ServeMux`，禁止 DefaultServeMux。保留已有
ReadHeader、Write、Idle 和 Header 上限，并新增全局活动请求硬上限；达到上限返回 429，不
建立无界等待队列。

错误映射固定为：

| 场景 | HTTP |
| --- | --- |
| JSON、参数、方法或 Content-Type 无效 | 400 / 405 / 415 |
| 未认证、未授权 | 401 / 403 |
| Node、Service 或 Endpoint 不存在 | 404 |
| 生命周期或状态冲突 | 409 |
| 全局过载或 Service 队列满 | 429 |
| Service 未就绪、停止或失败 | 503 |
| 操作 Deadline | 504 |
| Handler panic 或未知内部错误 | 500 |

Endpoint panic 必须在执行所在 goroutine 最外层恢复、记录稳定错误和堆栈，并只失败当前请求；
不能越过 Service Scheduler 或 net/http 边界终止进程。业务错误对外只返回稳定 code 和安全
消息，不返回第三方错误链、文件路径、玩家数据或配置内容。

## 9. Diagnostics 重构

### 9.1 Snapshot 关系

`Application.Diagnostics()` 的根 Snapshot 是“一次针对当前 Application 的聚合诊断文档”，
不是 ApplicationSnapshot 的别名：

```text
Snapshot
├── 采集元数据
├── ApplicationSnapshot：Application 自身生命周期与管理 Listener
├── RuntimeSnapshot：整个 Go 进程共享的 Runtime 数据
├── BufferPoolSnapshot：当前 Application 拥有的 Pool
└── NodeSnapshot[]：当前 Application 拥有的 Node
    └── ServiceSnapshot[]：当前 Node 拥有的 Service
```

ApplicationSnapshot 不重复 Node 健康、RPC 或 Service 数据。多个 Application 位于同一进程
时，它们查询到的 RuntimeSnapshot 可以相同，这一点必须在教程中明确。

### 9.2 Summary 与 Full

新的 Admin Diagnostics 默认返回独立的低成本 `diagnostics.Summary`：

```text
GET /admin/v1/diagnostics
```

Summary 包含：

- Application 名称、状态、Admin 和 pprof Listener 状态；
- Go Runtime goroutine、runnable goroutine、GOMAXPROCS、Go 管理内存、Heap、内存上限、
  分配累计、GC 次数、GC 暂停累计、GC CPU 累计和 Mutex 等待累计；
- BufferPool 当前使用汇总；
- 每个 Node 的生命周期、Health、Transport、Discovery 和目录数量；
- 每个 Node 的 Local/TCP/NATS RPC 积压、吞吐、失败、超时、拒绝和字节累计；
- 每个 Node 的 Service 状态数量，以及 Scheduler、Timer、Event 的有界汇总。

Summary 不输出逐 Service 名称和完整明细，因此大小随 Node 数增长，不随每个 Service 的字段
数量线性膨胀。采集仍需顺序读取 Service 叶子并汇总，但不为每个 Service 分配输出 DTO。

人工排障显式请求 Full：

```text
GET /admin/v1/diagnostics?detail=full
```

Full 复用 `Application.Diagnostics()` 的 v3.0 Snapshot v2，保留现有 Go 快照类型和 JSON
字段含义。非法 detail 返回 400。

### 9.3 字段兼容与修正

v3.0 Snapshot 中已公开的字段不删除。RPC Full 中与 Node Transport 重复的 Reconnects 和
ConsecutiveFailures 标记 Deprecated，新 Summary 只在 Node Transport 保存这一口径。

当前 `RuntimeSnapshot.MemoryLimitBytes` 未赋值的问题必须修复；同时增加 Go Runtime 实际管理
内存口径。教程必须说明这些字段不等于 OS RSS、容器内存或进程总 CPU，系统级资源仍由
宿主机、容器或外部监控采集。

Admin Listener 空闲时不执行周期采样，只有 Listener、HTTP Server 和等待 goroutine；一次
查询会读取 Runtime、聚合 Node/Service/RPC/Timer/Event 并编码 JSON，不得描述成“只查询
内存”或“完全没有开销”。Summary 适合秒级采集，Full 只用于按需排障。

## 10. 测试、Benchmark 与验收

实施使用测试驱动，至少覆盖：

### 10.1 `admin` 包

- GET/POST、名称和 Option 的正常与全部非法边界；
- JSON 空值、未知字段、尾随值、类型错误、nil 目标和大小边界；
- JSON/Empty Response、非法状态码、编码失败和响应上限；
- Request、Header、Query、Body 与 Principal 的独立所有权；
- Handler panic、Context 取消和 Deadline。

### 10.2 Application Admin Server

- 私有 ServeMux、固定路由、方法约束、保留路径和重复 Endpoint；
- `--admin` 解析、启动和删除 `--diagnostics` 后的未知参数错误；
- Start/Stop/Restart、`:0` 实际地址、并发启停、绑定失败和异常 Serve 退出；
- 无 Guard 环回成功、非环回失败、Guard 401/403/成功和 Principal 传播；
- 全局并发上限、Body/Response/Header/Timeout 上限和客户端取消；
- Application/Node/Service Retire、Resume 的成功、幂等、未知目标和部分失败；
- Application 自定义 GET/POST 与审计字段脱敏。

### 10.3 Service 调度桥

- GET 和 POST 确实在目标 Service 唯一执行槽执行；
- HTTP 并发查询/修改与普通 Task、Timer、Event、RPC 并行时无数据竞争；
- Retired 可管理，Starting/Stopping/Stopped/Failed 拒绝；
- 队列满 429、取消前不执行、执行中取消、提交后不回滚；
- Handler 内 Await 能释放并恢复执行权，不形成死锁；
- 响应在执行槽内编码，返回可变 Slice/Map 后无竞态；
- panic 隔离、错误码映射、请求完成严格一次和 goroutine 回收。

### 10.4 Diagnostics

- Summary 聚合、零 Node/Service、失败对象、多 Application 共享 Runtime 说明；
- Summary 与 Full 共同叶子字段口径一致；
- MemoryLimitBytes 和 Go 管理内存正确；
- RPC 重复恢复字段不进入 Summary；
- 0/1/64 Node、每 Node 0/1/64 Service 的 Summary/Full/JSON Benchmark；
- Benchmark 保存 `ns/op`、`B/op`、`allocs/op` 和响应字节数。

### 10.5 门禁

- 相关包普通测试与逐函数覆盖率检查；
- `go test -race` 覆盖 Admin HTTP 与 Service 并发；
- 全仓 `go test ./...`；
- `go vet ./...`、`gofmt`、`git diff --check`；
- Windows 当前平台测试，并至少完成 Linux/macOS 交叉构建；
- `go build ./...` 确认全部 examples 可构建；
- 对无法稳定注入的系统故障分支记录原因和剩余风险，不用无断言测试追求百分比。

## 11. 第 10 章与 examples

第 10 章重构为“Admin 管理 HTTP、Diagnostics 与 pprof”，章节顺序为：

1. Admin Server 的定位、启动、安全和空闲/请求开销；
2. Service GET/POST 端点：查询、修改、重载、异步通知和 Await；
3. Application 自定义端点；
4. Application、Node、Service Retire/Resume 内置控制；
5. 请求并发、取消、Deadline、队列满、错误码和审计；
6. Diagnostics Summary/Full、Application/Runtime/Node/Service 快照关系和监控字段；
7. pprof 独立 Listener、运行期关闭/重开/再次关闭的完整代码；
8. 业务监控适配器与外部系统资源监控边界。

原 `examples/10-diagnostics-and-pprof` 重组为 `examples/10-admin-diagnostics-and-pprof`，至少包含：

```text
01-admin-service-endpoints     GET 查询、POST 修改、Await 重载和异步通知
02-admin-application-control  Application 自定义端点与内置 Retire/Resume
03-diagnostics-snapshot       本地 Full 快照
04-admin-diagnostics          Summary/Full HTTP 与采集成本说明
05-pprof-toggle               --pprof 初始状态和运行期两次关闭、一次重开
06-metrics-adapter            自定义监控映射与采集缓存
```

每个示例必须包含 `main.go`、README、配置、Windows/Linux 运行脚本和必要的测试。根 examples
索引、教程链接、API 索引和旧目录引用必须同步更新；旧 v3.0 基线正文只修正失效链接，新增
教程写入 v3.1 maintenance，避免把新功能回填冻结基线。

## 12. 调研依据

- Go `net/http` Handler 并发执行且 Request/ResponseWriter 具有明确请求生命周期，因此不能把
  原始 HTTP 对象交给 Service 延后保存；
- Erlang gen_server 的同步 call/异步 cast 模型证明外部入口应把操作消息化后交给状态所有者，
  而不是并发直接访问内部状态；
- Spring Boot Actuator 把管理端点与普通业务入口分开，支持自定义读写操作、显式暴露和访问
  控制；本设计保留其管理控制面思想，但把使用者外观压缩为 GET/POST；
- Envoy Admin 明确同时包含只读信息与破坏性控制并要求安全网络保护；
- Kubernetes API 采用先认证、再授权、默认拒绝的控制面边界；Origin 首版用 Guard 和环回
  默认策略实现较小但可审计的同类边界。
