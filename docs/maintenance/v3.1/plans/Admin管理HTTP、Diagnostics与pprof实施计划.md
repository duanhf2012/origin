# Admin 管理 HTTP、Diagnostics 与 pprof 实施计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

> - 状态：已完成，验收通过（2026-08-09）
> - 目标版本：v3.1.0
> - 兼容性：删除 v3.0 的 `--diagnostics` 与独立 Diagnostics HTTP API，保留进程内快照 API并迁移到 `--admin` 内置路由
> - 基线：v3.0
> 设计：[Admin 管理 HTTP、Diagnostics 与 pprof 设计](../design/Admin管理HTTP、Diagnostics与pprof设计.md)

**Goal:** 用唯一 `--admin` Listener 提供内置 Diagnostics、Application/Node/Service 控制和业务 GET/POST 管理端点，并把 Service 数据操作安全串行化到目标 Service。

**Architecture:** Application 拥有 Admin HTTP 生命周期、冻结路由、安全、限流和审计；业务 Service 通过 `admin.Provider` 声明 GET/POST Endpoint，HTTP 请求通过目标 Service 现有有界 FIFO 取得唯一执行权。Diagnostics 默认返回按 Node 聚合的 Summary，显式 `detail=full` 复用完整快照；pprof 保持独立 Listener。

**Tech Stack:** Go 1.26.5、标准库 `net/http`/`runtime/metrics`/`encoding/json`、Origin Application/Node/Service Scheduler、现有 `errs` 与日志 Runtime；不增加第三方依赖。

## Global Constraints

- 基线为 v3.0，目标为 v3.1.0；只实现设计文档已经确认的范围。
- 删除 `--diagnostics`、独立 Diagnostics HTTP API 和旧 `/debug/origin/diagnostics`；保留本地 `Application.Diagnostics()`。
- Admin 只公开 GET/POST；GET 无副作用，POST 承载修改和动作。
- Service Endpoint 不接收原始 `ResponseWriter`、`*http.Request` 或流式 Body。
- Admin Server 无 Guard 时只允许环回监听；pprof 始终使用独立 Listener。
- Endpoint Timeout 默认 15 秒，请求 Body 默认 1 MiB，响应 Body 默认 4 MiB，全局并发默认 64。
- 不使用 `http.DefaultServeMux`、包级可变全局状态、无界队列、辅助 fire-and-forget goroutine 或运行期路由注册。
- 业务 Handler 返回的数据必须在持有 Service 执行权时编码为独立响应字节。
- 所有新增和修改的 Go 代码使用详细中文 GoDoc/步骤注释，并通过 gofmt。
- 每个实现 Task 遵循红—绿—重构；并发生命周期代码运行 `go test -race`。
- 保留用户当前工作树中的其他改动，只提交当前 Task 明确列出的文件。

---

## File Structure

### 新增公共 Admin 模型

- `admin/endpoint.go`：GET/POST Endpoint、Option、Provider 和校验。
- `admin/request.go`：脱离网络生命周期的 Request、Principal 与严格 JSON 解码。
- `admin/response.go`：不可变编码响应及 JSON/Empty 构造。
- `admin/security.go`：Operation、Guard 和认证/授权错误分类。
- `admin/invoke.go`：把 Endpoint 投递到目标 Service Scheduler 并同步等待结果。
- `admin/*_test.go`：值语义、边界、取消、panic、并发和执行权测试。

### Application 管理 HTTP

- `application/admin_registry.go`：Application Endpoint/Guard 冷注册、Service Provider 收集和冻结路由表。
- `application/admin_server.go`：Admin Server 生命周期、私有 ServeMux、请求限制、Guard、审计、错误映射和响应输出。
- `application/admin_builtin.go`：Diagnostics 和 Application/Node/Service Retire/Resume 固定路由。
- `application/admin_*_test.go`：注册、Server、安全、控制、并发和资源退出测试。
- `application/http_runtime.go`：继续作为 Admin 与 pprof 两个独立 Server 的通用资源控制器。
- `application/application.go`：字段、构建/路由冻结/Listener/Node 生命周期顺序和清理顺序。
- `application/diagnostics.go`：Full Runtime 字段修正和 Summary 聚合。
- `application/diagnostics_benchmark_test.go`：Summary/Full 聚合基准。
- `application/diagnostics_server.go`、`application/diagnostics_server_test.go`：删除，由 Admin Server 取代。

### Diagnostics DTO 与 Node 聚合

- `diagnostics/summary.go`、`diagnostics/summary_test.go`：低成本 Summary DTO 与 JSON 契约。
- `diagnostics/snapshot.go`：保留 Full v2，修正 GoDoc 并弃用 RPC 重复恢复字段。
- `node/diagnostics.go`：新增 `DiagnosticsSummary`，直接聚合 Service 而不建立逐 Service Slice。
- `node/diagnostics_test.go`：Summary 状态/统计/顺序/并发测试。

### 命令、Service 外观与测试替身

- `command/start.go`、`command/start_test.go`：只解析 `--admin` 与 `--pprof`。
- `service/application.go`：受限 Application 外观改为 Admin Server 方法。
- `service/service.go`：`IService` 增加现有实例已经实现的 `Name()` 查询，供冻结路由使用。
- `service/application_test.go`、`node/application_runtime_test.go`：同步测试替身。

### 教程与示例

- `examples/10-admin-diagnostics-and-pprof/**`：六组完整可运行示例。
- `examples/10-diagnostics-and-pprof/**`：迁移后删除。
- `docs/maintenance/v3.1/guides/10.admin-diagnostics-and-pprof.md`：新的完整第 10 章。
- `docs/maintenance/v3.1/guides/README.md`、`docs/maintenance/v3.1/README.md`、`examples/README.md`、`README.md`：索引更新。
- `docs/maintenance/v3.1/changes/Admin管理HTTP、Diagnostics与pprof变更摘要.md`：最终使用者变更。
- `docs/maintenance/v3.1/reports/Admin管理HTTP、Diagnostics与pprof验收报告.md`：测试、Race、Benchmark 和剩余风险。

---

### Task 1: Admin Endpoint、Request、Response 与 Guard 值模型

