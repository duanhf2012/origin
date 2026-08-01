# Origin 第三版 M20 多节点 Broadcast 与部分失败实施计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use `superpowers:executing-plans` to implement
> this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.
>
> 当前状态：实施中
>
> 创建日期：2026-08-01
>
> 对应设计：[M20 多节点 Broadcast 与部分失败设计](../design/milestones/M20-多节点Broadcast与部分失败设计.md)

**Goal:** 把当前最多投递一个本地目标的生成式 `BroadcastXxx` 扩展为本地、TCP、NATS
统一的多 Node 通知广播；编码前固定完整目标计划，只编码一次，并以稳定的部分失败错误返回
逐目标原因。

**Architecture:** 复用 M19 的不可变 Discovery、TCP target/session、NATS connection 和本地
endpoint 视图，在 `PrepareBroadcast` 中无分配扫描并固定广播计划。单目标成功计划退化为既有
`preparedTarget`；多目标计划保存固定视图和计数，提交阶段按 NodeID 顺序为每个可发送目标
建立独占 Buffer，并让最后一个可发送目标消费原始编码 Buffer。已知断开实例保留为失败意图，
提交期间不重扫、不改选、不重试。

**Tech Stack:** Go 1.26.5、Origin RPC 静态生成器、Origin BufferPool、Service Scheduler、
不可变 Discovery Snapshot、Origin TCP Runtime、NATS Go Client。

## Global Constraints

- 生成方法 `BroadcastXxx(ctx, ...) error`、payload、TCP/NATS Wire 和 NATS Subject 保持不变。
- rpcgen ABI 从 2 提升到 3，生成代码固定使用 `PrepareBroadcast -> Encode Once -> Broadcast`。
- 自动单目标选择和 Broadcast 默认排除 Retired；`IncludeRetired()` 仅增加 Retired，保持值语义。
- Broadcast 忽略 RoundRobin、Random、Key 和自定义 Selector；`OnNode` 仍缩小为最多一个目标。
- 广播意图按 NodeID 稳定排序；合法但已知断开的实例立即产生失败，不等待、不删除、不替代。
- 一次调用只读取一次 Discovery、本地 endpoint、TCP 64 分片和 NATS connection 视图。
- 单目标计划复用 M19 快路径；多目标允许一个固定 plan 对象，不按目标数建立成功目标 Slice。
- Sizer 和 Writer 只执行一次；各目标持有独立 Buffer；禁止引用计数、`unsafe` 和共享可释放 Buffer。
- 扇出在调用方当前栈顺序执行，不创建逐目标 goroutine、Timer、闭包、重试或工作池。
- 提交成功只表示本地 Service 队列、TCP 发送队列或 NATS Publish 接受，不增加远端 ACK。
- 最多 8192 个意图目标；`payload_size × intent_count` 使用 `int64` 防溢出并受配置上限约束。
- 新增 `rpc.max_broadcast_size`，默认 `64M`、最大 `1G`，外部配置只接受严格 `ByteSize` 字符串。
- 多目标部分成功固定返回 2010；多目标全部失败固定返回 2011；单目标保留底层原始错误。
- 成功热路径不建立公开逐目标结果；失败详情仅在失败返回路径按失败数分配。
- 所有新增和修改代码使用中文 GoDoc、执行步骤注释和所有权/并发不变量说明。

---

## 1. 文件职责

### 新建

- `rpc/broadcast.go`：广播计划、目标意图扫描、容量预检和稳定顺序提交。
- `rpc/broadcast_error.go`：只读 `BroadcastError`、`BroadcastFailure` 和聚合错误匹配。
- `rpc/broadcast_test.go`：范围、计划、容量、顺序、Context、所有权和错误模型单元测试。

### 修改

