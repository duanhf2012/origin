# Origin 第三版 M19 RPC 实例选择与单目标路由策略实施计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use `superpowers:executing-plans` to implement
> this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.
>
> 当前状态：已完成并通过验收
>
> 创建日期：2026-07-30
>
> 对应设计：[M19 RPC 实例选择与单目标路由策略设计](../design/milestones/M19-RPC实例选择与单目标路由策略设计.md)

**Goal:** 实现本地、TCP、NATS 统一的单目标实例选择，并为生成客户端提供默认绑定、
显式改名、精确 Node、RoundRobin、Random、稳定 Key 和自定义 Selector 外观。

**Architecture:** Discovery Runtime 向 RPC Runtime 提供一次原子读取取得的不可变候选视图；
RPC Runtime 在编码前合并本地和远端候选、过滤生命周期与连接状态、只选择一次并固定会话。
生成代码执行 `Prepare -> Encode -> Submit`，提交阶段只复核已经选中的身份和连接，不改选、
不重试。

**Tech Stack:** Go 1.26.5、Origin Service Scheduler、不可变 Discovery Snapshot、
Origin TCP Runtime、NATS Go Client、静态 `origingen`。

## Global Constraints

- `BindXxxRPC(owner)` 的默认 ServiceName 只由契约名确定性生成。
- 模板改名只使用 `BindXxxRPCTo(owner, serviceName)`；精确 Node 使用 `OnNode(nodeID)`。
- 自动候选只包含 Running 且当前 Connected 的实例；精确候选继续允许 Retired。
- Await 只在“合法候选仅缺连接”时等待；Async 和 Notify 当前不可发送就立即失败。
- Prepare 在请求 Buffer 分配和编码之前完成，一次调用只运行一次 Selector。
- 选中后固定 NodeID、ServiceName、SessionID、Transport generation 和连接对象。
- 选择后断线、快照替换、Session 替换或队列失败均不改选、不重试、不重发 Notify。
- M17 Provider SPI、业务 RPC 方法签名、payload 编码和 TCP/NATS Wire 均保持不变。
- 基础整数、string 和 `[]byte` 支持稳定 Key；命名整数由调用方显式转换；不支持 `uintptr`。
- TCP 目标与 Listener 连接固定上限从 4096 对齐为 8192。
- 内置候选读取、客户端派生和成功 Prepare 热路径不得复制候选/标签，并以
  `0 allocs/op` 为硬门禁。

---

## 1. 文件职责

### 新建

- `rpc/route.go`：路由模式、稳定 Key 归一化、`RouteSelector` 和只读 `RouteCandidates`。
- `rpc/prepare.go`：候选扫描、错误分类、内置选择、连接等待和 prepared target。
- `rpc/route_test.go`：路由值语义、Key、Selector、候选访问和错误测试。
- `rpc/prepare_test.go`：本地/远端候选过滤、一次选择、会话固定和等待边界测试。

### 修改

- `rpc/types.go`：生成 ABI 2、Target 只读派生和必要的内部枚举。
- `rpc/client.go`：路由派生、Prepare API、prepared 分配与提交。
- `rpc/runtime.go`：本地候选、Discovery 视图、轮询/随机状态和路由变化通知。
- `rpc/remote_runtime.go`、`rpc/remote_target.go`：8192 上限和 TCP 原子活动会话视图。
- `rpc/nats_runtime.go`：NATS 原子 connection generation 视图和路由变化通知。
- `internal/discovery/directory.go`：公开给框架内部桥接的一致 Snapshot 读取。
- `node/discovery_runtime.go`：实现 RPC 只读快照桥并在快照发布后通知路由等待者。
- `node/node.go`：冻结并绑定本地 Node 标签。
- `internal/rpcgen/render.go`：默认/显式绑定、`OnNode`、路由方法和 Prepare 生成。
- `internal/rpcgen/generate.go`：生成器 ABI 常量提升到 2。
- `internal/rpcgen/model_test.go`、`cmd/origingen/testdata/**`：生成外观与黄金生成物。
- `rpc/benchmark_test.go`：客户端派生、内置选择和 Prepare 分配门禁。
- M19 设计、路线图、复核清单与索引：实施结果和验收证据。