**Files:**
- Create: `admin/endpoint.go`
- Create: `admin/request.go`
- Create: `admin/response.go`
- Create: `admin/security.go`
- Create: `admin/endpoint_test.go`
- Create: `admin/request_test.go`
- Create: `admin/response_test.go`

**Interfaces:**
- Consumes: `errs.Code` 与标准库 HTTP/JSON 类型。
- Produces: `admin.Provider`、`admin.Handler`、`admin.Get`、`admin.Post`、`admin.Endpoint`、`admin.Request`、`admin.Response`、`admin.JSON`、`admin.Empty`、`admin.Guard`、`admin.Operation`、`admin.Principal`。

- [ ] **Step 1: 写 Endpoint 构造和校验失败测试**

```go
func TestEndpointGetPostAndValidation(t *testing.T) {
    handler := func(context.Context, Request) (Response, error) {
        return Empty(http.StatusNoContent), nil
    }
    get := Get("summary", handler)
    post := Post("reload-logic", handler,
        WithTimeout(3*time.Second),
        WithMaxBodyBytes(1024),
        WithMaxResponseBytes(2048),
        WithSuccessStatus(http.StatusAccepted),
    )
    if err := get.Validate(); err != nil || get.Method() != http.MethodGet {
        t.Fatalf("GET endpoint = %+v, %v", get, err)
    }
    if err := post.Validate(); err != nil || post.Method() != http.MethodPost {
        t.Fatalf("POST endpoint = %+v, %v", post, err)
    }
    for _, endpoint := range []Endpoint{
        Get("", handler),
        Get("Bad_Name", handler),
        Post("missing", nil),
        Post("bad-timeout", handler, WithTimeout(-time.Second)),
    } {
        if endpoint.Validate() == nil {
            t.Fatalf("Endpoint %+v unexpectedly valid", endpoint)
        }
    }
}
```

- [ ] **Step 2: 运行测试并确认因 `admin` API 不存在而失败**

Run: `go test ./admin -run 'TestEndpointGetPostAndValidation' -count=1`
Expected: FAIL，包或类型尚不存在。

- [ ] **Step 3: 实现最小 Endpoint API 和固定默认值**

```go
const (
    DefaultTimeout          = 15 * time.Second
    DefaultMaxBodyBytes     = int64(1 << 20)
    DefaultMaxResponseBytes = int64(4 << 20)
)

type Handler func(context.Context, Request) (Response, error)
type Provider interface{ AdminEndpoints() []Endpoint }

func Get(name string, handler Handler, options ...Option) Endpoint
func Post(name string, handler Handler, options ...Option) Endpoint

func (endpoint Endpoint) Validate() error
func (endpoint Endpoint) Name() string
func (endpoint Endpoint) Method() string
func (endpoint Endpoint) Timeout() time.Duration
func (endpoint Endpoint) MaxBodyBytes() int64
func (endpoint Endpoint) MaxResponseBytes() int64
func (endpoint Endpoint) SuccessStatus() int
func (endpoint Endpoint) Invoke(context.Context, Request) (Response, error)
```

GET 固定拒绝非零 Body 上限，默认 200；POST 默认 204。Option 错误保存到 Endpoint，在
`Validate` 时返回，不 panic。`Invoke` 只调用已冻结 Handler，在 Handler 最外层恢复 panic 并转换为
`errs.CodeInternal`，供 Application Endpoint 和 Service Endpoint 共用同一完成语义。

- [ ] **Step 4: 写 Request 独立所有权和严格 JSON 测试**

```go
func TestRequestCopiesInputAndDecodesOneStrictJSONValue(t *testing.T) {
    query := url.Values{"mode": {"safe"}}
    header := http.Header{"X-Test": {"one"}}
    body := []byte(`{"version":42}`)
    request := NewRequest("req-1", Principal{Subject: "operator"}, query, header, body)
    query.Set("mode", "changed")
    header.Set("X-Test", "changed")
    body[0] = '['

    var input struct { Version uint64 `json:"version"` }
    if err := request.DecodeJSON(&input); err != nil || input.Version != 42 {
        t.Fatalf("DecodeJSON() = %+v, %v", input, err)
    }
    for _, payload := range [][]byte{
        nil,
        []byte(`{"unknown":1}`),
        []byte(`{"version":1}{"version":2}`),
    } {
        invalid := NewRequest("req", Principal{}, nil, nil, payload)
        if invalid.DecodeJSON(&input) == nil {
            t.Fatalf("payload %q unexpectedly decoded", payload)
        }
    }
}
```

- [ ] **Step 5: 实现 Request、Principal 和严格解码**

`NewRequest` 深复制 Query、Header、Body 和 Principal 属性。`DecodeJSON` 使用
`json.Decoder.DisallowUnknownFields`，要求恰好一个 JSON 值，并把全部输入错误包装为
`errs.CodeInvalidArgument`。

- [ ] **Step 6: 写 Response 编码、状态和大小测试**

```go
func TestResponseJSONAndEmpty(t *testing.T) {
    response, err := JSON(http.StatusOK, map[string]int{"value": 7})
    if err != nil || response.Status() != http.StatusOK ||
        string(response.Body()) != "{\"value\":7}" {
        t.Fatalf("JSON() = %+v, %v", response, err)
    }
    if response := Empty(http.StatusAccepted); response.Status() != http.StatusAccepted || len(response.Body()) != 0 {
        t.Fatalf("Empty() = %+v", response)
    }
    if _, err := JSON(http.StatusBadRequest, struct{}{}); err == nil {
        t.Fatal("JSON accepted non-2xx status")
    }
}
```

- [ ] **Step 7: 实现不可变 Response 和安全访问器**

Response 字段保持私有；`Body()`、`Header()` 返回独立副本，Application 内部通过只读内部访问
函数取得编码结果。JSON 编码失败返回稳定内部错误，不包含原值。

- [ ] **Step 8: 实现 Guard/Operation 并运行包测试**

```go
type Principal struct {
    Subject    string
    Roles      []string
    Attributes map[string]string
}

type Operation struct {
    Method      string
    Endpoint    string
    NodeID      string
    ServiceName string
}

type Guard interface {
    Authorize(context.Context, *http.Request, Operation) (Principal, error)
}

var ErrUnauthenticated error
var ErrForbidden error
```