- `rpc/types.go`：生成 ABI 3 和 Broadcast 公共边界常量。
- `rpc/config.go`：`MaxBroadcastSize` 默认值、上限和校验。
- `rpc/client.go`：`IncludeRetired`、`PrepareBroadcast`、广播分配与提交。
- `rpc/route.go`：所有路由派生保留 Retired 标志并丢弃旧 prepared/broadcast 状态。
- `rpc/prepare.go`：候选生命周期过滤支持显式包含 Retired，并复用冻结视图分类。
- `rpc/runtime.go`：按已固定目标精确申请 Buffer，并在提交时只校验固定连接身份。
- `rpc/remote_session.go`、`rpc/nats_runtime.go`：复用既有 Notify 所有权边界，不增加协议。
- `errs/code.go`、`errs/errors.go`：新增 2011 和稳定哨兵。
- `application/config.go`：严格解析和转换 `rpc.max_broadcast_size`。
- `internal/rpcgen/generate.go`、`internal/rpcgen/render.go`：ABI 3、生成 `IncludeRetired` 和 Prepare 流程。
- `internal/rpcgen/model_test.go`、`cmd/origingen/testdata/**`：生成器行为和黄金生成物。
- `tests/integration/rpcfixture/**`：真实本地/TCP/NATS 多节点广播与失败测试。
- `rpc/benchmark_test.go`：派生、Prepare、复制与扇出分配门禁。
- M20 设计、路线图、复核清单、索引和迁移记录：回写实施结果与验证证据。

---

### Task 1：错误码、聚合错误与广播配置

**Files:**

- Create: `rpc/broadcast_error.go`
- Create: `rpc/broadcast_test.go`
- Modify: `errs/code.go`
- Modify: `errs/errors.go`
- Modify: `errs/errors_test.go`
- Modify: `rpc/config.go`
- Modify: `rpc/config_test.go`
- Modify: `application/config.go`
- Modify: `application/config_test.go`

**Interfaces:**

- Produces: `errs.CodeRPCBroadcastFailed`, `errs.ErrRPCBroadcastFailed`.
- Produces: `BroadcastFailure`, `(*BroadcastError).Total`, `Succeeded`, `FailureCount`, `Failure`, `Code`.
- Produces: `Config.MaxBroadcastSize` and application key `rpc.max_broadcast_size`.

- [ ] **Step 1: 写 2011、聚合错误和配置边界失败测试**

覆盖：2010/2011 文本与 `errs.New`；部分/全部失败的 Code；越界 `Failure`；
`errors.Is` 匹配聚合哨兵和任意底层原因；默认 `64M`；合法 `1G`；超过 `1G`、零值、裸整数、
错误单位和目标 `int` 溢出均拒绝。

- [ ] **Step 2: 运行窄测试并确认新 API 缺失**

Run:

```text
go test ./errs ./rpc ./application -run 'Broadcast|MaxBroadcast' -count=1
```

Expected: 编译失败或断言失败，原因仅为 2011、聚合类型或配置字段尚未实现。

- [ ] **Step 3: 实现最小错误与配置模型**

`BroadcastError` 字段保持私有，构造函数只供 rpc 包失败路径使用；`Error()` 仅输出
total/succeeded/failed；`Is` 先识别 2010/2011 哨兵，再线性检查失败原因。配置校验在冻结前
完成，Runtime 始终获得非零、未超过 `1G` 的字节数。

- [ ] **Step 4: 运行测试、格式化并提交**

Run:

```text
gofmt -w errs rpc application
go test ./errs ./rpc ./application -run 'Broadcast|MaxBroadcast' -count=1
```

Commit: `feat(M20): 增加广播错误模型与容量配置`

---

### Task 2：IncludeRetired 值派生与单目标选择统一

**Files:**

- Modify: `rpc/client.go`
- Modify: `rpc/route.go`
- Modify: `rpc/prepare.go`
- Modify: `rpc/route_test.go`
- Modify: `rpc/prepare_test.go`
- Modify: `rpc/benchmark_test.go`

**Interfaces:**

- Produces: `func (Client) IncludeRetired() Client`.
- Preserves: `OnNode`、四种 Route 派生、`Prepare` 和 `PrepareNotify`。

