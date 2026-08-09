# Application RPC 与 Origin Discovery Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (- [ ]) syntax for tracking.

**Goal:** 将 RPC Transport 移到 Application 配置层，并让 Origin Discovery 复用 Discovery Node 的 RPC 传输自举，不再配置独立 Discovery 地址。

**Architecture:** 配置加载器把顶层公共 RPC 配置和 Node TCP Endpoint 归一化为独立的 rpc.Config。RPC Runtime 新增仅框架可用的 Discovery 系统通道：TCP 复用 Listener 上的独立控制连接，NATS 复用同一 Connection 的保留 Subject；Origin Provider 保留原有 TTL、会话和快照 Actor。

**Tech Stack:** Go、internal/tcpnet、internal/natsnet、Origin Provider、Go test/race/vet。

## Global Constraints

- 不兼容旧的 discovery.origin.server.listen/address 和 Node 级 rpc.transport。
- 一个 Application 只能使用 TCP 或 NATS 一种 RPC Transport；各 Node 仍独立拥有连接、订阅与恢复状态。
- Origin 自举不得查询服务发现目录，系统通道不得经公开 Client、生成代码或任意 ServiceName 调用。
- 保持 TTL、Session、ServerEpoch、全量快照、Retired 和恢复语义。
- 先测试后实现；配置、运行时、测试、教程和示例在同一迁移中完成。
- 不覆盖或提交本任务外的现有工作区改动。

---

## Files and responsibilities

| 文件 | 责任 |
| --- | --- |
| application/config.go | 顶层 RPC 解码、Node TCP Endpoint 合并和 Origin 自举目标校验。 |
| node/config.go、node/node.go | 在 Runtime Freeze 前注入 Origin 所需的私有系统通道。 |
| rpc/system.go（新建） | 冻结的系统通道注册、目标和处理器接口。 |
| rpc/system_tcp.go、rpc/system_nats.go（新建） | TCP 多路握手与 NATS 保留 Subject。 |
| rpc/{wire,inbound_handler,remote_runtime,nats_wire,nats_runtime}.go | 每一代 Listener/Connection 的系统通道和停止/容量隔离。 |
| internal/discovery/origin/{config,client,server}.go | 删除独立地址与 Listener/Dial，接入系统通道，保留 Actor。 |
| application、node、rpc、internal/discovery/origin 测试 | 配置、协议、生命周期、恢复和端到端验证。 |
| 教程、配置参考、示例和设计记录 | 移除可复制的旧配置。 |

### Task 1: 迁移并严格校验 Application RPC 配置

**Files:**

- Modify: application/config.go
- Modify: application/application_test.go
- Modify: internal/discovery/origin/config.go
- Modify: internal/discovery/origin/integration_test.go

**Produces:** applicationRPCConfigMirror（顶层 transport、公共限制、TCP 公共调优或完整 NATS）；originBootstrap（服务端 NodeID、TCP advertise 或 NATS namespace）；Origin Config.Server 只保留 Node。

- [ ] **Step 1: 写入失败测试。**

新增表驱动 loadConfig 用例：Origin 缺顶层 RPC、TCP Node 缺 Endpoint、NATS 下出现 Node RPC、同时出现 tcp/nats、旧 server.listen/address、错误 server.node。每例断言 errs.CodeInvalidConfig。

~~~go
if _, err := loadConfig(directory); !errs.IsCode(err, errs.CodeInvalidConfig) {
    t.Fatalf("loadConfig() error = %v", err)
}
~~~

- [ ] **Step 2: 运行失败测试。**

Run: go test ./application -run 'TestApplicationRPCConfig|TestApplicationOriginDiscovery' -count=1

Expected: FAIL；顶层 rpc 目前仍被拒绝，Origin 仍要求地址。

- [ ] **Step 3: 实现归一化。**