Operation 不再额外暴露 Read/Write/Action；Guard 直接按 `Method` 的 GET/POST 做授权。两个哨兵错误
只负责稳定区分 401/403，不携带 Token、Cookie 或内部错误链。

Run: `go test ./admin -count=1`
Expected: PASS。

- [ ] **Step 9: 提交 Admin 值模型**

```powershell
git add admin
git commit -m "feat: 增加Admin端点值模型"
```

---

### Task 2: Service Endpoint 串行执行桥

**Files:**
- Create: `admin/invoke.go`
- Create: `admin/invoke_test.go`
- Modify: `service/service.go`
- Modify: `service/service_test.go`

**Interfaces:**
- Consumes: Task 1 `Endpoint.Invoke`、`Request`、`Response`；现有 `service.IService.DispatchAsync`、`Await` 和生命周期状态。
- Produces: `admin.InvokeService(ctx, target, endpoint, request) (Response, error)`；`service.IService.Name() string`。

- [ ] **Step 1: 先把现有 `Name()` 加入 IService 并验证所有实现**

```go
type IService interface {
    Name() string
    // 保留原有全部方法。
}
```

Run: `go test ./service ./node ./application -run '^$'`
Expected: PASS；真实 Service 通过嵌入的现有 `Name()` 满足接口，测试替身按编译错误逐个补齐。

- [ ] **Step 2: 写 Handler 必须在唯一执行槽运行的失败测试**

```go
func TestInvokeServiceSerializesConcurrentMutation(t *testing.T) {
    target := startAdminInvokeService(t)
    endpoint := Post("increment", func(ctx context.Context, _ Request) (Response, error) {
        if err := target.Await(ctx, func(context.Context) error { return nil }); err != nil {
            return Response{}, fmt.Errorf("admin handler does not own service task: %w", err)
        }
        current := target.value
        runtime.Gosched()
        target.value = current + 1
        return Empty(http.StatusNoContent), nil
    })
    const calls = 128
    runConcurrentInvocations(t, calls, func() error {
        _, err := InvokeService(context.Background(), target, endpoint, Request{})
        return err
    })
    if target.value != calls {
        t.Fatalf("value = %d", target.value)
    }
}
```

- [ ] **Step 3: 运行测试并确认 InvokeService 不存在**

Run: `go test ./admin -run 'TestInvokeServiceSerializesConcurrentMutation' -count=1`
Expected: FAIL。

- [ ] **Step 4: 实现 DispatchAsync + 有界结果 Channel 的最小桥**

```go
func InvokeService(
    callerCtx context.Context,
    target service.IService,
    endpoint Endpoint,
    request Request,
) (Response, error)
```

投递的闭包从 Scheduler `taskCtx` 派生执行 Context，并用 `context.AfterFunc(callerCtx, cancel)`
传播客户端取消，同时保留 taskCtx 的 Service 执行身份，使 Handler 内 `Await` 能正常释放和恢复
执行权。结果 Channel 容量固定为 1，调用方取消后任务发送结果不会阻塞或泄漏。

- [ ] **Step 5: 写取消、队列满、Retired 和停止状态测试**

覆盖以下断言：

```go
// 调用前取消：Handler 不执行，返回 CodeCanceled。
// Service Retired：仍能执行明确管理 Endpoint。
// Scheduler 队列满：InvokeService 返回 ErrServiceQueueFull。
// Service Stopping/Stopped/Failed：返回对应稳定生命周期错误。
// 调用方在 Handler 已提交修改后取消：值保持已提交，不自动回滚。
```

Run: `go test ./admin -run 'TestInvokeService(Canceled|QueueFull|Retired|Stopped)' -count=1`
Expected: 初次 FAIL，最小实现后 PASS。

- [ ] **Step 6: 写 Handler 内 Await 可恢复且不死锁的测试**

Handler 使用 `target.Await(ctx, func(waitCtx context.Context) error { ... })` 等待受控 Channel；
等待期间投递另一个普通 Task 修改版本，恢复后断言 Handler 重新取得执行权并看到新版本。

Run: `go test ./admin -run 'TestInvokeServiceHandlerCanAwait' -count=1 -timeout=10s`
Expected: PASS。

- [ ] **Step 7: 写 panic 严格完成一次测试并实现恢复**

Endpoint.Invoke 在业务 Handler 外层 recover，把 panic 转为 `errs.CodeInternal`；InvokeService 必须
始终向结果 Channel 发送一次终态，不能等到 HTTP Deadline 才返回。

Run: `go test ./admin -run 'TestInvokeServicePanicCompletesOnce' -count=100`
Expected: PASS，无挂起。

- [ ] **Step 8: 运行普通与 Race 测试**

Run: `go test ./admin ./service -count=1`
Expected: PASS。

Run: `go test -race ./admin ./service -run 'InvokeService' -count=1`
Expected: PASS，无 data race。

- [ ] **Step 9: 提交 Service 调度桥**

```powershell
git add admin/invoke.go admin/invoke_test.go service/service.go service/service_test.go
git commit -m "feat: 串行执行Service管理端点"
```

---

### Task 3: Application Endpoint 注册与冻结路由

**Files:**
- Create: `application/admin_registry.go`
- Create: `application/admin_registry_test.go`
- Modify: `application/application.go`
- Modify: `application/application_test.go`

**Interfaces:**
- Consumes: Task 1 `admin.Endpoint`/`Provider`/`Guard`；Task 2 `IService.Name()`。
- Produces: `Application.RegisterAdminEndpoint`、`Application.SetAdminGuard`、冻结的 Application/Service Endpoint Map。

- [ ] **Step 1: 写 Application 冷注册状态和重复名称测试**