---

### Task 1：路由值对象与 Key 归一化

**Files:**

- Create: `rpc/route.go`
- Create: `rpc/route_test.go`
- Modify: `rpc/client.go`
- Modify: `rpc/types.go`

**Interfaces:**

- Produces:
  `Client.OnNode(string) Client`,
  `Client.RouteRoundRobin() Client`,
  `Client.RouteRandom() Client`,
  `Client.Route(any) Client`,
  `Client.RouteBy(RouteSelector) Client`,
  `RouteSelector.Select(RouteCandidates) (int, bool)`.

- [x] **Step 1: 写路由派生和 Key 的失败测试**

```go
func TestClientRouteDerivationKeepsBaseClientImmutable(t *testing.T) {
    base := Client{target: ToService("PlayerService")}
    exact := base.OnNode("player-2")
    key := int64(-7)
    keyed := base.Route(key)
    if base.target.nodeID != "" || exact.target.nodeID != "player-2" {
        t.Fatal("值派生修改了基础客户端或没有保留精确节点")
    }
    if keyed.route.mode != routeKey || keyed.route.hash != uint64(key) {
        t.Fatal("整数 Key 没有确定性归一化")
    }
}
```

同时覆盖全部支持的基础整数、string、`[]byte`、nil、结构体、命名整数未显式转换、
`OnNode` 保留 ServiceName，以及策略派生不扩大精确目标。

- [x] **Step 2: 运行测试并确认因 API 不存在而失败**

Run: `go test ./rpc -run 'TestClientRoute|TestNormalizeRouteKey' -count=1`

Expected: 编译失败，缺少 `OnNode`、`Route` 或路由类型。

- [x] **Step 3: 实现最小路由值对象**

```go
type routeMode uint8

const (
    routeRoundRobin routeMode = iota + 1
    routeRandom
    routeKey
    routeCustom
)

type routeSpec struct {
    mode     routeMode
    hash     uint64
    selector RouteSelector
    err      error
}

type RouteSelector interface {
    Select(RouteCandidates) (index int, ok bool)
}
```

`Route(any)` 使用精确 type switch 和 FNV-1a 64 位，不使用反射、`fmt` 或临时字节切片；
不支持类型把 `errs.ErrRPCInvalidRouteKey` 保存到派生值。`OnNode` 使用当前绑定的
ServiceName 构造 `ToServiceOnNode`，并清除上一次 prepared 状态。

- [x] **Step 4: 运行路由单测**

Run: `go test ./rpc -run 'TestClientRoute|TestNormalizeRouteKey' -count=1`

Expected: PASS。

- [x] **Step 5: 提交独立路由值对象**

```text
git add rpc/route.go rpc/route_test.go rpc/client.go rpc/types.go
git commit -m "feat: 增加 M19 RPC 路由值对象"
```

### Task 2：不可变 Discovery 候选桥与本地标签

**Files:**

- Modify: `internal/discovery/directory.go`
- Modify: `internal/discovery/directory_test.go`
- Modify: `node/discovery_runtime.go`
- Modify: `node/node.go`
- Modify: `rpc/runtime.go`
- Test: `node/node_test.go`
- Test: `rpc/remote_resolver_test.go`

**Interfaces:**

- Consumes: Task 1 的 `RouteCandidates`。
- Produces:
  `RemoteResolver.Snapshot() RemoteSnapshot`,
  `RemoteSnapshot.List(string) RemoteCandidates`,
  `Runtime.BindLocalLabels(map[string]string) error`。

- [x] **Step 1: 写单快照一致性和无业务复制失败测试**