移除顶层 rpc 的保留字段拒绝；添加 decodeApplicationRPCConfig 和 decodeNodeTCPEndpoint。顶层 TCP 只接收队列/超时，Node TCP 只接收 listen/advertise；顶层 NATS 接收完整连接配置并禁止 nodes[].rpc。为每个 Node 深拷贝完整 rpc.Config。Origin 配置严格拒绝旧字段，并从完整 Node 列表生成 originBootstrap。

- [ ] **Step 4: 验证并提交。**

Run: go test ./application ./internal/discovery/origin -count=1

Expected: PASS。

Commit: git add application/config.go application/application_test.go internal/discovery/origin/config.go internal/discovery/origin/integration_test.go; git commit -m "feat: move rpc configuration to application scope"

### Task 2: 建立冻结的私有系统通道契约

**Files:**

- Create: rpc/system.go
- Modify: rpc/runtime.go
- Modify: rpc/runtime_test.go
- Modify: node/config.go
- Modify: node/node.go
- Test: node/node_test.go

**Interfaces:**

~~~go
type SystemKind uint8
const SystemDiscovery SystemKind = 1

type SystemTarget struct { NodeID string; Address string }
type SystemHandler interface {
    OnSystemOpen(SystemPeer)
    OnSystemMessage(SystemPeer, *Buffer) error
    OnSystemClose(SystemPeer, error)
}
type SystemPeer interface { Send(*Buffer) error; Close() }
~~~

Runtime.RegisterSystemHandler 只允许在 Freeze 前调用；Runtime.OpenSystem 只允许已注册的系统 kind。Node 使用私有 Factory Builder 把 Runtime 和 originBootstrap 注入 Origin；不得扩展公开 provider.Context。

- [ ] **Step 1: 写入失败测试。**

覆盖未知/重复 SystemKind、Freeze 后注册、业务入口伪造系统 ServiceName、以及 BeginStop 后业务入站拒绝但系统撤销仍可接收。

- [ ] **Step 2: 运行失败测试。**

Run: go test ./rpc ./node -run 'TestSystem|TestNode.*Discovery' -count=1

Expected: FAIL；当前没有系统通道和独立停止状态。

- [ ] **Step 3: 实现、验证并提交。**

注册表冻结为只读；Node 在识别 DiscoveryService 后、rpcRuntime.Freeze 前绑定 handler。自定义 Provider 仍使用原公开 SPI。

Run: go test ./rpc ./node -count=1

Expected: PASS。

Commit: git add rpc/system.go rpc/runtime.go rpc/runtime_test.go node/config.go node/node.go node/node_test.go; git commit -m "feat: add internal rpc system channel"

### Task 3: TCP Listener 多路承载 Discovery

**Files:**

- Create: rpc/system_tcp.go
- Modify: rpc/wire.go
- Modify: rpc/inbound_handler.go
- Modify: rpc/remote_runtime.go
- Test: rpc/wire_test.go
- Test: rpc/remote_runtime_test.go

**Contract:** TCP 首帧以独立 systemHelloKind 区分业务与系统，系统帧固定携带 SystemKind、源/目标 NodeID、源 SessionID 和控制 payload。业务 Hello、Request、Notify 和 Response 二进制布局不变。

- [ ] **Step 1: 写入失败测试。**

验证业务 Hello 继续成功；注册端能收到 Discovery 系统 Hello；错误 kind、错误目标、超大首帧、Actor 未 Ready 和业务帧进入系统 peer 均被拒绝。

- [ ] **Step 2: 运行失败测试。**

Run: go test ./rpc -run 'TestTCP.*System|TestInbound.*Hello' -count=1

Expected: FAIL；当前 inboundHandler 只能解析业务 Hello。

- [ ] **Step 3: 实现 TCP multiplexer。**

首帧分派业务或系统会话；系统会话只调用 SystemHandler，永不进入 resolveInbound。remoteRuntime 以 SystemTarget.Address 建立有界退避的独立控制连接。Listener 恢复重装冻结 handler。