```go
func TestRegisterAdminEndpointAndGuardBeforeCommandOnly(t *testing.T) {
    app := New()
    endpoint := admin.Get("build", func(context.Context, admin.Request) (admin.Response, error) {
        return admin.JSON(http.StatusOK, map[string]string{"version": "test"})
    })
    if err := app.RegisterAdminEndpoint(endpoint); err != nil {
        t.Fatalf("RegisterAdminEndpoint() error = %v", err)
    }
    if err := app.RegisterAdminEndpoint(endpoint); !errors.Is(err, errs.ErrInvalidArgument) {
        t.Fatalf("duplicate error = %v", err)
    }
    app.commandRun = true
    if err := app.SetAdminGuard(allowGuard{}); !errors.Is(err, errs.ErrInvalidArgument) {
        t.Fatalf("late SetAdminGuard() error = %v", err)
    }
}
```

- [ ] **Step 2: 实现 Application 冷注册字段与方法**

Application 新增：

```go
adminEndpoints []admin.Endpoint
adminGuard     admin.Guard
adminRoutes    *adminRouteTable
adminHTTP      httpRuntime
```

注册仅允许 `StateCreated && !commandRun`，Guard 只能设置一次，调用方 Slice/Endpoint 在冻结前
由 Application 自有复制保存。

- [ ] **Step 3: 写真实 Service Provider 收集测试**

建立两个 Node，各自配置同名模板但不同实际 ServiceName；Provider 返回同名 GET/POST。冻结后
断言键包含 NodeID、实际 ServiceName、Method、EndpointName，并分别绑定各自实例。

```go
type adminProviderService struct{ service.Service }
func (target *adminProviderService) AdminEndpoints() []admin.Endpoint {
    return []admin.Endpoint{admin.Get("state", target.handleState)}
}
```

- [ ] **Step 4: 实现 `freezeAdminRoutes(nodes)`**

Route Table 使用固定结构键：

```go
type serviceAdminRouteKey struct {
    nodeID, serviceName, method, endpoint string
}
type boundServiceAdminEndpoint struct {
    target   service.IService
    endpoint admin.Endpoint
}
```

对每个真实 Service 实例只调用一次 `AdminEndpoints()`；验证 Endpoint、重复键、保留名称和 Provider
panic。Provider panic 在构建冷路径恢复为配置错误，不启动 Listener。

- [ ] **Step 5: 写 Provider 不得依赖 OnInit 的生命周期测试**

Provider 记录调用时 `State()` 并断言发生在 OnInit 前；Handler 真正请求时目标已 Running。冻结
完成后再次 StartAdmin/StopAdmin 不重复调用 Provider。

- [ ] **Step 6: 运行注册测试**

Run: `go test ./application -run 'AdminRegistry|RegisterAdmin|FreezeAdmin' -count=1`
Expected: PASS。

- [ ] **Step 7: 提交注册与冻结路由**

```powershell
git add application/admin_registry.go application/admin_registry_test.go application/application.go application/application_test.go
git commit -m "feat: 冻结Application管理路由"
```

---

### Task 4: Admin Server、安全、限流与响应边界

**Files:**
- Create: `application/admin_server.go`
- Create: `application/admin_server_test.go`
- Create: `application/admin_security_test.go`
- Modify: `errs/code.go`
- Modify: `errs/errors.go`
- Modify: `errs/errors_test.go`
- Modify: `application/http_runtime.go`
- Modify: `application/http_runtime_test.go`

**Interfaces:**
- Consumes: Task 3 冻结路由和 Guard；现有 `httpRuntime.start/stop/addressSnapshot/snapshot`。
- Produces: `StartAdminServer`、`StopAdminServer`、`AdminAddress` 和私有 `/admin/v1` ServeMux。

- [ ] **Step 1: 写 Admin Server 启停、私有 Mux 和地址测试**

测试固定：未运行无地址；环回 `:0` Start 成功并返回实际端口；同地址 Start 幂等；不同地址
冲突；Stop 幂等并释放端口；Restart 成功；`http.DefaultServeMux` 前后不增加路由。

Run: `go test ./application -run 'TestAdminServerRuntimeLifecycle' -count=1`
Expected: FAIL，方法不存在。

- [ ] **Step 2: 实现最小生命周期 API**

```go
func (app *Application) StartAdminServer(address string) error
func (app *Application) StopAdminServer(ctx context.Context) error
func (app *Application) AdminAddress() (string, bool)
```

复用 `httpRuntime`，但只安装私有 Mux。Server 保留 `ReadHeaderTimeout=5s`、`WriteTimeout=20s`、
`IdleTimeout=60s`、`MaxHeaderBytes=1MiB`。新增 `CodeAdminUnavailable`/`ErrAdminUnavailable` 和
`CodeAdminStateConflict`/`ErrAdminStateConflict`；既有 Diagnostics 错误继续只服务独立 pprof，
不把新的 Admin 生命周期错误伪装成 Diagnostics 错误。

- [ ] **Step 3: 写无 Guard 环回/非环回与 Guard 传播测试**

```go
// nil Guard + 127.0.0.1:0 => Start 成功，Principal.Subject == "local"。
// nil Guard + 0.0.0.0:0 => Start 返回 ErrAdminUnavailable。
// Guard 返回 unauthenticated => 401。
// Guard 返回 forbidden => 403。
// Guard 成功 => Principal 深复制后传入 Handler。
```

为避免测试暴露真实网卡，非环回失败在 Listen 前校验地址。

- [ ] **Step 4: 实现 Guard、RequestID 和 Operation**

每个请求生成不包含业务数据的 RequestID；Guard 在读取 Body 前执行，并直接按 Operation.Method
的 GET/POST 授权，不建立第二套 Read/Write/Action 分类。无 Guard 环回使用固定 local Principal。
Guard error 用明确 sentinel 区分 401/403。

- [ ] **Step 5: 写请求方法、Content-Type、Body 和响应上限测试**

覆盖 GET Body=>400、POST 非 JSON Content-Type=>415、Body 超限=>413、第二个 JSON 值=>400、
响应超限=>500 安全错误、未知路径=>404、错误方法=>405 且 Allow 正确。

- [ ] **Step 6: 实现统一 `serveAdminEndpoint`**

```go
func (app *Application) serveAdminEndpoint(
    w http.ResponseWriter,
    r *http.Request,
    operation admin.Operation,
    endpoint admin.Endpoint,
    invoke func(context.Context, admin.Request) (admin.Response, error),
)
```

顺序固定为并发额度、Guard、方法/头/Body、Context Timeout、Invoke、响应上限、审计、输出。
使用 `http.MaxBytesReader`，不在日志打印 Body/Header 凭证。

