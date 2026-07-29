# Origin 第三版 M15 NATS 远程调用端到端闭环实施计划

> 当前状态：已执行完成；实现、验证与提交范围均按本计划验收

> **For agentic workers:** REQUIRED SUB-SKILL: Use
> `superpowers:subagent-driven-development` (recommended) or
> `superpowers:executing-plans` to implement this plan task-by-task. Steps use checkbox
> (`- [ ]`) syntax for tracking.

**目标：** 在不复制生成客户端、Dispatcher 和业务 Codec 的前提下，实现 NATS RPC
端到端闭环，并在同一里程碑内完成已经确认的 TCP 协议、配置和队列收敛。

**架构：** `rpc.Runtime` 在冻结配置后直接持有 TCP `remoteRuntime` 或 NATS
`natsRuntime`，不建立热路径 Transport 大接口。两种 Transport 共用服务发现解析、静态
Dispatcher、RequestID、调用完成、目标端 Deadline 和错误语义；TCP 继续使用 M5 Buffer
所有权，NATS 入站直接唯一移交 nats.go 的只读 `Message.Data`。

**技术栈：** Go 1.26.5、Origin M5/M6/M8/M9/M11～M14、nats.go v1.52.0、
nats-server/v2 v2.14.3、标准库 `crypto/rand`、`encoding/binary`、`context` 和 `sync`。

## 全局约束

- 代码、测试和 Benchmark 使用详细中文注释，公开 API 使用中文 GoDoc。
- 新行为严格执行测试先行；每个测试必须先因目标能力缺失而失败，再编写最小实现。
- RPC 热路径不使用反射，不创建每消息 goroutine，不创建每 RPC Go Timer。
- Request/Notify 不增加完整 payload 复制；NATS 成功 Response 最多复制一次业务 payload。
- 单个业务 payload 默认上限为 `4M`；公开字段固定为 `max_payload_size`。
- TCP 出站队列字段固定为 `send_queue_messages`；NATS 入站回调队列字段固定为
  `receive_queue_messages`，二者默认 `16384`、最大 `65536`。
- NATS 每本地 Node 最多 `65536` 个 pending；TCP 每目标连接最多 `65536` 个 pending。
- Running 与 Retired 都正常处理 RPC；框架不因 Retired 自动拒绝调用。
- Origin v3 尚未发布，不保留 M13 开发期 TCP Wire 或旧配置字段兼容分支。
- M15 完成前必须通过 Windows/Linux 全量测试、Race、Fuzz、覆盖率、Benchmark 和三平台
  构建，并使用真实三节点 NATS 集群回归。

---

### Task 1：公共配置语义与严格 Transport 选择

**文件：**

- 修改：`rpc/config.go`
- 修改：`rpc/types.go`
- 修改：`application/config.go`
- 修改：`application/application_test.go`
- 修改：`rpc/config_test.go`
- 修改：`node/config.go`

**接口：**

- 产出：`TransportNATS`、`Config.MaxPayloadSize`、`TCPConfig.SendQueueMessages`、
  `TCPConfig.ReadIdleTimeout`、`NATSConfig`、`NATSAuthConfig`、`NATSTLSConfig`。
- 约束：`Config.Validate()` 严格验证 TCP/NATS 对应配置块和各自必填字段。

- [x] **Step 1：写配置迁移失败测试**

```go
func TestLoadConfigAcceptsNATSAndRejectsLegacyRPCFields(t *testing.T) {
    // 有效 NATS 配置必须得到 TransportNATS、4M payload 和 16384 接收队列。
    // max_message_size、send_queue_frames、read_timeout 和同时出现 tcp/nats 必须失败。
}
```

- [x] **Step 2：运行测试确认因新字段尚不存在而失败**

```text
go test ./application ./rpc -run 'Test.*(Config|NATS|Legacy)' -count=1
```

- [x] **Step 3：实现最小配置结构和校验**

```go
type Config struct {
    Transport      string
    MaxPayloadSize int
    TCP            *TCPConfig
    NATS           *NATSConfig
}

type NATSConfig struct {
    Namespace            string
    URLs                 []string
    ReceiveQueueMessages int
    Auth                 NATSAuthConfig
    TLS                  NATSTLSConfig
}
```

- [x] **Step 4：更新消费字段并运行配置测试**

```text
go test ./application ./rpc ./node -count=1
```

---

### Task 2：M5/M6 数量队列和 NATS 基础边界收敛

**文件：**