测试在取得旧 Snapshot 后发布新快照，验证旧视图仍只返回旧 Node，新视图返回新 Node；
验证同一 Service 的候选按 NodeID 稳定排序；验证本地私有 Service 和冻结标签能进入 RPC
内部候选，但 M17 Provider SPI 没有新增方法。

- [x] **Step 2: 运行定向测试并确认桥接尚不存在**

Run:
`go test ./internal/discovery ./node ./rpc -run 'TestDirectorySnapshot|TestRPCRemoteSnapshot|TestBindLocalLabels' -count=1`

Expected: 编译失败或断言失败，缺少一致 Snapshot 桥接。

- [x] **Step 3: 增加框架内部不可变视图**

`Directory.Snapshot()` 只执行一次 `atomic.Pointer.Load`；Snapshot 提供只读 `Find`、`List`
和版本读取。Node 侧使用只读适配器把内部 discovery State/Transport/Contract 转换为
RPC 标量，不把 `internal/discovery` 类型暴露到公开 `rpc` 包，也不复制 Slice 或标签 Map。

本地标签在 Node 配置冻结后调用 `BindLocalLabels` 深复制一次；RPC 热路径只读该冻结 Map。

- [x] **Step 4: 运行目录、Node 和 RPC 桥接测试**

Run:
`go test ./internal/discovery ./node ./rpc -run 'TestDirectorySnapshot|TestRPCRemoteSnapshot|TestBindLocalLabels' -count=1`

Expected: PASS。

- [x] **Step 5: 提交候选桥**

```text
git add internal/discovery/directory.go internal/discovery/directory_test.go node/discovery_runtime.go node/node.go node/node_test.go rpc/runtime.go rpc/remote_resolver_test.go
git commit -m "feat: 增加 RPC 不可变候选视图"
```

### Task 3：候选过滤、内置选择与错误分类

**Files:**

- Create: `rpc/prepare.go`
- Create: `rpc/prepare_test.go`
- Modify: `rpc/runtime.go`
- Modify: `rpc/route.go`

**Interfaces:**

- Consumes: Task 2 的 `RemoteSnapshot` 和本地 endpoint/标签。
- Produces:
  `Runtime.prepare(Client, context.Context, MethodID, prepareKind) (preparedTarget, error)`。

- [x] **Step 1: 写过滤和选择失败测试**

覆盖本地公开/私有与远端合并、NodeID 排序、自动排除 Retired/断开、精确保留 Retired、
ContractID 与完整 Fingerprint、不兼容 Transport、默认 Runtime 级 RoundRobin、Random
范围、Key 稳定性、Selector 标签读取、nil/拒绝/越界/panic 和五阶段错误分类。

- [x] **Step 2: 运行 Prepare 核心测试并确认失败**

Run:
`go test ./rpc -run 'TestPrepareCandidates|TestPrepareRoundRobin|TestPrepareKey|TestPrepareSelector|TestPrepareErrorClassification' -count=1`

Expected: 编译失败或没有自动候选。

- [x] **Step 3: 实现无复制候选扫描**

使用一个栈上候选集合保存本地 endpoint、一次取得的远端 Snapshot 和连接状态视图；
`Len` 与按索引读取只扫描该视图，不构造候选 Slice。RoundRobin 计数按
`ServiceName + ContractID + Fingerprint` 在首次存在合法候选后惰性创建；Random 使用
Runtime 原子状态；自定义 Selector 只在 custom 分支恢复 panic。

- [x] **Step 4: 运行候选和选择测试**

Run:
`go test ./rpc -run 'TestPrepareCandidates|TestPrepareRoundRobin|TestPrepareKey|TestPrepareSelector|TestPrepareErrorClassification' -count=1`

Expected: PASS。

- [x] **Step 5: 提交选择核心**

```text
git add rpc/prepare.go rpc/prepare_test.go rpc/runtime.go rpc/route.go
git commit -m "feat: 实现 M19 单目标候选选择"
```