- [ ] **Step 1: 写值语义、派生顺序和生命周期失败测试**

覆盖基础客户端不变、重复调用幂等、全部路由派生双向顺序保留标志、默认自动候选排除 Retired、
显式包含后 Running+Retired 都可选、精确 `OnNode` 原本就允许 Retired，以及 Lost/Stopped 等状态
仍永远不进入候选。

- [ ] **Step 2: 运行测试并确认缺少 IncludeRetired**

Run:

```text
go test ./rpc -run 'IncludeRetired|RetiredCandidate' -count=1
```

Expected: 编译失败，缺少 `Client.IncludeRetired` 或候选标志。

- [ ] **Step 3: 实现最小候选过滤变化**

在 Client 和候选集保存一个 bool；值派生只复制轻量 Client 并清空旧 prepared/broadcast 状态；
生命周期匹配固定为 exact 或 include-retired 时允许 Retired，不向 Provider、Service 或 Dispatcher
增加开关。

- [ ] **Step 4: 建立零分配门禁并提交**

Run:

```text
gofmt -w rpc
go test ./rpc -run 'IncludeRetired|RetiredCandidate' -count=1
go test ./rpc -run '^$' -bench 'BenchmarkClientIncludeRetired' -benchmem -count=3
```

Expected: `BenchmarkClientIncludeRetired` 为 `0 B/op, 0 allocs/op`。

Commit: `feat(M20): 支持显式包含退休服务`

---

### Task 3：PrepareBroadcast 与冻结目标计划

**Files:**

- Create: `rpc/broadcast.go`
- Modify: `rpc/client.go`
- Modify: `rpc/prepare.go`
- Modify: `rpc/runtime.go`
- Modify: `rpc/broadcast_test.go`
- Modify: `rpc/prepare_test.go`

**Interfaces:**

- Produces: `func (Client) PrepareBroadcast(context.Context, MethodID) (Client, error)`.
- Internal: one-target `preparedTarget`; multi-target `broadcastPlan` with frozen views and counters.

- [ ] **Step 1: 写目标范围和预检失败测试**

覆盖本地+远端稳定 NodeID 顺序、RouteSelector 不执行、OnNode 最多一个、同名契约/指纹/方法过滤、
默认/包含 Retired、远端私有不可见、本地私有可见、合法断开仍计入 intent、8192 成功、8193 过载、
Prepare 前 Context 取消、零合法目标、仅契约不匹配、单目标断开返回底层错误、多目标全断开返回 2011。

- [ ] **Step 2: 运行测试并确认 PrepareBroadcast 缺失**

Run:

```text
go test ./rpc -run 'PrepareBroadcast|BroadcastTarget|BroadcastIntent' -count=1
```

Expected: 编译失败，缺少 `PrepareBroadcast` 或广播计划。

- [ ] **Step 3: 实现无分配两遍扫描计划**

第一遍在同一候选视图上分类 Service/Contract/Lifecycle/Transport、计数 intent 和 sendable，并记录
最后一个 sendable 的扫描位置；第二遍只在提交时执行。计划不复制候选或标签。一个合法且可发送
目标直接生成既有 `preparedTarget`；多目标只保存固定 candidateSet、method、数量和原始 Buffer
保留位置。

- [ ] **Step 4: 实现 Prepare 阶段错误分类**

8193 在任何编码前过载；单目标不可用返回其 Transport 错误；多目标全部不可用构造 2011；
存在可发送目标时把已知不可用留给提交聚合。契约不匹配只在不存在任何合法契约目标时返回。

- [ ] **Step 5: 运行范围测试和竞态测试并提交**

Run:

```text
gofmt -w rpc
go test ./rpc -run 'PrepareBroadcast|BroadcastTarget|BroadcastIntent' -count=1
go test -race ./rpc -run 'PrepareBroadcast|BroadcastTarget|BroadcastIntent' -count=1
```

Commit: `feat(M20): 固定多节点广播目标计划`

---

### Task 4：容量准入、单次编码 Buffer 与顺序扇出