- 修改：`internal/tcpnet/options.go`
- 修改：`internal/tcpnet/queue.go`
- 修改：`internal/tcpnet/conn.go`
- 修改：`internal/tcpnet/*_test.go`
- 修改：`tests/integration/tcpnet/tcpnet_test.go`
- 修改：`internal/natsnet/options.go`
- 修改：`internal/natsnet/conn.go`
- 修改：`internal/natsnet/subscription.go`
- 修改：`internal/natsnet/event.go`
- 修改：`internal/natsnet/message.go`
- 修改：`internal/natsnet/*_test.go`
- 修改：`tests/integration/natsnet/*.go`

**接口：**

- 产出：只按槽位限制的 `tcpnet` 发送队列。
- 产出：只按消息数限制的 NATS Subscription、合法的
  `Reconnect.BufferSize == -1`、`Conn.MaxPayload()`。
- 不变量：内部 `tcpnet.SendQueueFrames` 和 `natsnet.PendingMessages` 保持底层原生语义。

- [x] **Step 1：写数量队列和禁用重连缓冲失败测试**

```text
TestSendQueueOnlyLimitsMessageCount：连续放入小消息直到条数上限，断言下一条返回队列满；消息字节数不参与判断。
TestReconnectBufferMinusOneDisablesBuffer：使用 -1 通过校验并传给 nats.go，其他负数仍返回配置错误。
TestSubscriptionUsesUnlimitedPendingBytes：创建订阅后读取 nats.go 限制，断言消息数受限、字节数为 -1。
TestConnReportsServerMaxPayload：连接真实 NATS 后断言 MaxPayload 与服务器 INFO 中的 max_payload 一致。
```

- [x] **Step 2：运行测试确认历史字节字段使测试失败**

```text
go test ./internal/tcpnet ./internal/natsnet ./tests/integration/tcpnet ./tests/integration/natsnet -count=1
```

- [x] **Step 3：删除历史字节额度并实现服务器上限读取**

```go
func newSendQueue(maxMessages int) *sendQueue
func (conn *Conn) MaxPayload() int64
```

NATS 创建订阅时固定调用：

```go
raw.SetPendingLimits(resolved.PendingMessages, -1)
```

- [x] **Step 4：运行 M5/M6 单元与真实协议集成测试**

```text
go test ./internal/tcpnet ./internal/natsnet ./tests/integration/tcpnet ./tests/integration/natsnet -count=1
```

---

### Task 3：统一非零 uint64 SessionID 与发现目录

**文件：**

- 修改：`node/node.go`
- 修改：`node/node_test.go`
- 修改：`discovery/types.go`
- 修改：`internal/discovery/raw.go`
- 修改：`internal/discovery/directory.go`
- 修改：`internal/discovery/source.go`
- 修改：`internal/discovery/*_test.go`
- 修改：`node/discovery_runtime.go`
- 修改：`node/discovery_runtime.go`
- 修改：`tests/integration/rpcfixture/remote_rpc_process_test.go`

**接口：**

- 产出：Node、公开 Discovery、内部目录和 RPC `RemoteRoute` 统一使用 `uint64 SessionID`。
- 产出：`newSessionID() (uint64, error)` 使用 `crypto/rand`，零值重试。

- [x] **Step 1：写 uint64、零值拒绝和会话替换失败测试**

```text
TestNewSessionIDIsNonZeroUint64：生成多次 SessionID，断言每次非零且类型为 uint64。
TestDirectoryRejectsZeroSessionID：向目录写入零 SessionID，断言返回稳定配置错误且目录未变化。
TestSessionChangeProducesLostThenDiscovered：同一 NodeID 换成新 SessionID，断言先派发旧实例 Lost，再派发新实例 Discovered。
```

- [x] **Step 2：运行测试确认字符串 SessionID 不能满足新契约**

```text
go test ./node ./discovery ./internal/discovery ./tests/integration/rpcfixture -count=1
```

- [x] **Step 3：一次性迁移类型、生成和序列化夹具**

```go
func newSessionID() (uint64, error) {
    for {
        var raw [8]byte
        if _, err := rand.Read(raw[:]); err != nil {
            return 0, err
        }
        if id := binary.BigEndian.Uint64(raw[:]); id != 0 {
            return id, nil
        }
    }
}
```

- [x] **Step 4：运行发现、Node 和现有 TCP 集成测试**

```text
go test ./node ./discovery ./internal/discovery ./tests/integration/rpcfixture -count=1
```

---

### Task 4：TCP Wire v1 精简与配置适配

**文件：**