### Task 4：TCP/NATS 原子连接视图、等待通知与 8192 容量

**Files:**

- Modify: `rpc/remote_runtime.go`
- Modify: `rpc/remote_target.go`
- Modify: `rpc/remote_session.go`
- Modify: `rpc/nats_runtime.go`
- Modify: `rpc/runtime.go`
- Test: `rpc/remote_runtime_test.go`
- Test: `rpc/nats_response_ownership_test.go`
- Test: `rpc/prepare_test.go`

**Interfaces:**

- Produces:
  TCP `activeSession` 原子读取，
  NATS `connectionView{conn, generation}` 原子读取，
  `Runtime.NotifyRoutesChanged()`，
  无轮询 route-change Channel。

- [x] **Step 1: 写连接可见性和容量失败测试**

覆盖 TCP 握手完成前不可选、断开立即摘除、重连后重新进入、Session 替换；NATS
Disconnected/Reconnecting/Closed 摘除及新 generation 恢复；8192 个目标允许、
8193 个目标拒绝；等待通知无丢失唤醒。

- [x] **Step 2: 运行连接视图测试并确认失败**

Run:
`go test ./rpc -run 'TestPrepareTCPConnectionView|TestPrepareNATSConnectionView|TestRouteChangeWakeup|TestRemoteTargetCapacity' -count=1`

Expected: 断开实例仍可被旧路径解析，或 8192 容量断言失败。

- [x] **Step 3: 实现原子活动连接视图**

TCP target 用 `atomic.Pointer[outboundSession]` 发布握手完成会话；remote runtime 在写侧
维护不可变目标索引供选择读取。NATS 只在连接和订阅全部就绪后发布带 generation 的视图，
断开/关闭立即清除。每次视图变化关闭当前 route-change Channel 并替换新 Channel。

- [x] **Step 4: 将 TCP 固定上限统一为 8192**

`maxRemoteTargets` 和 Listener `MaxConnections` 使用同一 `8192` 常量；保持控制连接、
公开 Service 和 Provider 其他容量不变。

- [x] **Step 5: 运行连接、Race 定向测试**

Run:
`go test ./rpc -run 'TestPrepareTCPConnectionView|TestPrepareNATSConnectionView|TestRouteChangeWakeup|TestRemoteTargetCapacity' -count=1`

Run:
`go test -race ./rpc -run 'TestPrepareTCPConnectionView|TestPrepareNATSConnectionView|TestRouteChangeWakeup' -count=1`

Expected: 全部 PASS，无 Race。

- [x] **Step 6: 提交 Transport 视图**

```text
git add rpc/remote_runtime.go rpc/remote_target.go rpc/remote_session.go rpc/nats_runtime.go rpc/runtime.go rpc/remote_runtime_test.go rpc/nats_response_ownership_test.go rpc/prepare_test.go
git commit -m "feat: 固定 RPC 路由连接视图"
```

### Task 5：Prepare、Await 等待边界与会话固定提交

**Files:**

- Modify: `rpc/client.go`
- Modify: `rpc/prepare.go`
- Modify: `rpc/runtime.go`
- Modify: `rpc/remote_runtime.go`
- Modify: `rpc/nats_runtime.go`
- Test: `rpc/prepare_test.go`
- Test: `rpc/runtime_test.go`
- Test: `rpc/runtime_failure_test.go`

**Interfaces:**

- Produces:
  `Client.PrepareAwait(context.Context, MethodID) (Client, error)`,
  `Client.PrepareAsync(context.Context, MethodID) (Client, error)`,
  `Client.PrepareNotify(context.Context, MethodID) (Client, error)`。

- [x] **Step 1: 写 Prepare 顺序和等待边界失败测试**

覆盖失败时请求 Buffer 尚未分配；Await 只缺连接时用 owner `Await` 等待且可取消；
无同名、契约不匹配、只有 Retired 或 Transport 不兼容立即返回；Async/Notify 不等待；
连接恢复后 Selector 只执行一次。