**Files:**

- Modify: `rpc/broadcast.go`
- Modify: `rpc/client.go`
- Modify: `rpc/runtime.go`
- Modify: `rpc/remote_session.go`
- Modify: `rpc/nats_runtime.go`
- Modify: `rpc/broadcast_test.go`
- Modify: `rpc/benchmark_test.go`

**Interfaces:**

- Internal: prepared broadcast allocation with exact retained-target headroom.
- Preserves: `Client.Broadcast(ctx, methodID, request) error`.

- [ ] **Step 1: 写容量、复制和所有权失败测试**

覆盖 `payload_size × intent_count` 的 `int64` 溢出、默认 64M 和 1G 边界、断开目标仍参与容量、
超限零提交；Sizer/Writer 各执行一次；不同 headroom 的 payload 一致；除最后可发送目标外使用池化
副本；全部成功、部分失败、全部失败、单目标失败、Context 在首次提交前和扇出中途取消；每条路径
原始/副本 Buffer 恰好释放或转移一次。

- [ ] **Step 2: 运行测试并确认仍是单目标 Broadcast**

Run:

```text
go test ./rpc -run 'BroadcastCapacity|BroadcastFanout|BroadcastBuffer|BroadcastContext' -count=1
```

Expected: 多目标断言失败，或容量预检/独占 Buffer 行为尚不存在。

- [ ] **Step 3: 实现编码前容量准入**

`AllocateRequest` 在存在 broadcast plan 时先验证单 payload 上限，再用 `int64` 安全乘法计算总放大；
超限直接返回过载且没有申请/提交。原始 Buffer 使用最后一个可发送目标的精确 headroom。

- [ ] **Step 4: 实现稳定顺序扇出与固定连接校验**

按冻结候选视图重扫：不可用意图直接记录失败；其余目标在提交前检查 Context。非保留目标从
BufferPool 按精确 headroom 申请并复制规范 payload；固定 TCP session/NATS connection 若已被替换
即失败，绝不改用新连接；本地使用固定 endpoint。提交失败由当前栈释放尚未转移的 Buffer，成功
沿用既有 Notify 消费规则；最后可发送目标消费原始 Buffer。

- [ ] **Step 5: 实现稳定聚合结果**

单目标返回底层错误；多目标全成功返回 nil；部分成功构造 2010；多目标零成功构造 2011。
失败详情按扫描的 NodeID 顺序保存。Context 中途取消把未尝试意图逐个记录为相同原因，已经成功的
目标不可撤回。

- [ ] **Step 6: 运行单元、所有权压力和竞态测试并提交**

Run:

```text
gofmt -w rpc
go test ./rpc -run 'Broadcast' -count=20
go test -race ./rpc -run 'Broadcast' -count=1
```

Commit: `feat(M20): 实现单次编码的顺序广播扇出`

---

### Task 5：rpcgen ABI 3 与最终生成外观

**Files:**

- Modify: `rpc/types.go`
- Modify: `internal/rpcgen/generate.go`
- Modify: `internal/rpcgen/render.go`
- Modify: `internal/rpcgen/model_test.go`
- Modify: `cmd/origingen/testdata/**`
- Modify: `tests/integration/rpcfixture/zz_origin_rpc.gen.go`

**Interfaces:**

- Produces generated `func (XxxRPCClient) IncludeRetired() XxxRPCClient`.
- Changes generated Broadcast body to `PrepareBroadcast -> Allocate/Encode -> Broadcast`.

- [ ] **Step 1: 写生成器文本和模型失败测试**

断言 ABI 为 3；每个客户端生成 `IncludeRetired`；Broadcast 在任何 Sizer/Writer 前调用
`PrepareBroadcast`；编码和提交都使用 returned prepared client；Notify/Await/Async 保持 M19 流程；
Bind 默认 ServiceName 和 `BindXxxRPCTo` 不变。

- [ ] **Step 2: 运行测试并确认仍为 ABI 2**

Run:

```text
go test ./internal/rpcgen -run 'GeneratedABI|IncludeRetired|Broadcast' -count=1
```

Expected: ABI 或生成文本断言失败。

- [ ] **Step 3: 修改模板并重新生成黄金文件与 fixture**

只修改模板和生成器，不手工改生成物。使用仓库固定 `origingen` 命令更新 testdata 和集成 fixture，
随后运行 `--check` 确认第二次生成无差异。

- [ ] **Step 4: 运行生成器和全仓编译测试并提交**

Run:

```text
gofmt -w rpc internal/rpcgen
go test ./internal/rpcgen ./cmd/origingen/... -count=1
go test ./... -run '^$'
```

Commit: `feat(M20): 升级广播生成器 ABI 3`

---

### Task 6：真实本地、TCP、NATS 集成验证

**Files:**

- Modify: `tests/integration/rpcfixture/**`
- Modify: `rpc/broadcast_test.go`

- [ ] **Step 1: 写跨 Transport 失败集成测试**

使用真实 Node/Service、TCP Listener/Dial 和 NATS Server，覆盖：本地+TCP、多 TCP、本地+NATS、
多 NATS；每个 Node 恰好收到一次；同一 payload；部分断线返回 2010 且其他目标收到；全部断线返回
2011 且不编码；Prepare 后 TCP session/NATS connection 替换不改选；RouteBy selector 调用次数为零；
Retired 远端默认排除、显式包含；OnNode 单目标保留原始错误。

- [ ] **Step 2: 运行测试并确认未实现的跨节点路径失败**

Run:

```text
go test ./tests/integration/rpcfixture -run 'Broadcast' -count=1
```

Expected: 新的多目标或失败语义断言失败。

- [ ] **Step 3: 只修复真实 Transport 暴露的实现缺口**

不得用 fake 替代已要求的 TCP/NATS 验证，不增加 Broadcast Wire/Subject。若固定连接检查与现有发送
入口不兼容，只抽取 rpc 内部的精确 prepared submit helper，继续复用现有 Notify 编码和所有权。

- [ ] **Step 4: 重复、竞态和泄漏验证并提交**

Run:

```text
go test ./tests/integration/rpcfixture -run 'Broadcast' -count=20
go test -race ./tests/integration/rpcfixture -run 'Broadcast' -count=1
```

Commit: `test(M20): 覆盖多传输广播与部分失败`

---

### Task 7：性能、逃逸和可观测性门禁

**Files:**

- Modify: `rpc/benchmark_test.go`
- Modify: `rpc/broadcast.go`
- Modify: RPC 内部汇总日志/统计所属文件（以现有 logger/统计边界为准，不新增公开 API）

- [ ] **Step 1: 增加代表性 Benchmark**

覆盖 1/100/1000/8192 目标 Prepare；1/100/1000/8192 目标 32B fan-out；1KB、64KB、4M payload
容量边界；全成功、首个失败、随机失败、全部失败、Context 中断。输出 `ns/op`、`B/op`、
`allocs/op` 和吞吐量。

- [ ] **Step 2: 增加汇总可观测性**

复用现有日志/统计设施记录一次调用的 ServiceName、MethodID、intent/sendable/succeeded/failed、
本地/TCP/NATS 数量、include-retired、payload/total bytes 和阶段耗时。全成功不逐目标打印；失败最多
一条汇总日志；不记录 payload、认证信息或业务参数。若现有仓库没有可复用指标注册边界，只保留
内部结构化汇总与返回错误，明确记录剩余的外部指标接入点，不为 M20 建第二套指标框架。

- [ ] **Step 3: 运行 Benchmark、逃逸和 Profile 采样**

Run:

```text
go test ./rpc -run '^$' -bench 'Benchmark(ClientIncludeRetired|PrepareBroadcast|BroadcastFanout)' -benchmem -count=5
go test ./rpc -run '^$' -bench 'BenchmarkBroadcastFanout1000' -benchmem -cpuprofile m20-cpu.out -memprofile m20-mem.out
go test -gcflags='all=-m=2' ./rpc
```