- [ ] **Step 4: 保持容量隔离并验证。**

底层帧上限取业务完整包络和 provider.MaxSnapshotSize 控制包络的最大值；完成握手后分别执行业务/Discovery 上限。Discovery 保持 64 条发送队列，业务保持自身队列；两类连接都有保留额度和总上限。

Run: go test ./rpc -count=1

Expected: PASS。

Commit: git add rpc/system_tcp.go rpc/wire.go rpc/inbound_handler.go rpc/remote_runtime.go rpc/wire_test.go rpc/remote_runtime_test.go; git commit -m "feat: carry discovery over rpc tcp listener"

### Task 4: NATS Connection 承载 Discovery

**Files:**

- Create: rpc/system_nats.go
- Modify: rpc/nats_runtime.go
- Modify: rpc/nats_wire.go
- Test: rpc/nats_wire_test.go
- Test: rpc/nats_runtime_test.go

**Contract:** 服务端 Subject 为 orpc.<namespace>.sys.discovery.server.<server-node>；客户端 Subject 为 orpc.<namespace>.sys.discovery.client.<node>.<session>。系统 Subscription 与业务 Subscription 在同一个 NATS Connection generation 中建立和回收。

- [ ] **Step 1: 写入失败测试。**

验证 Subject 不与业务 req/resp 冲突、客户端先订阅再 Hello、旧代消息被丢弃、Broker payload 不足时报启动错误、共置 Discovery Server 因 NoEcho 走本地 peer。

- [ ] **Step 2: 运行失败测试。**

Run: go test ./rpc -run 'TestNATS.*System|TestNATS.*Payload' -count=1

Expected: FAIL；当前 NATS Runtime 只有 req/resp Subscription。

- [ ] **Step 3: 实现、验证并提交。**

入站消息复制到独占 Buffer 才投递 handler；代次重建全部系统 Subscription；NATS Connection 仍保持每 Node 一条。

Run: go test ./rpc -count=1

Expected: PASS。

Commit: git add rpc/system_nats.go rpc/nats_runtime.go rpc/nats_wire.go rpc/nats_wire_test.go rpc/nats_runtime_test.go; git commit -m "feat: carry discovery over rpc nats"

### Task 5: Origin Provider 改接系统通道

**Files:**

- Modify: internal/discovery/origin/client.go
- Modify: internal/discovery/origin/server.go
- Modify: internal/discovery/origin/wire.go
- Modify: node/discovery_server.go
- Modify: node/node.go
- Test: internal/discovery/origin/integration_test.go
- Test: application/application_test.go

- [ ] **Step 1: 写入 TCP/NATS Provider 失败测试。**

只配置 server.node，启动 Server 与 Provider 后必须完成首次权威同步；覆盖 Publish、Withdraw、TTL、Session 接管、ServerEpoch 和 NATS NoEcho 本地自举。

- [ ] **Step 2: 运行失败测试。**

Run: go test ./internal/discovery/origin ./application -run 'Test.*Origin.*(TCP|NATS|Lifecycle)' -count=1

Expected: FAIL；当前 Server 仍调用 tcpnet.Listen，Client 仍 Dial Server.Address。

- [ ] **Step 3: 保留 Actor，替换连接适配器。**

origin.Service 实现 rpc.SystemHandler；PrepareDiscovery 只准备 Actor，客户端通过 Runtime.OpenSystem 连接 originBootstrap。既有 Hello、心跳、发布、撤销、全量快照和 ServerEpoch 控制帧不改语义。client map 改以 SystemPeer 为键，在 OnSystemClose 沿用现有撤销逻辑。

- [ ] **Step 4: 修正启动停止顺序并验证。**

启动顺序固定为 RPC → Discovery Actor → Provider → 业务 Service；停止顺序为 Withdraw → 业务入站停止 → Service → Provider → Actor → RPC。BeginStop 不得切断系统 peer。

Run: go test ./internal/discovery/origin ./application ./node -count=1