- [x] **Step 2: 写选择后竞态失败测试**

在 Prepare 后、Submit 前分别替换发现 Session、TCP session、NATS generation、断开连接
和替换快照；断言返回稳定错误且不调用第二个目标、不重新执行 Selector、不重发 Notify。

- [x] **Step 3: 运行定向测试并确认失败**

Run:
`go test ./rpc -run 'TestPrepareBeforeAllocate|TestPrepareAwaitConnection|TestPreparedTargetIdentity|TestPreparedTargetNoReselect' -count=1`

Expected: 编译失败或旧提交路径重新解析目标。

- [x] **Step 4: 实现三个 Prepare 入口**

成功 Prepare 返回 Client 值副本并保存精确 transport、NodeID、ServiceName、SessionID、
TCP session 指针或 NATS connection generation、MethodID 和 CallKind。Await 慢路径先取得
route-change Channel 再扫描，在 Service Await 内等待事件后重扫；Connected 热路径不创建
等待对象。

- [x] **Step 5: 改造分配和提交复核**

`AllocateRequest` 只接受 prepared Client 并按最终 transport 计算 headroom；Await/Async/
Notify 校验 prepared MethodID/CallKind，再提交固定目标。Broadcast 保持原 M11 范围和旧
编码路径，不进入 M19 自动多目标语义。

- [x] **Step 6: 运行 Client、Runtime 和 Race 测试**

Run:
`go test ./rpc -run 'TestPrepareBeforeAllocate|TestPrepareAwaitConnection|TestPreparedTargetIdentity|TestPreparedTargetNoReselect|TestClient' -count=1`

Run:
`go test -race ./rpc -run 'TestPrepareAwaitConnection|TestPreparedTargetIdentity|TestPreparedTargetNoReselect' -count=1`

Expected: 全部 PASS，无 Race。

- [x] **Step 7: 提交 Prepare 和固定提交**

```text
git add rpc/client.go rpc/prepare.go rpc/runtime.go rpc/remote_runtime.go rpc/nats_runtime.go rpc/prepare_test.go rpc/runtime_test.go rpc/runtime_failure_test.go
git commit -m "feat: 实现 RPC 编码前 Prepare"
```

### Task 6：origingen 最终外观与 ABI 2

**Files:**

- Modify: `rpc/types.go`
- Modify: `internal/rpcgen/generate.go`
- Modify: `internal/rpcgen/render.go`
- Modify: `internal/rpcgen/model_test.go`
- Modify: `cmd/origingen/main_test.go`
- Regenerate: `cmd/origingen/testdata/**`

**Interfaces:**

- Consumes: Task 1 和 Task 5 的 Client API。
- Produces:
  `BindXxxRPC(owner)`,
  `BindXxxRPCTo(owner, serviceName)`,
  generated `OnNode`/route methods，
  每个 Await/Async/Notify 的 `Prepare -> Encode -> Submit`。

- [x] **Step 1: 写生成文本失败测试**

对 `PlayerRPC`、`DBRPC`、无 `RPC` 后缀契约和恰好名为 `RPC` 的契约验证默认名称；
验证 `BindPlayerRPCTo`、`OnNode`、四种路由方法；验证生成方法在 encode 调用前执行对应
Prepare；验证 Broadcast 不被扩张；验证 ABI 不匹配时 `-check` 明确失败。

- [x] **Step 2: 运行生成器测试并确认失败**

Run: `go test ./internal/rpcgen ./cmd/origingen -count=1`

Expected: 生成文本缺少绑定/路由方法，ABI 仍为 1。

- [x] **Step 3: 生成默认绑定和派生方法**

默认 ServiceName 规则为：契约名以 `RPC` 结尾且前缀非空时替换该后缀，否则追加
`Service`；`RPC` 本身得到 `RPCService`。生成方法只包装底层 `rpc.Client` 值，不复制
实现逻辑。