- 修改：`rpc/wire.go`
- 修改：`rpc/wire_test.go`
- 修改：`rpc/remote_session.go`
- 修改：`rpc/inbound_handler.go`
- 修改：`rpc/remote_runtime.go`
- 修改：`rpc/remote_target.go`
- 修改：`rpc/*_test.go`
- 修改：`tests/integration/rpcfixture/remote_rpc_*`

**接口：**

- 产出：11B Hello、6B HelloAck、22B Request、10B Notify、12B Response、一字节
  Ping/Pong。
- 产出：`RemainingTimeoutMillis uint32` 向上取整，零值和超过约 49.71 天拒绝。

- [x] **Step 1：用字面量黄金包写新 Wire 失败测试和 Fuzz Seed**

```go
func TestTCPWireV1ExactLayouts(t *testing.T) {
    // 断言固定字节，不使用被测 encode 函数生成期望值。
}
```

- [x] **Step 2：运行测试确认旧 ORP1 Magic 和旧头长度导致失败**

```text
go test ./rpc -run 'Test.*(Wire|Hello|Request|Response|Timeout)' -count=1
```

- [x] **Step 3：实现新布局并迁移 TCP 会话**

```go
const (
    tcpWireVersion       = byte(1)
    tcpRequestFixedSize  = 22
    tcpNotifyFixedSize   = 10
    tcpResponseFixedSize = 12
)
```

- [x] **Step 4：运行 RPC 单元、Fuzz 冒烟和双进程 TCP 回归**

```text
go test ./rpc ./tests/integration/rpcfixture -count=1
go test ./rpc -run '^$' -fuzz Fuzz -fuzztime 5s
```

---

### Task 5：共享目标端 Deadline 与 TCP 两阶段停止

**文件：**

- 新建：`rpc/inbound_deadline.go`
- 新建：`rpc/inbound_deadline_test.go`
- 修改：`rpc/remote_runtime.go`
- 修改：`rpc/inbound_handler.go`
- 修改：`internal/tcpnet/listener.go`
- 修改：`internal/tcpnet/listener_internal_test.go`
- 修改：`internal/tcpnet/listener_test.go`
- 修改：`node/node.go`

**接口：**

- 产出：TCP/NATS 共用的 `inboundDeadlines`，每请求只登记 M8 DeadlineQueue。
- 产出：`Listener.StopAccept(ctx)` 只停止 Accept、保留已接受 Conn。
- 产出：`Runtime.BeginStop(ctx)` 停止入站；`Runtime.Close()` 最终关闭 Transport 和
  Deadline。

- [x] **Step 1：写 Deadline 一次完成和 StopAccept 保留连接失败测试**

```text
TestInboundDeadlineCompletesOnce：让完成与超时并发竞争，断言请求只完成一次且 Deadline 绑定被删除。
TestListenerStopAcceptKeepsAcceptedConnections：建立连接后调用 StopAccept，断言新连接失败、旧连接仍能双向收发。
TestTCPBeginStopKeepsAdmittedResponsePath：先投递请求再 BeginStop，断言已接收请求仍能返回响应，最终 Close 才关闭连接。
```

- [x] **Step 2：运行测试确认当前 Close 同时关闭所有连接**

```text
go test ./rpc ./internal/tcpnet ./node -run 'Test.*(Deadline|StopAccept|BeginStop)' -count=1
```

- [x] **Step 3：实现最小共享 Deadline 和两阶段资源边界**

```go
func (listener *Listener) StopAccept(ctx context.Context) error
func (deadlines *inboundDeadlines) Bind(delay time.Duration, cancel context.CancelCauseFunc) (timerwheel.DeadlineID, error)
func (deadlines *inboundDeadlines) Close(cause error)
```

- [x] **Step 4：运行 TCP、Node 生命周期和 Race 定向测试**

```text
go test ./rpc ./internal/tcpnet ./node ./tests/integration/rpcfixture -count=1
go test -race ./rpc ./internal/tcpnet ./node
```

---

### Task 6：ORN1 静态线协议

**文件：**

- 新建：`rpc/nats_wire.go`
- 新建：`rpc/nats_wire_test.go`
- 新建：`rpc/nats_wire_fuzz_test.go`
- 修改：`rpc/benchmark_test.go`

**接口：**

- 产出：39B Request、18B Notify、29B Response 的原地 Prepend 和只读解析视图。
- 产出：`0x11`、`0x12`、`0x13` PacketType 与严格尾部/名称/Session/Deadline 校验。

- [x] **Step 1：按设计字面量编写黄金包失败测试**