验收：`IncludeRetired` 为 0 alloc；单目标 Prepare 不新增广播 plan 分配；多目标不按成功目标数分配
Go 对象；无逐目标 goroutine。Profile 文件只作为本地证据，不提交仓库。

- [ ] **Step 4: 根据数据做必要的安全优化并提交**

只接受保持所有权清楚且有基准收益的调整；禁止 `unsafe`、引用计数 payload、无界池或复杂无锁算法。

Commit: `perf(M20): 建立广播性能与观测门禁`

---

### Task 8：全量复核、文档回写和里程碑提交

**Files:**

- Modify: `docs/design/milestones/M20-多节点Broadcast与部分失败设计.md`
- Modify: `docs/plans/M20-多节点Broadcast与部分失败实施计划.md`
- Modify: `docs/design/Origin第三版后续里程碑路线图.md`
- Modify: `docs/design/Origin第三版重构设计文档复核清单.md`
- Modify: `docs/design/README.md`
- Modify: `docs/design/Origin第三版迁移说明.md`

- [ ] **Step 1: 对照设计逐条复核实现**

检查公共 API、默认 Retired 规则、Provider SPI、目标排序、连接冻结、单次编码、Buffer 所有权、
Context、错误 Code、目标/容量上限、Wire 兼容、生成 ABI、日志敏感信息和硬里程碑上限。发现偏差先补
测试再修复，不以修改设计掩盖实现缺口。

- [ ] **Step 2: 运行覆盖率并检查低覆盖分支**

Run:

```text
go test ./rpc ./errs ./application ./internal/rpcgen ./tests/integration/rpcfixture -coverprofile=m20-cover.out -count=1
go tool cover -func=m20-cover.out
```

补齐可稳定触发的取消、溢出、所有权、连接替换和聚合错误分支；本地临时覆盖率文件不提交。

- [ ] **Step 3: 请求独立代码复核并处理发现**

按 `superpowers:requesting-code-review` 对 `21e70d2..HEAD` 的设计符合性、正确性、并发、资源所有权、
性能和测试充分性进行复核。Critical/Important 必须修复并重新验证；Minor 记录或在不扩大范围时修复。

- [ ] **Step 4: 运行完整质量门禁**

Run:

```text
gofmt -w .
go vet ./...
go test ./...
go test -race ./...
go build ./...
go test ./rpc ./tests/integration/rpcfixture -run '^$' -bench 'Broadcast' -benchmem
```

然后运行仓库固定生成检查、Linux/macOS 交叉构建和文档链接/占位符检查。任何失败必须定位根因，
不得跳过测试或放宽断言。

- [ ] **Step 5: 回写验收证据并形成 M20 完成提交**

设计和实施计划状态改为已完成；路线图固定下一步只剩 M21 业务运行时扩展收口和 M22 稳定发布；
记录命令、Benchmark、覆盖率、兼容性和剩余风险；确认工作树只含 M20 必要改动。

Commit: `feat(M20): 完成多节点广播与部分失败`

---

## 2. 完成定义

M20 只有同时满足以下条件才完成：

- 生成客户端外观与设计完全一致，旧 ABI 能由生成检查明确诊断；
- 本地、TCP、NATS 多目标都只投递一次，且使用同一 payload 编码结果；
- 发现和连接变化不能改变已 Prepare 的本次目标身份；
- 默认排除 Retired，`IncludeRetired()` 对单目标和 Broadcast 都生效且保持零分配值派生；
- 已知断开不被静默删除，2010/2011、单目标原始错误和逐目标详情均稳定；
- 容量、Context、过载和所有权错误路径保证零重复释放、零泄漏、零隐式重试；
- 单元、真实协议集成、Race、Vet、Build、生成检查、跨平台构建和 Benchmark 全部通过；
- 文档、路线图、复核清单、迁移说明和提交状态一致；
- M20 独立提交后，才开始 M21 业务运行时扩展收口设计。