- [ ] **Step 7: 写全局 64 并发快速拒绝测试**

用 64 个受控阻塞 Handler 占满额度，第 65 个请求必须立即 429；释放后新请求成功。测试不使用
Sleep 判断，全部通过 Channel 屏障。

- [ ] **Step 8: 实现固定容量 Semaphore 和审计**

Semaphore 只在每个 Application 内创建，不使用全局变量；获取失败立即 429。POST、认证/授权
失败、panic 和非 2xx 记录完整审计字段；GET 不记录 Query、Header、Body或响应内容。

- [ ] **Step 9: 运行普通、重复与 Race 测试**

Run: `go test ./application -run 'AdminServer|AdminSecurity|AdminLimit' -count=10`
Expected: PASS。

Run: `go test -race ./application -run 'AdminServer|AdminSecurity|AdminLimit' -count=1`
Expected: PASS。

- [ ] **Step 10: 提交 Admin Server**

```powershell
git add application/admin_server.go application/admin_server_test.go application/admin_security_test.go application/http_runtime.go application/http_runtime_test.go
git commit -m "feat: 增加有界Admin HTTP Server"
```

---

### Task 5: 内置控制与自定义 Endpoint 路由

**Files:**
- Create: `application/admin_builtin.go`
- Create: `application/admin_builtin_test.go`
- Create: `application/admin_service_test.go`
- Modify: `application/admin_server.go`

**Interfaces:**
- Consumes: Task 2 `admin.InvokeService`、Task 3 Route Table、Task 4 `serveAdminEndpoint`。
- Produces: 固定 Application/Node/Service Retire/Resume、Application Endpoint 和 Service Endpoint 路由。

- [ ] **Step 1: 写 Application 自定义 GET/POST 路由测试**

登记 `build` GET 和 `reload` POST，启动 Admin Server 后分别访问：

```text
GET  /admin/v1/application/endpoints/build
POST /admin/v1/application/endpoints/reload
```

断言 GET/POST 方法隔离、未知 Endpoint 404、Handler Principal/Query/Body 正确。

- [ ] **Step 2: 写 Service 查询和修改端到端测试**

真实 Service Provider 暴露 `state` GET 和 `increment` POST。并发发送 128 个 POST 后 GET，断言
最终值严格为 128；同时运行普通 Dispatch，Race 下无竞争。

Run: `go test ./application -run 'TestAdminServiceEndpointQueryAndMutation' -count=1`
Expected: FAIL，路由尚未接通。

- [ ] **Step 3: 实现 Application/Service 动态目标路由**

使用 Go ServeMux method+wildcard pattern；从 `Request.PathValue` 读取 Node/Service/Endpoint，随后
只查冻结 Map，不临时扫描 Service 或构建反射调用。Service 路由调用 `admin.InvokeService`。

- [ ] **Step 4: 写 Application、Node、Service Retire/Resume 测试**

覆盖六条固定 POST 路由的成功、重复幂等、未知目标、Application 非 Running、Service Failed、
发现发布失败和 Context Deadline。断言 GET 请求这些路径返回 405，控制结果不伪造回滚。

- [ ] **Step 5: 实现内置控制 Handler**

```text
POST /admin/v1/application/retire
POST /admin/v1/application/resume
POST /admin/v1/nodes/{node}/retire
POST /admin/v1/nodes/{node}/resume
POST /admin/v1/nodes/{node}/services/{service}/retire
POST /admin/v1/nodes/{node}/services/{service}/resume
```

成功无 Body 返回 204。调用现有 `Retire/Resume`，不复制生命周期实现。

- [ ] **Step 6: 写错误映射表测试并实现**

表驱动断言 invalid=400、unauthenticated=401、forbidden=403、not found=404、state conflict=409、
queue full=429、not ready/stopping/stopped/failed=503、deadline=504、panic/internal=500。

- [ ] **Step 7: 运行普通与 Race 测试**

Run: `go test ./application -run 'Admin(Builtin|Service|ApplicationEndpoint)' -count=1`
Expected: PASS。

Run: `go test -race ./application -run 'Admin(Builtin|Service)' -count=1`
Expected: PASS。

- [ ] **Step 8: 提交内置控制与业务端点**

```powershell
git add application/admin_builtin.go application/admin_builtin_test.go application/admin_service_test.go application/admin_server.go
git commit -m "feat: 接入Admin控制与Service端点"
```

---

### Task 6: Diagnostics Summary 与 Runtime 字段修正

**Files:**
- Create: `diagnostics/summary.go`
- Create: `diagnostics/summary_test.go`
- Modify: `diagnostics/snapshot.go`
- Modify: `diagnostics/snapshot_test.go`
- Modify: `node/diagnostics.go`
- Modify: `node/diagnostics_test.go`
- Modify: `application/diagnostics.go`
- Modify: `application/diagnostics_test.go`
- Modify: `application/diagnostics_benchmark_test.go`
- Modify: `application/admin_builtin.go`

**Interfaces:**
- Consumes: 现有 Full Snapshot/Node/Service/RPC 叶子统计。
- Produces: `diagnostics.Summary`、`NodeSummary`、`Application.DiagnosticsSummary()`、`Node.DiagnosticsSummary()` 和 Admin Diagnostics 路由。

- [ ] **Step 1: 写 Summary JSON 与零值契约测试**

定义并断言：

```go
type Summary struct {
    SchemaVersion uint32
    CollectedAt   time.Time
    StartedAt     time.Time
    CollectCost   Duration
    Application   ApplicationSummary
    Runtime       RuntimeSummary
    BufferPool    BufferPoolSnapshot
    Nodes         []NodeSummary
}
```

JSON 顶层使用 `schema_version`、`collected_at`、`started_at`、`collect_cost`、`application`、
`runtime`、`buffer_pool`、`nodes`；空 Nodes 编码为 `[]`，不是 null。

- [ ] **Step 2: 定义低基数 Runtime/Node/Service 汇总 DTO**

