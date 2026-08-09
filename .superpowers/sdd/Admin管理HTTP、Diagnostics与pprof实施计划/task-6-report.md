# Task 6：Diagnostics Summary 与 Runtime 字段修正实施报告

## 1. 结论

状态：完成。

基线：`2145c58d27aa71a9445ed88c6a2d2ddcfb54c1ce`。

本任务新增独立 `diagnostics.Summary` schema v1，保留 Full Snapshot schema v2；Summary 按
Node 输出固定 DTO，并把每个 Node 的全部 Service 直接累加为一个 `ServiceAggregate`。Admin
`GET /admin/v1/diagnostics` 默认采集 Summary，唯一 `detail=full` 采集 Full，非法或重复 detail
返回 400，其他方法返回 405 和精确 `Allow: GET`。Listener 空闲时没有新增采样 goroutine。

Runtime 使用 Go 1.26.5 的四个固定指标名：

- `/sched/goroutines/runnable:goroutines`
- `/cpu/classes/gc/total:cpu-seconds`
- `/sync/mutex/wait/total:seconds`
- `/gc/gomemlimit:bytes`

每个指标都在读取 Value 前检查 Kind；缺失和 `KindBad` 保持零值。Full 的
`MemoryLimitBytes` 已真实填充；Summary 的 `GoMemoryUsedBytes` 使用
`MemStats.Sys-MemStats.HeapReleased`，并显式防止下溢。

## 2. TDD 记录

### 2.1 Summary DTO、JSON 与 Full 兼容

RED：先加入 Summary 零值 JSON、稳定字段名、Runtime 字段、Service 单聚合对象和 Full RPC
恢复字段兼容测试。首次运行：

```text
FAIL github.com/duanhf2012/origin/v3/diagnostics [build failed]
diagnostics\summary_test.go:13:43: undefined: diagnostics.Summary
diagnostics\summary_test.go:50:28: undefined: diagnostics.ApplicationSummary
diagnostics\summary_test.go:55:24: undefined: diagnostics.RuntimeSummary
diagnostics\summary_test.go:69:24: undefined: diagnostics.NodeSummary
```

GREEN：新增 `summary.go` 后，零值 `Nodes` 编码为 `[]`，Duration 编码保留单位，Summary RPC
没有 `reconnects`/`consecutive_failures`，Full v2 两字段继续存在并增加 Deprecated GoDoc。

```text
ok github.com/duanhf2012/origin/v3/diagnostics
```

### 2.2 Node 64 Service 聚合与并发

RED：先构造 64 个真实绑定 IService，每个返回人工设置的 Execution/Timer/Event 叶子，并交错
Retire 32 个 Service；测试在 `DiagnosticsSummary` 尚不存在时不能编译。

GREEN：`Node.DiagnosticsSummary()` 直接遍历静态 `node.services`，逐个读取 State、
ExecutionStats、TimerStats、EventStats 并累加。测试验证 64 个 Service 的状态数量和全部目标
累计值与人工和一致；另覆盖全部生命周期桶、nil Node，以及并发 Retire/Resume、Timer、Event
期间反复采集。

```text
ok github.com/duanhf2012/origin/v3/node
```

### 2.3 Runtime、Application 锁边界与 CollectCost

RED：先断言 Full `MemoryLimitBytes > 0`、Summary 内存口径、四个真实 metrics Kind、缺失与
KindBad 零值、nil Application、运行 Application 聚合，以及阻塞 Service 叶子期间可以重新取得
`app.mu`。

GREEN：Application 只在锁内复制身份、Admin/pprof 快照、Node 指针 Slice 和 BufferPool 指针；
Runtime、Pool 和 Node 采集均在锁外。阻塞叶子测试同时等待 20ms，证明 `CollectCost` 覆盖完整
真实采集，而不是只覆盖锁内复制。

### 2.4 Admin detail 路由和预编码

RED：最初使用 Go Method Pattern 时，POST 得到 Go 1.26 自动生成的：

```text
status=405 Allow="GET, HEAD" Body="Method Not Allowed\n"
```

而契约要求精确 `Allow: GET`。

GREEN：固定路径进入已有 `serveAdminEndpoint`，由 GET Endpoint 的统一方法边界生成 405；Summary
和 Full 都先通过 `admin.JSON` 完成编码，再进入响应上限校验和唯一 outer commit/audit。验证结果：

```text
GET /admin/v1/diagnostics                         200 schema_version=1
GET /admin/v1/diagnostics?detail=full             200 schema_version=2
GET /admin/v1/diagnostics?detail=x                400
GET /admin/v1/diagnostics?detail=                 400
GET /admin/v1/diagnostics?detail=full&detail=full 400
POST /admin/v1/diagnostics                        405 Allow: GET
```

### 2.5 Benchmark fixture