```text
TestNATSWireExactRequestLayout：按字段写入固定值，逐字节比较预期 39 字节固定头及名称、载荷顺序。
TestNATSWireRejectsZeroSessionAndDeadline：分别将来源会话、目标会话和剩余超时置零，断言编码或解码失败。
TestNATSWireRejectsTruncatedNames：截断 NodeID 或 ServiceName，断言解码失败且不返回部分有效包。
```

- [x] **Step 2：运行测试确认 ORN1 编解码尚不存在**

```text
go test ./rpc -run 'TestNATSWire' -count=1
```

- [x] **Step 3：实现最小无反射编解码**

```go
const (
    natsPacketRequest  = byte(0x11)
    natsPacketNotify   = byte(0x12)
    natsPacketResponse = byte(0x13)
)
```

- [x] **Step 4：运行单元、Fuzz 冒烟和 Benchmark**

```text
go test ./rpc -run 'TestNATSWire' -count=1
go test ./rpc -run '^$' -fuzz FuzzNATSWire -fuzztime 5s
go test ./rpc -run '^$' -bench 'NATSWire' -benchmem
```

---

### Task 7：NATS 出站 pending 与响应完成

**文件：**

- 新建：`rpc/nats_runtime.go`
- 新建：`rpc/nats_pending.go`
- 新建：`rpc/nats_runtime_test.go`
- 修改：`rpc/runtime.go`
- 修改：`rpc/client.go`
- 修改：`rpc/remote_session.go`

**接口：**

- 产出：每 Node 一张最多 `65536` 的 pending Map、一套 RequestID 和响应订阅。
- 产出：`remoteRequestHandle` 直接区分 TCP Session/NATS Runtime，不增加闭包或 Transport
  接口装箱。

- [x] **Step 1：写登记、回滚、取消、迟到响应和整体关闭失败测试**

```text
TestNATSPendingCapacityAndRollback：填满 65536 个 pending 后断言过载；模拟 Publish 失败并断言预占项立即回滚。
TestNATSPendingCompletesExactlyOnce：让响应、取消和终止并发竞争，断言 complete 只被调用一次。
TestNATSResponseValidatesBothSessions：分别篡改来源和目标 SessionID，断言 pending 保留；两个会话都匹配时才完成。
```

- [x] **Step 2：运行测试确认 NATS pending 尚不存在**

```text
go test ./rpc -run 'TestNATS(Pending|Response)' -count=1
```

- [x] **Step 3：实现短锁 pending 和一次完成**

```go
type natsPendingCall struct {
    targetSessionID uint64
    complete        func(*Buffer, error)
}

type remoteRequestHandle struct {
    tcp       *outboundSession
    nats      *natsRuntime
    requestID uint64
}
```

- [x] **Step 4：运行 pending 单元和 Race**

```text
go test ./rpc -run 'TestNATS(Pending|Response)' -count=1
go test -race ./rpc -run 'TestNATS(Pending|Response)'
```

---

### Task 8：NATS 入站、生命周期和服务发现闭环

**文件：**

- 新建：`rpc/nats_inbound.go`
- 修改：`rpc/nats_runtime.go`
- 修改：`rpc/runtime.go`
- 修改：`node/discovery_runtime.go`
- 修改：`node/node.go`
- 修改：`node/node_test.go`
- 修改：`internal/discovery/directory.go`
- 新建：`tests/integration/rpcfixture/nats_rpc_integration_test.go`

**接口：**

- 产出：稳定 Subject、请求/响应 Subscription、原始只读 Data 唯一移交、按需一次响应复制。
- 产出：发现路由同时校验 NodeID、ServiceName、Contract、Transport、SessionID。
- 历史产出：NATS 终态和 TCP Listener 永久失败触发发现撤销及受控 Node Stop 信号。
  2026-07-29 最新决策要求 M16 将其改为详细记录错误、撤销不可达发现并完成 pending，
  但不取消 Application，也不停止 Node 或 Service。

- [x] **Step 1：写真实嵌入式三节点端到端失败测试**

```text
TestNATSRPCAwaitAsyncNotifyAndRetired：真实 NATS 下验证 Await、Async、Notify；目标 Retired 后三种调用仍可处理。
TestNATSRPCRejectsNoRouteWrongTransportAndStaleSession：分别构造未发现、传输不一致和旧会话目录，断言发送前失败。
TestNATSRPCReconnectDoesNotReplayRequests：断开并恢复 NATS，断言重连期间拒绝新调用，旧请求没有被重放。
```

- [x] **Step 2：运行测试确认 NATS Runtime 尚未接入**