RuntimeSummary 固定包含 goroutines、runnable_goroutines、gomaxprocs、go_memory_used_bytes、
memory_limit_bytes、heap_alloc_bytes、heap_objects、total_alloc_bytes、gc_cycles、gc_pause_total、
gc_cpu_seconds_total、mutex_wait_seconds_total。

NodeSummary 保留 Health/Transport/Discovery/Directory 和不含 Reconnects 重复字段的 RPCSummary；
ServiceAggregate 保存状态数量、Accepted/Ready/Running/Awaiting、Rejected/Panic、Timer
Active/DuePending/Ready/Running/Rejected/Panic、Event HandlerFailure 累计。

- [ ] **Step 3: 写 Node Summary 不分配逐 Service DTO 的测试**

构造 64 个 Service，断言 Summary 只有一个 ServiceAggregate，没有 `[]ServiceSnapshot`；所有
状态数量和累计值等于逐叶子人工期望。并发 Retire/Resume/Timer/Event 时反复采集，Race 安全。

- [ ] **Step 4: 实现 `Node.DiagnosticsSummary()`**

直接遍历静态 `node.services`，每个 Service 读取 State、ExecutionStats、TimerStats、EventStats
并累加；不调用 Full `Diagnostics()`，不建立中间 Service Slice。RPC 使用固定三个类别映射。

- [ ] **Step 5: 写 Runtime 修正和 Summary 指标测试**

断言 Full `MemoryLimitBytes > 0`；Summary `GoMemoryUsedBytes == MemStats.Sys-MemStats.HeapReleased`；
runtime/metrics 缺失或 KindBad 时返回零值而不 panic。使用当前 Go 1.26.5 指标名：

```text
/sched/goroutines/runnable:goroutines
/cpu/classes/gc/total:cpu-seconds
/sync/mutex/wait/total:seconds
/gc/gomemlimit:bytes
```

- [ ] **Step 6: 实现 Application Summary 聚合**

Application 只在锁内复制 app 身份、Admin/pprof 快照、Node 指针和 BufferPool 指针，释放锁后
采集 Runtime、Pool 和 Node Summary。Full 保持 Schema v2；RPC Full 重复恢复字段只加
Deprecated GoDoc，不删除字段。

- [ ] **Step 7: 接入 Admin Diagnostics 路由**

```text
GET /admin/v1/diagnostics              => Summary
GET /admin/v1/diagnostics?detail=full  => Full Snapshot v2
GET /admin/v1/diagnostics?detail=x     => 400
POST /admin/v1/diagnostics             => 405
```

编码错误在写 Header 前处理，避免先返回 200 再静默失败。

- [ ] **Step 8: 扩充 Summary/Full/JSON Benchmark**

保留 0/1/64 Node × 0/1/64 Service 矩阵，分别运行 Summary、Full、SummaryJSON、FullJSON；
`b.ReportAllocs()` 并用 `b.ReportMetric(float64(len(payload)), "response-bytes")` 保存响应大小。

Run: `go test ./application -run '^$' -bench 'Diagnostics(Summary|Full)' -benchmem -count=3`
Expected: Summary 在 64×64 场景不产生按 Service DTO 的约 1.43 MiB 分配，响应显著小于 Full。

- [ ] **Step 9: 运行 Diagnostics 普通与 Race 测试**

Run: `go test ./diagnostics ./node ./application -run 'Diagnostics|Summary' -count=1`
Expected: PASS。

Run: `go test -race ./node ./application -run 'Diagnostics|Summary' -count=1`
Expected: PASS。

- [ ] **Step 10: 提交 Diagnostics Summary**

```powershell
git add diagnostics node/diagnostics.go node/diagnostics_test.go application/diagnostics.go application/diagnostics_test.go application/diagnostics_benchmark_test.go application/admin_builtin.go
git commit -m "feat: 增加低成本Diagnostics Summary"
```

---

### Task 7: `--admin`、Application 生命周期与旧 Diagnostics HTTP 删除

**Files:**
- Modify: `command/start.go`
- Modify: `command/start_test.go`
- Modify: `application/application.go`
- Modify: `application/application_test.go`
- Modify: `application/http_lifecycle_test.go`
- Modify: `application/pprof_test.go`
- Modify: `service/application.go`
- Modify: `service/application_test.go`
- Modify: `node/application_runtime_test.go`
- Delete: `application/diagnostics_server.go`
- Delete: `application/diagnostics_server_test.go`

**Interfaces:**
- Consumes: Tasks 3–6 完整 Admin Server 和冻结路由。
- Produces: 唯一 `--admin` 启动外观；Service `ApplicationRuntime` 的 Admin/pprof 能力；最终资源顺序。

- [ ] **Step 1: 把命令测试改为只接受 `--admin`**

StartRequest 使用：

```go
type StartRequest struct {
    AdminAddress string
    PprofAddress string
    // 保留其他字段。
}
```

断言 `--admin 127.0.0.1:6061` 解析成功、空值/重复值失败、`--diagnostics` 返回未知参数 usage
错误、`--admin` 与 `--pprof` 可同时存在。

- [ ] **Step 2: 运行命令测试确认旧实现失败**

Run: `go test ./command -run 'Start.*Admin|Diagnostics' -count=1`
Expected: FAIL，旧字段/flag 仍存在。

- [ ] **Step 3: 实现 CLI 删除与 AdminAddress**

删除 DiagnosticsAddress flag/字段/帮助文本；新增 AdminAddress。错误消息不得把地址凭证或其他
环境值写入日志。

- [ ] **Step 4: 写新的启动顺序测试**

测试 Channel 记录：Build 后 Provider 已调用，Admin Listener 已可访问，随后才进入 OnInit 和
OnStart。Admin bind 失败时任何 Service 生命周期都未进入，已构建 Node 通过 Rollback 清理。

- [ ] **Step 5: 调整 Application.run 顺序**

```text
initializeResources
→ select/buildNodes
→ app.nodes = nodes
→ freezeAdminRoutes
→ StartAdminServer（请求指定时）
→ startNodes
```

任何步骤失败都复用现有 defer/closeResources/rollbackStartup 逆序清理。正常停止在 Node 完成后
关闭 Admin，再关闭 pprof。

- [ ] **Step 6: 更新 Service ApplicationRuntime 外观与替身**