RED：完整 3×3 矩阵首次运行发现生产 `node.New` 明确禁止零 Service Node：

```text
BenchmarkDiagnosticsSummary/Nodes1_Services0: node.New() error = Node "node-0" 没有 Service
```

GREEN：零 Service 行改用 Node 已公开的 nil-safe Diagnostics 语义；其余行继续构造真实 Node 和
Service。全部 fixture 均在 `b.ResetTimer()` 前构造，Cleanup 逆序回收真实 Node。

## 3. 测试与静态检查

Task 6 普通测试：

```text
go test ./diagnostics ./node ./application -run 'Diagnostics|Summary' -count=1
ok github.com/duanhf2012/origin/v3/diagnostics 0.045s
ok github.com/duanhf2012/origin/v3/node        0.082s
ok github.com/duanhf2012/origin/v3/application 0.143s
```

Task 6 Race：

```text
go test -race ./node ./application -run 'Diagnostics|Summary' -count=1
ok github.com/duanhf2012/origin/v3/node        1.133s
ok github.com/duanhf2012/origin/v3/application 1.208s
```

相关包全量测试：

```text
go test ./diagnostics ./node ./application -count=1
ok github.com/duanhf2012/origin/v3/diagnostics
ok github.com/duanhf2012/origin/v3/node
ok github.com/duanhf2012/origin/v3/application
```

Vet：

```text
go vet ./diagnostics ./node ./application
PASS
go vet ./...
PASS
```

覆盖率命令：

```text
go test ./diagnostics ./node ./application -run 'Diagnostics|Summary' -coverprofile <temp> -count=1
```

核心新增/修改函数覆盖率：

```text
Application.DiagnosticsSummary       100.0%
collectRuntimeSnapshot               100.0%
collectRuntimeSummary                100.0%
runtimeSummaryFrom                   100.0%
collectRuntimeMetricValues           100.0%
runtimeMetricValuesFrom              100.0%
diagnostics.Summary.MarshalJSON      100.0%
Node.DiagnosticsSummary              100.0%
aggregateService                     100.0%
```

全仓回归：并行 `go test ./... -count=1` 两次分别遇到 Task 6 范围外的 Windows command 临时
response 文件短暂被占用；首个失败用例单独复跑通过。为排除包间并行和外部文件句柄干扰，最终
串行复核：

```text
go test -p 1 ./... -count=1
PASS（全部包）
```

## 4. Benchmark（Go 1.26.5，windows/amd64）

命令：

```text
go test ./application -run '^$' -bench 'Diagnostics(Summary|Full)' -benchmem -count=3
PASS，128.676s
```

完整执行了 0/1/64 Node × 0/1/64 Service × Summary/Full/SummaryJSON/FullJSON。下表为关键
64×64 场景三次结果的中位数：

| Benchmark | ns/op | B/op | allocs/op | response-bytes |
|---|---:|---:|---:|---:|
| DiagnosticsSummary | 193,701 | 49,824 | 3 | 113,565 |
| DiagnosticsFull | 694,637 | 1,434,276 | 67 | 3,299,137 |
| DiagnosticsSummaryJSON | 877,504 | 287,575 | 15 | 113,569 |
| DiagnosticsFullJSON | 8,282,309 | 9,304,198 | 28,766 | 3,299,338 |

关键结论：

- Summary 原始采集在 64×1 和 64×64 都是 `49,824 B/op`、`3 allocs/op`，没有按 Service
  输出 DTO 分配；
- Full 64×64 出现预期的约 1.43 MiB 逐 Service DTO 分配；
- Summary 响应约 113.6 KiB，Full 约 3.30 MiB，Full 约为 Summary 的 29 倍；
- JSON 编码的 allocs 主要来自标准库按字段输出；Summary 响应和分配不随每 Node Service
  数量线性膨胀。

## 5. 自审与剩余顾虑

- 未修改或暂存 `node/node.go`、`node/config.go`、`node/discovery_server.go`、
  `application/application.go`、`application/application_test.go`、`application/config.go`；
- 未新增包级可变状态、周期 goroutine、反射扫描、第三方依赖或隐藏缓存；
- Summary Node 聚合不调用 Full `Diagnostics()`，也不创建 `[]ServiceSnapshot`；
- Admin 采样只发生在合法 GET 请求进入 Handler 后，outer 请求上限和审计仍只有一层；
- Full schema v2 和公开 JSON 字段未删除；
- 唯一测试夹具说明：生产配置禁止零 Service Node，因此 Benchmark 的零 Service 行使用既有
  nil-safe Node Diagnostics 语义。该行用于固定 DTO/编码零边界，不代表生产允许空 Node；
- 并行全仓回归的 Windows 临时文件占用与 Task 6 文件无关，目标用例单独通过且串行全仓通过，
  仍在报告中保留原始现象，未通过跳过或放宽断言掩盖。