Expected: PASS。

Commit: git add internal/discovery/origin node application/application_test.go; git commit -m "feat: bootstrap origin discovery through rpc transport"

### Task 6: 同步示例和文档

**Files:**

- Modify: examples/07-remote-rpc/{01-tcp-two-nodes,02-nats-two-nodes,03-route-and-broadcast}/config/application.yaml
- Modify: examples/08-discovery/01-origin-provider/{config/application.yaml,README.md}
- Modify: docs/baseline/v3.0/guides/{07.remote-rpc.md,08.discovery.md,reference/configuration.md}
- Modify: docs/baseline/v3.0/design/details/{2026-07-26-完整配置模型设计.md,2026-07-26-服务发现提供者设计.md}
- Modify: docs/baseline/v3.0/design/milestones/{M6-NATS基础库设计.md,M13-TCP远程调用端到端闭环设计.md,M15-NATS远程调用端到端闭环设计.md,M17-公共服务发现Provider与Origin内置发现设计.md}
- Test: application/application_test.go

- [ ] **Step 1: 写入示例配置失败测试。**

加载四个远程 RPC/Origin Discovery 示例，断言 loadConfig 成功；断言 Origin server 只有 node，NATS URLs 在 YAML 顶层只出现一次。

- [ ] **Step 2: 运行失败测试。**

Run: go test ./application -run TestTutorialRemoteRPCAndDiscoveryConfigs -count=1

Expected: FAIL；旧示例仍有 Discovery 地址或 Node 级 Transport。

- [ ] **Step 3: 迁移 YAML 和说明。**

TCP：顶层 rpc.transport: tcp 和公共调优，Node 保留 rpc.tcp.listen/advertise。NATS：顶层一次性配置完整 rpc.nats，Node 不再配置 RPC。所有 Origin server 删除 listen/address。教程明确 Node TCP endpoint 是自举地址，而 NATS 顶层值只是每 Node 独立连接的模板。

- [ ] **Step 4: 更新设计结论并验证。**

将旧的“Node 级 Transport/独立 Discovery TCP 控制通道/混用 TCP-NATS”结论更新为最终模型；不保留可复制的旧 YAML。

Run: go test ./application -run TestTutorialRemoteRPCAndDiscoveryConfigs -count=1

Expected: PASS。

Run: rg -n -S 'discovery\.origin\.server\.(listen|address)' docs examples

Expected: 仅迁移说明中的旧名，不存在现行配置。

Commit: git add examples docs/baseline/v3.0 application/application_test.go; git commit -m "docs: migrate rpc and discovery configuration examples"

### Task 7: 最终验证

**Files:**

- Modify: 仅在验证发现缺口时添加最小回归测试。

- [ ] **Step 1: 运行带竞态的核心回归。**

Run: go test -race ./application ./node ./rpc ./internal/discovery/origin

Expected: PASS，无 race、端口、订阅、Buffer 或 goroutine 泄漏。

- [ ] **Step 2: 运行全量验证。**

Run: go test ./...

Expected: PASS。

Run: go vet ./...

Expected: PASS。

- [ ] **Step 3: 复查最终差异。**

Run: git diff --check; git status --short

Expected: 无空白错误和意外文件。

## Self-review

- **Spec coverage:** Task 1 覆盖新配置；Task 2 覆盖私有装配；Task 3/4 覆盖两种传输与容量隔离；Task 5 覆盖 Provider、自举和生命周期；Task 6 覆盖本次审查发现的教程、示例、参考和设计漂移；Task 7 覆盖 race、全量和 vet。
- **Placeholder scan:** 没有未落实的占位术语或“写适当测试”类描述；每个任务给出测试命令与预期。
- **Type consistency:** SystemKind、SystemTarget、SystemHandler、SystemPeer 在 Task 2 定义，Task 3–5 使用；originBootstrap 在 Task 1 产生并由 Task 5 使用。