```text
go test ./tests/integration/rpcfixture -run 'TestNATSRPC' -count=1
```

- [x] **Step 3：实现 NATS Runtime 与 Node 绑定**

```go
func (runtime *natsRuntime) start(ctx context.Context, engine *timerwheel.Engine) error
func (runtime *natsRuntime) beginStop(ctx context.Context) error
func (runtime *natsRuntime) close()
```

- [x] **Step 4：运行三节点功能、故障和 Race**

```text
go test ./tests/integration/rpcfixture -run 'TestNATSRPC' -count=1
go test -race ./rpc ./node ./tests/integration/rpcfixture
```

---

### Task 9：过载、所有权和性能门禁

**文件：**

- 修改：`rpc/nats_runtime_test.go`
- 修改：`tests/integration/rpcfixture/nats_rpc_integration_test.go`
- 新建：`tests/integration/rpcfixture/nats_rpc_benchmark_test.go`
- 修改：`internal/natsnet/benchmark_test.go`
- 修改：`rpc/benchmark_test.go`

**接口：**

- 产出：Request 队列满错误响应、Notify 丢弃、慢消费者诊断和 Response 发布失败规则。
- 产出：32B、1KB、64KB、接近 4M 的分配、吞吐和延迟基线。

- [x] **Step 1：写过载与所有权失败测试**

```text
TestNATSRequestQueueFullReturnsStableError：填满 Service Ready 队列后发送 Request，断言收到 CodeServiceQueueFull。
TestNATSNotifyQueueFullDoesNotCloseConnection：填满队列后发送 Notify，断言消息被丢弃但 NATS 连接保持可用。
TestNATSResponseCopiesSuccessfulPayloadOnce：成功响应进入独立 Buffer 后修改原消息，断言业务看到的数据不变；错误响应不分配 Buffer。
```

- [x] **Step 2：运行测试确认缺失分支能够被捕获**

```text
go test ./rpc ./tests/integration/rpcfixture -run 'TestNATS.*(Queue|Copy|Overload)' -count=1
```

- [x] **Step 3：实现最小限频计数和资源释放**

正常热路径只使用已有原子计数；不得新增公共 Metrics、每消息日志或额外消息队列。

- [x] **Step 4：运行定向测试和 Benchmark**

```text
go test ./rpc ./tests/integration/rpcfixture -count=1
go test ./rpc ./tests/integration/rpcfixture -run '^$' -bench 'NATS|TCPWire' -benchmem
```

---

### Task 10：文档回写、全仓验收与 M15 提交

**文件：**

- 修改：`docs/design/milestones/M15-NATS远程调用端到端闭环设计.md`
- 修改：`docs/design/milestones/M13-TCP远程调用端到端闭环设计.md`
- 修改：`docs/design/milestones/M5-TCP网络基础库设计.md`
- 修改：`docs/design/milestones/M6-NATS基础库设计.md`
- 修改：`docs/design/milestones/里程碑设计文档复核清单.md`
- 修改：`docs/design/milestones/里程碑路线图.md`
- 修改：`docs/design/设计文档索引.md`
- 修改：`docs/plans/M15-NATS远程调用端到端闭环实施计划.md`

**接口：**

- 产出：实现结果、覆盖率、Benchmark、平台验证和仍延后到 M16 的边界记录。

- [x] **Step 1：运行格式、静态检查和全量测试**

```text
gofmt -w <本里程碑修改的 Go 文件>
go vet ./...
go test ./...
go test -race ./...
```

- [x] **Step 2：运行覆盖率、Fuzz 和 Benchmark**

```text
go test -coverprofile cover.out ./...
go tool cover -func cover.out
go test ./rpc -run '^$' -fuzz Fuzz -fuzztime 10s
go test ./rpc ./tests/integration/rpcfixture -run '^$' -bench . -benchmem
```

- [x] **Step 3：运行跨平台构建和 Linux 实测**

```text
scripts\buildwin.bat
scripts\buildlinux.bat
GOOS=darwin GOARCH=amd64 go build ./...
GOOS=darwin GOARCH=arm64 go build ./...
```

在 `192.168.8.3` 使用已安装 Go 和 `/opt/origin-nats/compose.yaml` 三节点集群执行：

```text
go test ./...
go test -race ./rpc ./node ./tests/integration/rpcfixture
```

- [x] **Step 4：复核工作树和提交整个 M15**

```text
git diff --check
git status --short
git add AGENTS.md application discovery docs errs internal node rpc tests
git commit -m "feat: 完成 M15 NATS 远程调用闭环"
```