```go
type ApplicationRuntime interface {
    diagnostics.Source
    StartAdminServer(string) error
    StopAdminServer(context.Context) error
    AdminAddress() (string, bool)
    StartPprof(string) error
    StopPprof(context.Context) error
    PprofAddress() (string, bool)
}
```

删除三个 Diagnostics Server 方法并更新 service/node/application 测试替身。

- [ ] **Step 7: 删除旧 Diagnostics Server 文件并更新状态快照**

ApplicationSnapshot 用 `AdminServer ServerSnapshot` 取代 `DiagnosticsServer`；更新 nil、启动、停止
和失败测试。旧 HTTP Path 常量与只读 Handler 不得残留。

- [ ] **Step 8: 运行命令、Application 与 pprof 生命周期测试**

Run: `go test ./command ./service ./node ./application -run 'Admin|Diagnostics|Pprof|HTTP|ResourceOrder' -count=1`
Expected: PASS。

Run: `go test -race ./application -run 'Admin|Pprof|HTTP' -count=1`
Expected: PASS。

- [ ] **Step 9: 提交唯一 Admin 启动入口**

```powershell
git add command application service node/application_runtime_test.go
git commit -m "feat: 以Admin取代Diagnostics HTTP入口"
```

---

### Task 8: 六组完整 Chapter 10 Examples

**Files:**
- Move/Delete: `examples/10-diagnostics-and-pprof/**`
- Create: `examples/10-admin-diagnostics-and-pprof/README.md`
- Create: `examples/10-admin-diagnostics-and-pprof/01-admin-service-endpoints/**`
- Create: `examples/10-admin-diagnostics-and-pprof/02-admin-application-control/**`
- Create: `examples/10-admin-diagnostics-and-pprof/03-diagnostics-snapshot/**`
- Create: `examples/10-admin-diagnostics-and-pprof/04-admin-diagnostics/**`
- Create: `examples/10-admin-diagnostics-and-pprof/05-pprof-toggle/**`
- Create: `examples/10-admin-diagnostics-and-pprof/06-metrics-adapter/**`
- Modify: `examples/README.md`

**Interfaces:**
- Consumes: 完整 Admin/Diagnostics/pprof 公共 API。
- Produces: 可在 Windows/Linux 独立运行并参与根 module 构建的示例。

- [ ] **Step 1: 先为 Service GET/POST 示例写测试**

`01-admin-service-endpoints/main_test.go` 直接测试 Provider：GET 返回初始数据；POST 修改版本；
Await 重载只在返回后提交；异步通知返回 202；并发 POST 最终值确定。

- [ ] **Step 2: 实现 `01-admin-service-endpoints`**

示例提供 `summary` GET、`reload-logic` POST 和 `refresh-player` POST，完整展示
`AdminEndpoints`、`Request.DecodeJSON`、`admin.JSON/Empty`、Await 局部变量规则和日志。run 脚本
使用：

```text
--admin 127.0.0.1:6061
```

README 提供可复制 curl 请求和响应。

- [ ] **Step 3: 实现 `02-admin-application-control`**

程序入口注册 Application GET/POST Endpoint；README 演示内置 Application/Node/Service
Retire/Resume，说明幂等、Retired 仍可精确管理和 POST 审计。

- [ ] **Step 4: 迁移 `03-diagnostics-snapshot`**

基于旧 01 示例保留本地 `Application.Diagnostics()`，更新 AdminServer 字段和 Snapshot/Runtime/
Node 所有权说明；不启动 HTTP。

- [ ] **Step 5: 实现 `04-admin-diagnostics`**

替换旧独立 Diagnostics Server 示例，展示 Summary、`detail=full`、秒级采集、空闲不采样和一次
请求实际聚合/编码的成本；run 脚本只使用 `--admin`。

- [ ] **Step 6: 迁移并强化 `05-pprof-toggle`**

保留 `--pprof` 初始启动，代码在 2 秒关闭、4 秒重开、6 秒再次关闭；同时可启动 `--admin`，
证明两 Listener 独立。README 提供 CPU、Heap、goroutine、mutex 请求及短时采集说明。

- [ ] **Step 7: 迁移 `06-metrics-adapter`**

默认读取 Diagnostics Summary，缓存一次采集结果供多个消费者使用；说明 OS RSS、容器内存和
进程 CPU 由外部监控提供，不把 pprof 当作 Metrics。

- [ ] **Step 8: 为每组补齐 README、配置和运行脚本**

每组包含 `README.md`、`config/application.yaml`、`run.bat`、`run.sh`；Go 代码有中文注释，脚本
无旧 `--diagnostics`。根 `examples/README.md` 链接新目录。

- [ ] **Step 9: 构建和测试全部示例**

Run: `go test ./examples/10-admin-diagnostics-and-pprof/... -count=1`
Expected: PASS。

Run: `go build ./examples/10-admin-diagnostics-and-pprof/...`
Expected: PASS。

Run: `rg -n -- '--diagnostics|10-diagnostics-and-pprof' examples`
Expected: 无残留。

- [ ] **Step 10: 提交 Examples**

```powershell
git add examples/10-admin-diagnostics-and-pprof examples/README.md
git add -u examples/10-diagnostics-and-pprof
git commit -m "examples: 完善Admin与诊断示例"
```

---

### Task 9: 重写第 10 章与全部索引

**Files:**
- Create: `docs/maintenance/v3.1/guides/10.admin-diagnostics-and-pprof.md`
- Modify: `docs/maintenance/v3.1/guides/README.md`
- Modify: `docs/maintenance/v3.1/README.md`
- Modify: `docs/baseline/v3.0/guides/10.diagnostics-and-pprof.md`
- Modify: `docs/baseline/v3.0/guides/reference/api-index.md`
- Modify: `README.md`
- Create: `docs/maintenance/v3.1/changes/Admin管理HTTP、Diagnostics与pprof变更摘要.md`

**Interfaces:**
- Consumes: 已验证的最终 API、examples 路径和 Benchmark 数字。
- Produces: 第 10 章完整使用说明、兼容/迁移说明和可点击索引。

- [ ] **Step 1: 按已确认顺序建立第 10 章正文**