- [x] **Step 4: 生成 Prepare 调用并提升 ABI**

`rpc.GeneratedABIVersion`、生成器期望 ABI 和生成文件双向编译期检查全部改为 2。
Await/Async/Notify 分别先调用对应 Prepare，再把 prepared Client 交给静态 encoder。

- [x] **Step 5: 重新生成仓库生成物并校验**

Run: `go generate ./...`

Run: `go run ./cmd/origingen rpc --check ./...`

Run: `go test ./internal/rpcgen ./cmd/origingen -count=1`

Expected: 生成物无 diff 漂移，全部 PASS。

- [x] **Step 6: 提交生成 ABI**

```text
git add rpc/types.go internal/rpcgen/generate.go internal/rpcgen/render.go internal/rpcgen/model_test.go cmd/origingen
git commit -m "feat: 生成 M19 RPC 客户端外观"
```

### Task 7：本地、真实 TCP 与真实 NATS 端到端回归

**Files:**

- Modify: `rpc/runtime_test.go`
- Modify: `rpc/remote_runtime_test.go`
- Modify: `rpc/nats_response_ownership_test.go`
- Modify: `node/node_test.go`
- Modify: `tests/integration/rpcfixture/**`

**Interfaces:**

- Consumes: Task 1～6 的最终外观。
- Produces: 三种 transport 一致的可执行验收。

- [x] **Step 1: 增加本地生成客户端业务外观测试**

通过真实生成客户端字段验证 `BindPlayerRPC`、`BindPlayerRPCTo`、默认路由、Key、
`OnNode`，以及本地公开/私有候选边界。Retired 摘流与恢复 Running 只在远端 TCP/NATS
发现状态中验证；当前本地 `service.State` 尚无 Retired，留待后续生命周期里程碑。

- [x] **Step 2: 增加真实双 Node TCP 测试**

启动两个 Node，发现多个 PlayerService，验证轮询、Key、断线立即摘除、Await 等待恢复、
Async/Notify 快速失败和 Session 替换不误发。

- [x] **Step 3: 增加真实三 Node NATS 测试**

启动共享 NATS Server 和三个 Node，验证多实例选择、断连摘除、恢复重新入选、
generation 固定与无重发。

- [x] **Step 4: 运行端到端和 Race**

Run: `go test ./... -run 'TestM19|TestRPC.*Route' -count=1`

Run: `go test -race ./rpc ./node -run 'TestM19|TestRPC.*Route' -count=1`

Expected: 全部 PASS，无 Race。

- [x] **Step 5: 提交集成验收**

```text
git add rpc node tests/integration/rpcfixture
git commit -m "test: 覆盖 M19 单目标路由集成场景"
```

### Task 8：分配、逃逸、容量和平台质量门禁

**Files:**

- Modify: `rpc/benchmark_test.go`
- Modify: `node/benchmark_test.go`

**Interfaces:**

- Produces: M19 性能和平台验收证据。

- [x] **Step 1: 增加客户端派生与 Prepare Benchmark**

加入 `BenchmarkBindGeneratedClient`、`BenchmarkClientOnNode`、
`BenchmarkRouteRoundRobin`、`BenchmarkRouteRandom`、`BenchmarkRouteKeyInt`、
`BenchmarkRouteKeyString` 和 100/1000/8192 候选 Prepare。

- [x] **Step 2: 运行零分配断言与 Benchmark**

Run:
`go test ./rpc -run 'TestRoute.*Allocs|TestPrepare.*Allocs' -bench 'Benchmark(Client|Route|Prepare)' -benchmem -count=3`

Expected: 内置客户端派生和成功 Prepare 为 `0 allocs/op`；候选数量增长不产生候选复制。

- [x] **Step 3: 运行逃逸分析**

Run: `go test ./rpc -run '^$' -gcflags='all=-m=2'`

Expected: 路由值对象和成功 Prepare 不因框架封装稳定逃逸；允许调用方业务 Selector 自身
或已有 Await 状态产生设计内分配。