正文必须依次覆盖：Admin 定位和启动；安全/空闲开销；Service GET/POST；Application Endpoint；
内置 Retire/Resume；并发/取消/错误；Diagnostics Summary/Full 与快照关系；pprof 动态代码；
监控适配器。

- [ ] **Step 2: 写清 HTTP 性能语义**

明确写出：Listener 空闲时不采样，但并非绝对零成本；每次请求读取 Runtime、聚合 Node/Service/
RPC/Timer/Event 并编码 JSON；Summary 秒级监控，Full 按需排障；查询不只是“读内存”。只引用
Task 6 当前机器实测基准，不做跨机器承诺。

- [ ] **Step 3: 写完整 Service 使用代码**

教程必须包含 `AdminEndpoints`、GET 查询、POST 修改、Await 重载、202 异步通知、返回值在执行
槽内编码、Retired/停止/队列满语义和对应 examples 链接。

- [ ] **Step 4: 把 Diagnostics 与 pprof 作为章节小节**

Diagnostics 小节解释 Snapshot 根、Application 自身、进程 Runtime、Application 所有 Nodes 和
Node 所有 Services 的关系；pprof 小节给出 Start/PprofAddress/Stop 的关闭—重开—关闭代码，
并说明 CPU/Trace 进程互斥和采集开销。

- [ ] **Step 5: 更新冻结 v3.0 文档的最小事实与链接**

v3.0 基线不回填新功能，只在原第 10 章顶部增加指向 v3.1 新教程的维护说明，并修复迁移后失效
的 example 链接。API index 明确 v3.1 替换项，不重写其他章节。

- [ ] **Step 6: 更新索引与变更摘要**

同步 maintenance README、guide index、根 README、example index。变更摘要列出删除
`--diagnostics`、新增 `--admin`、Service Provider、Admin Guard、Diagnostics Summary 和 pprof
不变项。

- [ ] **Step 7: 执行链接和旧名称扫描**

Run: `rg -n -- '--diagnostics|StartDiagnosticsServer|StopDiagnosticsServer|DiagnosticsAddress|10-diagnostics-and-pprof' README.md docs examples application command service node`
Expected: 只在 v3.1 迁移说明、设计/实施计划和必要历史基线中出现，产品代码与新示例无残留。

Run: `git diff --check`
Expected: 无 whitespace error。

- [ ] **Step 8: 提交教程与索引**

```powershell
git add README.md docs examples/README.md
git commit -m "docs: 重写第10章Admin与诊断教程"
```

---

### Task 10: 全量验证、覆盖率、性能与验收报告

**Files:**
- Create: `docs/maintenance/v3.1/reports/Admin管理HTTP、Diagnostics与pprof验收报告.md`
- Modify: `docs/maintenance/v3.1/README.md`
- Modify: `docs/maintenance/v3.1/plans/Admin管理HTTP、Diagnostics与pprof实施计划.md`

**Interfaces:**
- Consumes: Tasks 1–9 的全部实现、测试、examples 和文档。
- Produces: 可复验门禁记录、覆盖率/Benchmark 结论、已知风险和最终完成提交。

- [x] **Step 1: 运行格式、静态检查和全仓单元测试**

Run: `gofmt -w admin application node service command diagnostics examples/10-admin-diagnostics-and-pprof`
Expected: 无未格式化文件。

Run: `go vet ./...`
Expected: PASS。

Run: `go test ./... -count=1`
Expected: PASS。

- [x] **Step 2: 运行相关包 Race**

Run: `go test -race ./admin ./service ./node ./application ./command ./diagnostics -count=1`
Expected: PASS，无 data race。

- [x] **Step 3: 生成逐函数覆盖率并检查低覆盖路径**

Run: `go test ./admin ./application ./node ./diagnostics -coverprofile=admin-diagnostics-cover.out -count=1`

Run: `go tool cover -func=admin-diagnostics-cover.out`

检查所有 Admin 注册、Guard、方法/大小/并发/取消/错误映射、Service 调度、Summary 聚合函数；
任何可稳定触发但未覆盖的分支必须补专门断言测试。完成后删除临时 coverage 文件，不提交。

- [x] **Step 4: 运行重复并发和生命周期压力测试**

Run: `go test ./admin ./application -run 'Admin|InvokeService' -count=100 -timeout=10m`
Expected: PASS，无偶发超时、端口泄漏或严格一次失败。

- [x] **Step 5: 运行 Diagnostics Benchmark**

Run: `go test ./application -run '^$' -bench 'Diagnostics(Summary|Full)' -benchmem -count=5`
Expected: 保存各规模中位数、allocs/op、B/op、response-bytes；Summary 64×64 不建立逐 Service DTO。

- [x] **Step 6: 跨平台构建**

Run: `$env:GOOS='linux'; go build ./...`
Expected: PASS。

Run: `$env:GOOS='darwin'; go build ./...`
Expected: PASS。

Run: `Remove-Item Env:GOOS; go build ./...`
Expected: Windows 当前平台 PASS。

- [x] **Step 7: 写验收报告并回填计划复选框**

报告记录精确命令、日期、Go/OS/CPU、测试结果、覆盖率、Benchmark 表格、非环回安全边界、无法
稳定注入的 OS 故障和剩余风险。计划只把实际完成步骤改为 `[x]`，不伪造结果。

- [x] **Step 8: 最终差异和敏感信息检查**

Run: `git diff --check`
Expected: PASS。

Run: `rg -n -i 'password|token|authorization|cookie' admin application examples/10-admin-diagnostics-and-pprof docs/maintenance/v3.1/guides/10.admin-diagnostics-and-pprof.md`
Expected: 只出现安全说明、测试虚拟值或明确脱敏逻辑，不含真实凭证。

Run: `git status --short`
Expected: 当前功能文件范围清楚；用户原有无关改动仍保持未暂存。

- [x] **Step 9: 提交验收材料**

```powershell
git add docs/maintenance/v3.1/reports/Admin管理HTTP、Diagnostics与pprof验收报告.md docs/maintenance/v3.1/README.md docs/maintenance/v3.1/plans/Admin管理HTTP、Diagnostics与pprof实施计划.md
git commit -m "test: 完成Admin与诊断验收"
```