- [x] **Step 4: 运行完整质量门禁**

Run: `go test ./... -count=1`

Run: `go test -race ./... -count=1`

Run: `go vet ./...`

Run: `go run ./cmd/origingen rpc --check ./...`

Run: `$env:CGO_ENABLED='0'; $env:GOOS='linux'; $env:GOARCH='amd64'; go build ./...`

Run: `$env:CGO_ENABLED='0'; $env:GOOS='darwin'; $env:GOARCH='amd64'; go build ./...`

Expected: 全部成功。

- [x] **Step 5: 提交性能门禁**

```text
git add rpc/benchmark_test.go node/benchmark_test.go
git commit -m "test: 增加 M19 路由性能门禁"
```

### Task 9：验收回写与最终提交

**Files:**

- Modify: `docs/plans/M19-RPC实例选择与单目标路由策略实施计划.md`
- Modify: `docs/design/milestones/M19-RPC实例选择与单目标路由策略设计.md`
- Modify: `docs/design/milestones/里程碑设计文档复核清单.md`
- Modify: `docs/design/milestones/里程碑路线图.md`
- Modify: `docs/design/设计文档索引.md`

- [x] **Step 1: 对照设计 13 节逐条核验代码和测试**

逐项确认默认/改名绑定、`OnNode`、四种策略、Running/Connected、Retired 精确调用、
Prepare 前置、Await/Async/Notify 边界、固定会话、无重试、ABI 2、8192 容量和零分配。

- [x] **Step 2: 扫描占位、生成漂移和工作区污染**

Run:
`$m19Markers = @('TO' + 'DO', 'TB' + 'D', 'FIX' + 'ME', '待' + '实现'); rg -n ($m19Markers -join '|') rpc node internal/rpcgen docs/plans/M19-* docs/design/milestones/M19-*`

Run: `git diff --check`

Run: `git status --short`

Expected: 没有 M19 遗留占位、空白错误、生成漂移或临时缓存目录。

- [x] **Step 3: 回写完成状态和验收记录**

把本计划任务全部勾选，记录真实测试、Race、Vet、生成检查、Benchmark、逃逸和跨平台结果；
把 M19 设计、路线图、复核清单与索引改为已实现。

- [x] **Step 4: 重跑文档修改后的最终门禁**

Run: `go test ./... -count=1`

Run: `git diff --check`

Expected: PASS。

- [x] **Step 5: 提交 M19**

只暂存 M19 代码、测试和文档；不使用 `git add .`，并保留工作区中与 M19 无关的修改。

## 3. 完成底线

M19 只有在生成客户端最终外观、三种 Transport 的选择和失败语义、Retired 边界、会话固定、
8192 容量、Race、生成物校验、零分配 Benchmark、Vet 和跨平台构建全部通过后才算完成。

## 4. 2026-08-01 最终验收记录

- `go test ./... -count=1`：通过；
- `go test -race ./... -count=1`：通过；
- `go vet ./...`：通过；
- `go run ./cmd/origingen rpc --check ./...`：通过，无生成漂移；
- Linux amd64、macOS amd64 `CGO_ENABLED=0 go build ./...`：通过；
- 路由派生、所有内置策略、100/1000/8192 候选 Prepare、生成客户端绑定均为
  `0 B/op, 0 allocs/op`；8192 候选 Prepare 为约 `1.39~1.42 ms/op`；
- 逃逸分析只保留惰性路由计数器冷路径、Await 和业务 Selector 等设计内逃逸；零分配断言
  证明内置成功热路径没有新增堆分配；
- 最终 Review 修复 Selector 期间较早 Node 恢复连接导致候选下标换人的竞态，并增加
  确定性回归测试与 64 分片 TCP 不可变活动会话视图；
- 与 M19 无关的 `internal/tcpnet/options.go`、`internal/tcpnet/conn_test.go` 修改未纳入
  M19 提交。
