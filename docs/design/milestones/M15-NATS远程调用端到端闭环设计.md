# Origin 第三版 M15 NATS 远程调用端到端闭环设计

> 文档类型：里程碑设计（已确认，原 M14 顺延）
> 创建日期：2026-07-29
> 最后更新：2026-07-29
> 当前状态：已实现并完成 Windows、Linux 与真实三节点 NATS 集群验收

## 1. 顺延原因

TCP 和 NATS 远端发送已经统一要求先读取当前 Node 的本地服务发现快照。正式发现目录
已经由 M14 实现并验收，因此原 M14 NATS RPC 顺延为 M15。

本文保存已经确认的 NATS RPC 结论，并结合 M14 最终目录接口完成剩余开工 Review。

## 2. 已确认结论

### 2.1 Subject 与命名空间

- 配置提供显式 NATS RPC namespace，用于隔离开发、测试、预发布和生产环境；
- 示例 namespace：`game-prod`；
- 请求 Subject：`orpc.{namespace}.req.{targetNodeID}`；
- 响应 Subject：`orpc.{namespace}.resp.{sourceNodeID}`；
- 每个 Node 只建立 Node 级请求和响应订阅；
- ServiceName 留在 NATS RPC 线协议中；
- Subject 在 Node 启动时生成并缓存，RPC 热路径不重复拼接字符串；
- 不使用 Queue Group 完成普通精确 RPC 路由。

### 2.2 服务发现前置

- NATS 发送前必须查询 M14 当前 Node 的公共可见快照；
- 没有发现目标 `NodeID + ServiceName` 时立即返回 `CodeRPCNoRoute`；
- 不通过“向无订阅者 Subject 发布并等待 15 秒”代替服务发现；
- TCP 与 NATS 使用同一发现、契约、状态和错误判断；
- NATS 不建立第二份目标目录。

### 2.3 Transport 边界

- 每个 Node 只配置一种业务 RPC Transport；
- TCP Node 只直接调用 TCP Node；
- NATS Node 只直接调用 NATS Node；
- 同一 Application 可以混合运行 TCP/NATS Node；
- 首版不实现跨 Transport Bridge；
- 跨 Transport 目标返回 `CodeTransportUnavailable`。

### 2.4 Runtime 结构

- `rpc.Runtime` 直接选择 TCP 专用 Runtime 或 NATS 专用 Runtime；
- 不建立通用 `remoteTransport` 大接口；
- 不建立通用 Packet 抽象；
- 只共享 RequestID、Dispatcher、Deadline、错误和服务发现等真正共同的逻辑；
- 单个 Node 的热路径不通过 Transport 接口装箱或动态分派。

### 2.5 NATS 线协议

- M15 在实现 NATS RPC 的同时，按第 2.10 节统一精简已经由 M13 实现的 TCP Wire v1；
- NATS 使用独立、最小的 `ORN1` Envelope，但不在每条消息中写入四字节 ASCII Magic；
- 首字节 `PacketType` 同时表达协议代次和消息类型：`0x11` 为 v1 Request、`0x12`
  为 v1 Notify、`0x13` 为 v1 Response；未来不兼容布局使用 `0x2x`；
- 共享业务 payload 编解码、RequestID、MethodID、ServiceName、Deadline 和错误语义；
- Node 级请求与响应 Subject 保持稳定和简短，不追加 SessionID、ServiceName 或
  RequestID；
- NATS 没有 TCP 握手形成的逐 Node 连接身份，因此 `ORN1` 包头携带完成进程代次校验所需
  的来源和目标 SessionID；
- SessionID 统一改为由 `crypto/rand` 在 Node 启动时生成的非零 `uint64`，不混入
  NodeID Hash、系统时间或持久化计数器；NodeID 已经隔离碰撞域，额外混合不会增加
  64 位结果的熵；
- 不把完整 TCP Wire v1 再嵌入 NATS；
- TCP 和 NATS 只共享 SessionID、RequestID、MethodID、Deadline 与错误等真实共同语义，
  不为了形式统一让任一 Transport 携带无法使用的字段；
- 首版不增加压缩、Reserved 或预留 Flags。

### 2.6 契约校验

- 生成客户端携带的 ContractID 和完整 ContractFingerprint 必须在发送前与 M14 当前
  `NodeID + ServiceName` 发现快照一致；
- 发现快照同时返回该目标进程的 SessionID，目标契约在同一 Node 进程生命周期内冻结；
- Request 和 Notify 不再重复携带 ContractID 或 ContractFingerprint；
- 目标端使用 TargetSessionID 拒绝不是发给当前进程代次的消息，再以
  `ServiceName + MethodID` 执行静态分发；
- 不增加契约目录 Subject；
- 不增加远端契约缓存或模拟 TCP 握手；
- 发送前发现指纹不一致时直接返回稳定契约错误，不编码或发布业务 payload。

### 2.7 Deadline 传播与被调用方超时

- NATS Request 携带固定 4 字节 `RemainingTimeoutMillis uint32`；Notify 和 Response
  不携带该字段；
- 调用方按照统一 RPC 语义只选择一个有效 Deadline：显式 Context Deadline 优先，
  Context 没有 Deadline 时使用 Service/Node 默认值，最终内置默认值为 `15s`；
- 发送前只读取并计算一次剩余 Duration，向上取整到毫秒后写入 `ORN1`，该计算不创建
  新的 Timer、goroutine 或 Context；
- `1ns～1ms` 编码为 `1ms`，避免因线协议精度降低而让目标端提前超时；
- `0` 非法；最大值为 `4294967295ms`，约 `49.71` 天，超过上限直接返回参数错误，
  不静默截断；
- 线协议传播相对剩余时间，不传播 Unix 时间戳，避免依赖不同机器的系统时钟同步；
- 目标 Node 在解码、契约和目标校验完成后、进入 Service 队列前检查
  `RemainingTimeoutMillis`；值为零时直接按 `CodeDeadlineExceeded` 拒绝，不执行
  Service 业务逻辑；
- 合法 Request 使用目标 Node 共享的 M8 DeadlineQueue 登记一条本地 Deadline，覆盖
  Service 排队和业务执行，不为每个请求创建 Go Runtime Timer；
- 目标执行 Context 使用取消原因表达 Deadline；请求完成、准入失败或 Node 停止时立即
  清理对应 M8 条目；
- Redis、数据库以及目标业务继续发起的下游 RPC 应遵守该 Context；下游 RPC 发送前重新
  读取继承 Deadline 的剩余时间，因此整条调用链消费同一时间预算；
- 调用方的唯一 Deadline 负责结束本地 pending，目标端的唯一 M8 Deadline 负责释放目标
  进程资源。二者属于不同进程的本地执行边界，不是同一进程重复计时；
- 首版不增加 Cancel 消息。调用方手工取消后立即结束本地 pending，目标端最多继续到原
  `RemainingTimeoutMillis` 到期或正常完成；迟到 Response 由 RequestID 终态规则丢弃；
- Origin RPC 不调用 NATS 原生同步 Request，也不使用其本地等待 Timeout 代替上述语义。

相对剩余时间不会精确扣除普通网络传输耗时，因此目标端理论上可能比调用方多执行一段
网络抖动时间。首版接受这一极小偏差，以换取无跨机时钟依赖、固定四字节 Timeout 字段和
最简单的热路径；禁止重连缓冲延迟重放，避免该偏差扩大到秒级或分钟级。

该方案与 gRPC 的 `grpc-timeout` 和 Deadline 传播语义一致，但 `ORN1` 使用固定二进制
整数，避免文本单位解析。Dubbo 的 Deadline 倒计时和 Finagle 的跨进程 Deadline Context
也证明了“把调用时间预算传给被调用方”是成熟的分布式 RPC 机制，而不是 NATS Transport
自身的职责。

调研依据：

- [gRPC Deadlines](https://grpc.io/docs/guides/deadlines/)；
- [gRPC over HTTP/2：grpc-timeout](https://github.com/grpc/grpc/blob/master/doc/PROTOCOL-HTTP2.md)；
- [Apache Dubbo Deadline 机制](https://dubbo.apache.org/zh-cn/overview/mannual/java-sdk/tasks/framework/timeout/)；
- [Finagle Contexts](https://twitter.github.io/finagle/guide/Contexts.html)；
- [NATS Request-Reply](https://docs.nats.io/nats-concepts/core-nats/reqreply)。

### 2.8 Request、Notify 与 Response 最终布局

三种消息分别使用最小布局，不为了结构统一携带无意义字段。NATS 已经提供完整
`Message.Data` 长度，因此都不增加 PayloadLength。

Request：

```text
PacketType                 uint8 = 0x11
RequestID                  uint64
MethodID                   uint64
RemainingTimeoutMillis     uint32
SourceSessionID            uint64
TargetSessionID            uint64
SourceNodeLength           uint8
ServiceNameLength          uint8
SourceNodeID               []byte
ServiceName                []byte
BusinessPayload            []byte
```

固定部分为 `39B`。SourceNodeID 用于计算稳定响应 Subject；NATS 请求 Subject 只包含
目标 NodeID，无法推导发布者身份。两个一字节长度让解析器直接确定两个名称与业务 payload
边界。

Notify：

```text
PacketType                 uint8 = 0x12
MethodID                   uint64
TargetSessionID            uint64
ServiceNameLength          uint8
ServiceName                []byte
BusinessPayload            []byte
```

固定部分为 `18B`。Notify 不等待响应，因此不携带 RequestID、Timeout、SourceNodeID 或
SourceSessionID。

Response：

```text
PacketType                 uint8 = 0x13
RequestID                  uint64
StatusCode                 uint32
SourceSessionID            uint64
TargetSessionID            uint64
BusinessPayload            []byte
```

固定部分为 `29B`。Response 由 RequestID 找到本地 pending，不携带 ServiceName；
SourceSessionID 必须命中 pending 记录的目标进程代次，TargetSessionID 必须命中当前调用方
进程代次，避免迟到响应误命中新进程调用。

`PacketType` 代替原候选中的 `Magic[4] + Kind`：专属 Subject 已经隔离 Origin RPC，
一个字节足以同时完成协议代次和消息分类。Request 与 Notify 共用请求 Subject，业务
payload 又允许为空，因此两者仍必须有一个不可推导的类型标识。

### 2.9 断线重连

- NATS 正在断线或重连时拒绝新的 RPC，立即返回
  `CodeTransportUnavailable`；
- M6 通用 NATS 基础库仍可使用默认 `8M` Reconnect Buffer；M15 创建 Node 共享 RPC
  Connection 时必须显式设置 `Reconnect.BufferSize = -1`，映射到
  `nats.ReconnectBufSize(-1)`，禁用断线期间的发送缓冲；
- M15 实施时补充 M6 参数校验，使 `-1` 成为唯一合法的“禁用 Reconnect Buffer”值，
  其他负数继续报配置错误；
- 即使连接状态检查和 Publish 之间发生断线竞争，禁用缓冲也必须让该次 Publish 立即失败，
  不能在重连后延迟重放；
- 断线前已经提交的 Await/Async pending 保留到响应或原 Deadline；
- 不因瞬时断线立即完成这些 pending；
- 不自动重新发布或重试非幂等请求；
- NATS 连接终态关闭或重连耗尽时统一完成全部 pending；
- 恢复连接只服务后续新调用。

TCP 与 NATS 都在重连期间拒绝新调用、都不自动重发 Request/Notify，但已有 pending 的
物理归属不同：TCP pending 属于一条已经断开的目标连接，因此断线时立即失败；NATS pending
属于 Node 级共享响应 Subject，Broker 短暂切换不会证明目标 Request 已经丢失，因此保留到
合法 Response、原 Deadline 或 Connection 终态。该差异是 Transport 资源模型决定的，不
向生成客户端增加不同接口。

### 2.10 M15 同步实施的 TCP 线协议精简

M13 首次实现时使用四字节 ASCII `ORP1` Magic、字符串 SessionID、带 Kind 的 Response
和纳秒级 `uint64` Timeout。M15 引入 NATS RPC 时已经需要把 Node、Discovery 和 RPC
共享的 SessionID 统一为非零随机 `uint64`，因此在同一个里程碑内完成一次 TCP Wire v1
收敛，避免随后再次修改全部公共身份类型和协议测试。

Origin v3 尚未正式发布，不承诺旧 M13 开发期线协议与最终 v3 兼容。本次直接修正 TCP
Wire v1，不增加兼容分支、双协议解析、协商状态或旧协议回退。

#### 2.10.1 Magic 与协议版本

- 删除 Hello 和 HelloAck 中的四字节 ASCII Magic；
- Hello 首字节改为固定 `WireVersion uint8 = 1`；
- WireVersion 是 Origin TCP RPC 线布局常量，不来自配置、构建时间或服务发现；
- 未来只有线布局发生不兼容变化时才递增 WireVersion；
- HelloAck 位于同一条有序连接的固定握手响应阶段，不重复携带 WireVersion；
- 收到不支持的 WireVersion 时立即按传输协议错误关闭连接，不猜测字段、不尝试回退。

Hello 最终布局：

```text
WireVersion       uint8 = 1
SourceNodeLength  uint8
TargetNodeLength  uint8
TargetSessionID   uint64
SourceNodeID      []byte
TargetNodeID      []byte
```

固定部分为 `11B`。SourceNodeID 用于识别调用来源和执行“先建立者保留”的重复 NodeID
规则；TargetNodeID 与 TargetSessionID 共同拒绝错误地址、错误 Node 和陈旧发现代次。
TCP 连接本身已经唯一标识来源连接生命周期，M13 实现中的 SourceSessionID 只被保存而未
参与任何判断，因此从 TCP Hello 删除；NATS 没有逐 Node 连接，仍按第 2.8 节携带必要的
来源 SessionID。

HelloAck 最终布局：

```text
StatusCode        uint32
ServiceCount      uint16
ServiceEntries    []ServiceEntry
```

固定部分为 `6B`。服务端只有在 Hello 的 TargetNodeID 与 TargetSessionID 都命中自身时
才返回成功，因此 HelloAck 不再重复返回 NodeID、SessionID、对应长度或 WireVersion。
失败响应的 ServiceCount 必须为零。每个 ServiceEntry 继续使用：

```text
ServiceNameLength   uint8
ServiceName         []byte
ContractFingerprint [32]byte
```

ServiceCount、ServiceNameLength 和完整指纹仍用于有界预分配、确定性解析及握手阶段契约
校验，不属于冗余字段。

#### 2.10.2 TCP 业务包

Request 与 Notify 共用主动方到被连接方的一条连接，且有返回值的 RPC 也允许使用
Notify 调用，因此二者无法从方法契约、payload 长度或连接方向可靠推导，继续保留一字节
Kind：

```text
Request：
Kind                    uint8 = 1
RequestID               uint64
MethodID                uint64
RemainingTimeoutMillis  uint32
ServiceNameLength       uint8
ServiceName             []byte
BusinessPayload         []byte
```

Request 固定部分从 `26B` 收敛为 `22B`。RemainingTimeoutMillis 与 NATS 使用完全一致的
向上取整、零值拒绝和约 `49.71` 天上限规则。

```text
Notify：
Kind               uint8 = 2
MethodID           uint64
ServiceNameLength  uint8
ServiceName        []byte
BusinessPayload    []byte
```

Notify 固定部分保持 `10B`。

被连接方到主动方在握手后只可能发送 Response 或 Pong。Pong 是固定一字节包，Response
最小为十二字节，因此 Response 类型可以由连接角色和帧长度确定，不再携带 Kind：

```text
Response：
RequestID        uint64
StatusCode       uint32
BusinessPayload  []byte
```

Response 固定部分从 `13B` 收敛为 `12B`。主动方先识别唯一的一字节 Pong，其余合法帧按
Response 严格解析；小于十二字节、RequestID 为零或错误响应携带 payload 都是协议错误。

Ping 和 Pong 继续各使用一字节 Kind：

```text
Ping  Kind uint8 = 3
Pong  Kind uint8 = 4
```

理论上可以把零长度帧解释为心跳，但心跳不在高频业务热路径，每次节省一字节没有实际
延迟收益，反而会把意外空帧静默解释为存活信号。保留显式 Kind 使协议错误更容易定位。

Request 与 Notify 不能为了删除 Kind 改用 `RequestID=0` 区分，否则每个 Notify 反而
增加八字节；也不占用 MethodID 的标志位，避免改变稳定 ID 空间和碰撞规则。TCP 业务包
继续不携带 Magic、WireVersion、ContractID、ContractFingerprint 或 PayloadLength：
版本已经由握手确定，契约已经由 HelloAck 目录验证，payload 边界由 M5 四字节长度帧提供。

#### 2.10.3 实施边界与验收

M15 必须把以下工作作为一个不可拆分的协议迁移完成：

1. Node、Discovery、TCP RPC 和 NATS RPC 的 SessionID 全部改为非零随机 `uint64`；
2. 更新 TCP Hello、HelloAck、Request、Notify、Response、Ping 和 Pong 的编码、解析与
   固定头大小；
3. 保持 M5 外层四字节大端 FrameLength 不变；
4. 更新 TCP 握手、非法版本、错误方向、截断、尾部数据、SessionID、Deadline 和响应解析
   测试，并为线协议解析继续保留 Fuzz；
5. 更新 Windows/Linux 真实双进程 TCP 回归测试，确保 M15 没有破坏 M13 的 Await、
   Async、Notify、重连、心跳、Deadline、契约和 Buffer 所有权；
6. 保存精简前后的固定头大小、`ns/op`、`B/op`、`allocs/op` 和端到端延迟基线，确认
   TCP/NATS 热路径没有引入新增分配或完整 payload 复制。

### 2.11 NATS Message.Data 所有权与解码边界

固定依赖 `nats.go v1.52.0` 在进入异步订阅队列前，已经为每条入站消息建立独立的
`Msg` 和消息字节；回调返回后，只要上层仍持有 `Data` 切片，底层数组就不会被下一条消息
复用。M15 利用这个已经存在的所有权，不能为了形式统一无条件再复制一份完整 payload。

M6 的 Message.Data 契约在 M15 实施时同步明确为：

- Data 是只读字节，任何上层都不能原地修改；
- Handler 可以同步读取，也可以把该只读切片唯一移交给一个明确有界的异步任务；
- 持有者不需要调用 Release；最后一个 Go 引用消失后由 GC 回收；
- 业务若要修改、长期缓存或交给多个并发所有者，必须自行复制；
- 升级固定 nats.go 版本前必须重新检查源码行为并执行跨回调生命周期测试。

#### 2.11.1 Request 与 Notify

Node 请求订阅回调只执行以下有界步骤：

1. 检查 ORN1 最小长度、PacketType、名称边界和 `max_payload_size`；
2. 解析并校验 TargetSessionID、ServiceName、MethodID 和 Request Deadline；
3. 解析 Request 的 SourceNodeID、SourceSessionID 和 RequestID；
4. 取得 `BusinessPayload = Message.Data[payloadOffset:]` 的只读切片；
5. 把该切片唯一移交给目标 Service 的一个已接受 Task；
6. 立即返回 NATS 回调，不在网络 goroutine 中执行静态业务解码或业务方法。

Service Task 在取得唯一执行槽后直接用现有静态 Dispatcher 解码该切片。Task 正常完成、
业务错误或 panic 后都不再保存引用，使消息内存自然回收。Service 队列拒绝时，回调立即
放弃切片；Request 按统一错误回复，Notify 按第 3.1 节的限频诊断规则丢弃。

该路径不把 Request/Notify 复制到 BufferPool。nats.go 的原始入站分配已经发生，再复制
既不能消除该分配，还会增加一次完整 payload memcpy 和短时双份内存。

#### 2.11.2 Response

响应订阅回调先在原始 Data 上解析固定头、校验 SourceSessionID/TargetSessionID，并按
RequestID 从 Node 级 pending 表删除唯一记录：

- pending 不存在、调用已取消或响应迟到：不复制，直接放弃 Data；
- StatusCode 非零：不复制业务 payload，直接以稳定错误完成 pending；
- 成功且 payload 为空：使用现有零长度响应 Buffer 语义完成；
- 成功且 payload 非空：只按 BusinessPayload 的准确长度从 Application 共享 BufferPool
  取得 Buffer，复制一次业务 payload 后完成 pending。

响应保留这一次按需复制，是为了复用本地/TCP 已经实现的 `*rpc.Buffer` 完成链、Await
恢复、Async 回调、生成代码解码和统一 Release 规则。首版不增加外部 Buffer 模式、
`[]byte | *Buffer` 联合状态、每消息 Release 闭包或新的响应接口；这些复杂度大于省掉一次
响应 payload 复制的收益。

#### 2.11.3 出站与性能验收

生成代码继续直接把业务参数编码到带准确 headroom 的 BufferPool Buffer，M15 原地前置
ORN1 后调用 M6 Publish；Publish 返回后立即释放 Buffer，不再拼接第二份 Origin 消息。
nats.go 将数据复制进自身写缓冲是官方客户端所有权边界，M15 不增加第三次复制。

实施时必须：

1. 用真实 nats.go 回调保存 Data，在回调返回、连续接收其他消息并触发 GC 后验证内容不变；
2. 覆盖 Request/Notify 投递成功、队列拒绝、目标不存在、Deadline 到期和 panic 后引用
   释放；
3. 覆盖成功、空成功、业务错误、迟到和未知 RequestID Response 的零次或一次复制规则；
4. 对 32B、1KB、64KB 和接近 4M payload 比较本方案与“全部复制到 BufferPool”的
   `ns/op`、`B/op`、`allocs/op`、吞吐、P95/P99 和峰值存活堆；
5. 确认订阅回调不执行静态业务解码，不产生每消息 goroutine，也不新增完整 payload 副本。

如果 Benchmark 证明只读切片跨 Service 排队显著恶化峰值存活堆或尾延迟，应停止实施并
把数据交回开发者复核，不能静默改成全复制策略。

### 2.12 Node 级 pending 上限

每个使用 NATS RPC 的本地 Node 固定最多保存 `65536` 个尚未进入终态的 Await/Async
Request。该值不预分配 Map，也不增加配置项。Notify 不创建 RequestID、Deadline 或
pending，因此不占用该额度。

pending 从“登记成功、准备 Publish”开始占用，直到以下任一唯一终态释放：

- 收到并验证合法 Response；
- 调用方 Context 取消或 Deadline 到期；
- Publish 立即失败并回滚登记；
- NATS Connection 进入不会再恢复的关闭终态；
- Node/RPC Runtime 正式停止。

达到 `65536` 时，新 Await/Async 必须在编码完成但 Publish 之前立即返回
`CodeTransportOverloaded`，请求 Buffer 所有权仍由调用方回收；不能先发布后发现无处登记，
也不能阻塞等待旧 pending 腾出位置。

NATS 使用一个 Node 级 RequestID 空间、一个响应 Subject、一个响应 Subscription 和一张
pending Map。首版使用一把短 Mutex 保护“容量检查、登记、删除和整体分离”的线性化点；
不预先增加分片 Map、对象池、Channel Semaphore 或逐目标计数目录。完成回调、Service
调度、日志和 Buffer Release 都必须在锁外执行。

Connection 终态关闭时，在锁内先关闭新登记并把整张 pending Map 与 Runtime 分离，随后
在锁外遍历旧 Map，以 `CodeTransportUnavailable` 完成全部调用；不为批量关闭额外复制
一个同等长度的 pending Slice。

#### 2.12.1 与 TCP 上限的关系

TCP 和 NATS 都使用数值 `65536`、相同过载错误、相同“不预分配、不配置、不池化”原则，
但资源粒度有意不同：

- TCP：每条有向目标 Node 连接最多 `65536`；
- NATS：当前本地 Node 通过共享 NATS Connection 发出的全部目标合计最多 `65536`。

这不是上层 RPC 调用语义分叉，而是底层资源模型不同。TCP 的 pending 表、故障和响应关联
天然属于独立连接；NATS 的 RequestID、响应订阅和 Connection 是 Node 级共享资源。为了
表面一致而给 NATS 增加逐目标计数，会增加热路径 Map 查询，并允许总量扩大到
`65536 × 目标 Node 数`，失去共享连接的明确内存上限；反过来把 TCP 改成 Node 全局原子
计数，又会让原本独立的连接在热路径竞争同一缓存行。首版不做这两种形式统一。

用户可观察行为仍保持一致：达到所属 Transport 的内部容量时 Await/Async 快速返回同一个
`CodeTransportOverloaded`，Notify 不占 pending，业务接口、超时、取消和响应语义不变。

#### 2.12.2 测试与性能门禁

实施时至少覆盖：

1. 第 `65536` 条可以登记，第 `65537` 条在 Publish 前被拒绝；
2. Response、取消、超时和 Publish 失败都准确归还一个额度；
3. 重复、迟到、未知 RequestID Response 不重复扣减；
4. Connection 终态整体分离后不再允许登记，全部旧 pending 严格完成一次；
5. 多 goroutine 并发登记、完成和取消通过 Race，不突破硬上限；
6. pending Map 不预分配，pendingCall 继续以值保存，不增加对象池；
7. 保存空表、普通并发、接近上限和集中关闭时的 `ns/op`、`B/op`、`allocs/op` 以及
   P95/P99；若单 Mutex 出现明确竞争瓶颈，再带 Benchmark 结果与开发者确认是否分片。

## 3. 最终 Review 结论

### 3.1 入站拒绝、Notify 丢弃与慢消费者

NATS Request Subscription 回调完成固定头校验和目标查找后，只尝试一次目标 Service
准入。不同结果使用以下稳定规则：

- `Running` 与 `Retired` 都按普通规则准入 Request、Notify 和 Broadcast；
- `Retired` 只是服务发现可观察状态，框架不得自动拒绝、丢弃或移出普通路由；
- 业务若希望退休期间拒绝特定操作，可以主动返回 `CodeServiceRetired`；
- Service FIFO 已满时，Request 返回 `CodeServiceQueueFull`；
- Service 尚未就绪时，Request 返回 `CodeServiceNotReady`；
- Service 正在停止时，Request 返回 `CodeServiceStopping`；
- Notify 没有响应；准入失败时直接释放消息引用、增加丢弃计数并限频诊断；
- 不阻塞 NATS 回调、不等待队列空间、不自动重试，也不因为一次 Service 拒绝关闭共享
  NATS Connection。

TCP 必须使用同一组 Service 准入规则。特别是 M13 早期设计中“Retired 返回
`CodeServiceRetired`”的描述失效，M15 同步清理 TCP 实现和回归测试，不能让相同 RPC 因
Transport 不同产生状态语义差异。

错误 Response 发布失败时不重试原 Request，也不关闭共享连接；调用端 pending 继续由
本次 Deadline 或连接终态唯一完成。目标端只进行限频基础设施诊断，不能为同一请求创建
第二条隐藏重试路径。

NATS 本地异步 Subscription 达到 Pending 上限后，nats.go 可能在 Origin 回调前丢弃消息。
此时框架已经无法取得被丢弃 Request 的 RequestID，不能补发错误 Response；调用端最终
由 Deadline 完成。处理规则固定为：

1. 不自动关闭 Connection，不重建 Subscription，不重放消息；
2. 第一次立即记录警告，之后同类警告最多每分钟一次；
3. 日志包含 Subject、当前 Pending 消息数和累计 Dropped，不记录 payload；
4. Request 拒绝、Notify 丢弃和慢消费者使用原子累计值，不在正常 RPC 热路径取时间或
   获取诊断锁；
5. M15 不为此增加公共 Metrics 系统或新的 Node 配置项。

### 3.2 队列容量名称与消息数量限制

2026-07-29 最终确认覆盖 M5、M6 和 M13 的早期“双重数量/字节上限”设计。TCP 与 NATS
RPC 的短期队列都只保留消息数量限制：

- TCP 每条连接使用 `send_queue_messages`，表示出站等待发送的完整 RPC 消息数量，默认
  `16384`，最大 `65536`；
- NATS 每条 Request/Response Subscription 使用 `receive_queue_messages`，表示已经从
  Broker 收到、但尚未完成 Subscription 回调的入站消息数量，默认 `16384`，最大
  `65536`；Request 与 Response Subscription 各自拥有独立额度；
- TCP 不再使用 `send_queue_bytes` 或 RPC Adapter 内部派生的发送字节额度；
- NATS 不再使用 `pending_size`、`PendingBytes` 或 Pending 字节额度；
- NATS Adapter 把 `receive_queue_messages` 映射为 nats.go
  `SetPendingLimits(receiveQueueMessages, -1)`，只让官方客户端限制消息数量；
- 任一消息即使业务 payload 为空，也占用一个队列位置；
- 队列满立即返回或执行第 3.1 节的拒绝规则，不阻塞、不静默覆盖旧消息。

`send_queue_messages` 与 `receive_queue_messages` 不是同一队列：前者是 TCP 出站队列，
后者是 NATS 入站回调队列。NATS 普通 Publish 不在 Origin 包装层额外建立待发送消息队列，
RPC 使用的 NATS Reconnect Buffer 也固定关闭，因此不存在可与 TCP
`send_queue_messages` 对齐的 NATS 出站配置。禁止把二者强行合并为方向不明的
`queue_messages`。

`max_payload_size` 与队列数量无关。它始终表示**单个 RPC 业务 payload 的最大字节数**，
默认 `4M`，用于发送前校验、接收分配保护和反序列化边界。三个容量概念必须在配置和代码
注释中明确区分：

| 概念 | TCP | NATS | 含义 |
|---|---|---|---|
| 单个业务载荷大小 | `rpc.max_payload_size` | `rpc.max_payload_size` | 单个业务 payload 最大字节数 |
| TCP 出站队列 | `tcp.send_queue_messages` | 不增加 | 尚未完成发送的完整 RPC 消息数量 |
| NATS 入站回调队列 | 不适用 | `nats.receive_queue_messages` | 尚未完成 Subscription 回调的消息数量 |
| 在途调用数量 | 每目标连接固定 `65536` | 每本地 Node 固定 `65536` | Await/Async pending 数量 |

只保留数量限制会使最坏内存同时受“队列数量 × 单消息上限”影响。该取舍由开发者明确选择，
目的是减少双计数、双配置和两种 Transport 的行为差异；项目允许接近 `max_payload_size`
的大包高并发时，应降低对应消息数量，而不是重新增加第二套字节队列。

M5 通用 `tcpnet` 和 M6 通用 `natsnet` 当前实现仍保留历史字节计数；M15 实施时同步删除
相关字段、校验、统计和测试，避免 RPC Adapter 通过“设置极大值”伪装为单限制。公开 RPC
配置和内部 Transport 原生名称分层处理：

- `rpc.Config.MaxMessageSize`、`DefaultMaxMessageSize` 和配置
  `max_message_size` 在 M15 统一迁移为 `MaxPayloadSize`、
  `DefaultMaxPayloadSize` 和 `max_payload_size`；
- `rpc.TCPConfig.SendQueueFrames` 和配置 `send_queue_frames` 在 M15 统一迁移为
  `SendQueueMessages` 和 `send_queue_messages`；
- `rpc.TCPConfig.ReadTimeout` 和配置 `read_timeout` 在 M15 统一迁移为
  `ReadIdleTimeout` 和 `read_idle_timeout`；
- `internal/tcpnet.Options.MaxMessageSize`、`SendQueueFrames`、`ReadTimeout` 继续描述完整
  TCP 帧、帧槽位和底层读空闲超时，不对业务配置公开，不做没有收益的机械改名；
- `internal/natsnet.SubscriptionOptions.PendingMessages` 继续贴近 nats.go 原生 Pending
  语义；M15 的 NATS RPC Adapter 负责从 `receive_queue_messages` 映射，不把底层名泄漏到
  Node 配置。

### 3.3 两阶段优雅停止

RPC Runtime 内部停止固定拆为“停止入站”和“最终关闭”两个阶段，TCP 与 NATS 使用相同
上层顺序：

1. 收到正式 Stop 后，原子关闭入站 RPC 准入；
2. 从服务发现撤销当前 Node 的公开 Service；
3. TCP 只停止 Listener Accept，不关闭已经接受的 Conn；NATS Drain Request Subscription；
4. 已经进入 Service FIFO 的 Running、Ready、Waiting 和 Async pending 按统一规则排空；
5. 尚未准入的 Request 返回 `CodeServiceStopping`，Notify 丢弃并计数；
6. 按实际启动顺序的严格反序执行 Service `OnStop(ctx)`；
7. 排空与 `OnStop` 期间继续保留出站 TCP/NATS、NATS Response Subscription、pending、
   DeadlineQueue 和 TimerEngine；
8. `OnStop` 因此可以正常 Await DBService 等依赖完成存档；
9. 全部 `OnStop` 返回后关闭新的出站 RPC 准入；
10. 等待剩余 pending 进入终态，再关闭 TCP 出站连接或 Drain NATS Response
    Subscription 与 Connection；
11. 最后关闭 RPC DeadlineQueue、TimerEngine 和其他 Node 基础设施。

TCP 与 NATS 的 Transport 细节有意不同，但用户可观察语义必须一致：

| 阶段 | TCP | NATS |
|---|---|---|
| 停止新入站 | 关闭入站准入并停止 Accept | 关闭入站准入并 Drain Request Subscription |
| 已准入任务 | 不因连接关闭撤回 | 不因订阅停止撤回 |
| 排空/OnStop 出站 | 保留已有出站连接和重连管理 | 保留 Connection 与 Response Subscription |
| 最终 pending | 按响应、Deadline 或终态完成一次 | 按响应、Deadline 或终态完成一次 |
| 最终 Transport | 关闭入站 Conn、出站会话和重连管理 | Drain Response Subscription 和 Connection |

当前 M5 `Listener.Close` 会同时关闭监听 socket 和全部已接受 Conn，不能直接用于第一阶段。
M15 为内部 `tcpnet.Listener` 增加“只停止 Accept”的最小能力：关闭监听 socket 并等待
AcceptLoop 退出，但保留已接受 Conn。这样 Stop 边界前已经准入的 Request 仍能通过原连接
返回 Response，Stop 边界后的 Request 可以得到 `CodeServiceStopping`，而不是全部退化为
断线。最终关闭阶段再复用 `Close` 回收这些 Conn，不为此建立第二套 Listener 类型。

### 3.3.1 Response 过载差异

TCP 和 NATS 都不能阻塞 Service Runner 等待 Response Transport 恢复，但物理资源粒度不同：

- TCP Response 无法进入当前连接发送队列时，关闭这一条目标连接，使该连接上的 pending
  立即以 Transport 错误完成；
- NATS Response Publish 失败时，不关闭整个 Node 共享 Connection，否则一个调用会连带
  失败所有无关目标；该 Request 不重试，调用端由 Deadline 或 Connection 终态完成；
- NATS Response Subscription 发生慢消费者并丢弃响应时，同样无法恢复具体 RequestID，
  调用端由 Deadline 完成；
- 两种 Transport 都不重发可能已经产生业务副作用的 Request，也不在 Service Runner 内
  阻塞等待。

Request Subscription Drain 超时后强制 Close 并继续停止。总体 Stop Context 到期时，剩余
pending 由实际原因唯一完成：Deadline 到期使用 `CodeDeadlineExceeded`，Transport 被强制
关闭使用 `CodeTransportUnavailable`。任何路径都不得自动重发 Request 或 Notify。

Retired 不是 Stop，也不触发上述两阶段流程。Retired Service 继续正常接收和发送 RPC、
运行 Timer 与处理其他业务，只通过发现事件把状态交给业务观察。

M13 当前实现的 `BeginStop` 会在 `OnStop` 前同时关闭入站和出站，这只是 M13 已声明的临时
顺序。M15 先把 TCP/NATS Runtime 改造成内部两阶段资源边界，M16 再完成 Application、
Node、Service finalizer 的最终编排。

### 3.4 Namespace、NodeID 与 NATS 配置

NATS RPC `namespace` 必填，不提供静默默认值，也不从 `--app-name` 推导。生产环境通常
一个 Node 一个进程，不同进程的 AppName 可能不同；显式 namespace 才能可靠隔离开发、
测试、预发布和生产环境。

Namespace 与 NodeID 统一使用以下 63 字符以内的小写 kebab-case：

- 必须以 `a`～`z` 开头；
- 后续只允许小写字母、数字和单个 `-`；
- 不允许连续 `--`；
- 不允许以 `-` 结尾。

NodeID 示例为 `gateway-1`、`game-12`、`db-cn-east-1`。点号、下划线、通配符、空白和大写
字母都在配置冷路径拒绝，使 NodeID 可以安全作为一个 NATS Subject Token。

NATS 配置继续属于每个 Node；Application 不拥有默认 Transport：

```yaml
nodes:
  - id: game-1
    scheduler:
      max_tasks: 20000
      max_await_tasks: 10000
      default_await_timeout: 15s
    rpc:
      transport: nats
      max_payload_size: 4M
      nats:
        namespace: game-prod
        urls:
          - nats://nats-1:4222
          - nats://nats-2:4222
          - nats://nats-3:4222
        receive_queue_messages: 16384
        auth:
          username: ${NATS_USERNAME}
          password: ${NATS_PASSWORD}
        tls:
          enabled: false
          ca_file: ""
          cert_file: ""
          key_file: ""
          server_name: ""
          insecure_skip_verify: false
    services:
      - PlayerService
```

固定内部规则：

- Connection Name 自动生成为 `origin-rpc-{namespace}-{nodeID}`；
- RPC 专用 Connection 固定 `no_echo=true`；
- 重连发送 Buffer 固定 `-1`，重连期间新调用快速失败；
- `receive_queue_messages` 默认 `16384`、最大 `65536`，分别应用到 Request 和 Response
  Subscription，不再存在字节上限；
- username/password、token、Credentials File 和 NKey Seed File 四种认证方式互斥；
- TLS 支持服务端校验和可选双向证书；
- 凭据不得进入日志、错误或配置快照诊断；
- RPC 完整 NATS 消息上限为业务 `max_payload_size` 加固定包头和名称边界；
- Node 启动时校验 NATS Server 公布的 `max_payload`，不足时直接启动失败；
- Subject 在 Node 启动时生成并缓存，RPC 热路径不拼接字符串。

Node 的 NATS RPC 配置只公开项目确实需要选择的 namespace、Server 地址、接收队列容量、
认证和 TLS。`no_randomize`、连接与基础操作超时、Drain 超时、Ping、最大未响应 Ping 和
重连次数/等待/抖动继续存在于 M6 `internal/natsnet.Options`，由 M15 RPC Adapter 使用固定
安全默认值，不逐项暴露到业务配置。真实生产场景和可重复数据证明需要调整时，再单独
Review 最小高级配置，避免 Node 配置与 nats.go 版本细节强耦合。

`transport` 与配置块使用严格互斥规则：

- `transport: tcp` 必须提供 `tcp`，不得同时提供 `nats`；
- `transport: nats` 必须提供 `nats`，不得同时提供 `tcp`；
- 省略整个 `rpc` 时只支持同 Node RPC，不创建网络资源；
- 配置解码仍然拒绝未知字段，旧的 `max_message_size`、`send_queue_frames`、
  `read_timeout`、`subscription.pending_messages` 在 M15 迁移后直接报配置错误，不建立
  长期兼容别名。

### 3.4.1 Transport 基础设施终态

Node 的 RPC 入站能力是公开 Service 的基础设施。以下状态已经不能只记日志后继续假装
Node 可用：

- TCP Listener 在非正常 Stop 期间永久关闭或 AcceptLoop 退出；
- NATS Connection 进入不会恢复的关闭终态，或有界重连次数耗尽。

发生上述状态时，Node 必须先撤销服务发现中的公开 Service，再触发受控 Node Stop；上层
Application 按既有失败规则收尾，并由 Kubernetes、systemd 或其他进程管理器决定是否
重启。这样不会让发现目录长期保留一个实际已经无法接收 RPC 的 Node。

以下局部故障不升级为 Node Stop：

- 单个 TCP 目标连接断开，只影响该目标并按 M13 规则有界重连；
- NATS 慢消费者、Service 队列满和单次 Publish 失败按过载或调用错误处理；
- Retired 只是业务可观察状态，继续允许收发 RPC。

### 3.5 M15 同批迁移清单

本轮配置复核涉及已经完成的 TCP 代码，不能只修改 NATS 新代码。M15 实施计划必须把以下
迁移作为同一批原子变更：

1. `application` 配置镜像、`rpc.Config`、默认常量、校验错误和所有示例迁移到
   `MaxPayloadSize`/`max_payload_size`；
2. TCP RPC 公开配置迁移到
   `SendQueueMessages`/`send_queue_messages` 和
   `ReadIdleTimeout`/`read_idle_timeout`；
3. TCP RPC Adapter 继续映射到内部 `tcpnet.Options.SendQueueFrames`、
   `MaxMessageSize` 和 `ReadTimeout`，不机械修改 M5 的协议原生字段；
4. 从 M5 删除 `SendQueueBytes`、字节原子计数、派生函数、校验、统计和相关测试；
5. NATS RPC 新增 `ReceiveQueueMessages`/`receive_queue_messages`，分别映射到 Request 和
   Response Subscription 的 `PendingMessages`；
6. 从 M6 删除 `PendingBytes`、字节 Pending 统计、校验、日志字段和相关测试，并使用
   `SetPendingLimits(messages, -1)`；
7. NATS RPC Node 配置只公开第 3.4 节最小字段；M6 低层 Options 保留通用能力和固定默认；
8. TCP/NATS Transport 配置块严格互斥，迁移前旧字段和无效组合都具有配置失败测试；
9. 更新 TCP 单元、集成、双进程、Race、Fuzz、Benchmark 和 Windows/Linux 回归，证明字段
   迁移及队列简化没有改变 M13 的调用、重连、心跳、Deadline 和 Buffer 所有权；
10. 更新 NATS 三节点、慢消费者、队列边界、断线、重连耗尽和受控 Node Stop 测试；
11. 更新全部公开文档、示例和配置夹具，不允许代码接受旧名而文档只展示新名；
12. 本次迁移不增加兼容别名、弃用周期或双字段优先级，因为 Origin v3 尚未正式发布。

如果以后为 TCP 增加 TLS，`enabled`、`ca_file`、`cert_file`、`key_file`、
`server_name` 和 `insecure_skip_verify` 必须与 NATS `tls` 使用相同名称和语义；M15 不因
未来可能需要 TLS 而提前增加当前无效配置。

### 3.6 三节点故障测试与性能门禁

Windows 与 Linux 自动化测试都在测试进程中启动三个嵌入式 `nats-server`，再启动至少三个
Origin Node。普通测试不依赖 Docker 或外部主机；最终验收继续使用 Ubuntu 上现有的三节点
Docker Compose NATS 集群。

功能测试至少覆盖：

- Await、Async、Notify、空 payload、业务错误和框架错误；
- 基础类型、Go 结构体、顶层 Protobuf、嵌套 Protobuf 和自定义 Codec；
- 默认 `15s`、显式 Context Deadline、取消和目标 Deadline；
- 无路由、契约不匹配、Session 不匹配；
- Service 未就绪、Running、Retired、Stopping 和队列满；
- Retired 在 TCP/NATS 下都继续正常处理 Request、Notify 和 Broadcast；
- namespace 隔离和非法 NodeID/namespace；
- `32B`、`1KB`、`64KB` 与接近 `4M` payload；
- BufferPool、pending、Subscription 和 goroutine 最终全部回收。

故障测试至少覆盖：

- 停止任意一个 NATS Server 后连接到其余节点；
- 三个 Server 逐个滚动重启；
- 全集群停止再恢复；
- 重连期间新 RPC 快速失败且不进入隐藏发送缓冲；
- 断线前 pending 按响应、Deadline 或连接终态完成一次；
- 达到最大重连次数后的整体 pending 分离；
- Node 重启、SessionID 改变、迟到/重复/未知 Response；
- Request/Response Subscription 慢消费者；
- Service 队列满；
- 正常 Stop、Stop Context 到期和强制 Close；
- 认证、权限、TLS 和 Server `max_payload` 错误。

性能记录在 Windows/Linux 分别保存：

- ORN1 与同步迁移的 TCP Wire 编解码 `ns/op`、`B/op`、`allocs/op`；
- pending 登记、响应、取消和整体关闭；
- NATS Publish 到 Service 准入；
- TCP/NATS Await、Async、Notify 的 P50/P95/P99、吞吐和峰值存活堆；
- 100 个可发现目标时的路由开销；
- `receive_queue_messages` 队列接近上限时的尾延迟与恢复情况。

硬性门禁为：

1. Request/Notify 不增加第二份完整 payload 复制；
2. 成功 Response 最多复制一次业务 payload；
3. 错误、迟到和未知 Response 不复制 payload；
4. 不创建每消息 goroutine或每 RPC Go Timer；
5. pending 热路径不增加对象池分支；
6. TCP/NATS 都只维护消息数量队列，不再维护队列 payload 字节计数；
7. 并发登记、响应、取消、Stop 和整体关闭通过 Race；
8. TCP Listener 异常终止和 NATS 永久终态会撤销发现并触发受控 Node Stop；
9. 不设置脱离机器的固定 QPS 或微秒门禁；同机重复基准出现超过约 `10%` 的稳定退化时，
   必须定位原因并由开发者确认是否接受复杂度。

## 4. 实施验收记录

M15 于 2026-07-29 完成实现，最终代码严格保持以下边界：

- `rpc.Runtime` 直接持有 TCP 或 NATS Runtime，没有为热路径增加 Transport 大接口；
- TCP 与 NATS 共用服务发现解析、生成客户端、Dispatcher、Codec、目标端 M8 Deadline
  和稳定错误语义；
- NATS 每 Node 只创建一条 Connection、一个 Request Subscription 和一个 Response
  Subscription，Subject 固定为 `orpc.{namespace}.req.{node}` 与
  `orpc.{namespace}.resp.{node}`；
- NATS 重连缓冲固定关闭，重连期间新调用快速失败，断线前 pending 保留且 Request 不重放；
- Request/Notify 直接借用 nats.go 入站 `Message.Data`；成功 Response 按需复制一次，
  错误、迟到和未知 Response 不复制业务 payload；
- TCP/NATS 均只保留消息数量边界；历史发送字节额度和 NATS pending 字节额度已经删除；
- Running 与 Retired 均允许 RPC，Stopping 才停止新入站准入；
- TCP Listener 永久失败和 NATS Connection 终态先撤销发现，再取消 Application 唯一
  生命周期 Context，由串行控制路径执行优雅 Stop。

验证结果：

- Windows 11、Go 1.26.5：`go test ./...`、`go test -race ./...`、`go vet ./...`、
  覆盖率、Wire/Codec Fuzz 和 Windows/Linux 构建脚本全部通过；
- Ubuntu 26.04、Go 1.26.5：全量测试与 `go test -race ./...` 全部通过；
- 嵌入式三节点 NATS 集群覆盖跨 Broker Await/Async/Notify、复杂 Go 结构体嵌套
  Protobuf、Retired、单节点故障重连、旧 pending 不重放、过载、Deadline 与优雅 Stop；
- 已部署三节点 NATS 集群 `192.168.8.3:4222～4224` 的跨节点 RPC 测试通过；
- Broker 认证失败与 `max_payload` 不足均在 Node 启动阶段明确失败。

本机 Windows 固定 `100000` 次微基准：

| 基准 | ns/op | B/op | allocs/op |
|---|---:|---:|---:|
| TCP Request 头原地编码与解析 | 40.62 | 0 | 0 |
| NATS Request 头原地编码与解析 | 84.57 | 0 | 0 |
| NATS pending 登记与完成 | 32.37 | 0 | 0 |

Ubuntu 虚拟机固定 `1000` 次 32B Await 端到端延迟：

| Transport | 平均 | P50 | P95 | P99 |
|---|---:|---:|---:|---:|
| TCP | 28.370µs | 24.385µs | 60.461µs | 81.299µs |
| NATS | 56.477µs | 51.906µs | 103.309µs | 157.900µs |

这些数据是当前机器回归基线，不是脱离硬件环境的固定性能门禁。后续若同机重复基准出现
超过约 `10%` 的稳定退化，应先定位原因，再按开发指导原则决定是否用额外复杂度换取性能。

## 5. 开工门禁（历史记录）

M15 只有在以下条件全部满足后才允许编写实施计划：

1. M14 服务发现本地目录已经实现并验收；
2. 本文第 3 节全部最终结论已经写入并完成 Review；
3. M6 NATS 基础库与当前固定依赖版本重新完成兼容性检查；
4. TCP 与 NATS 共用的调用路径没有复制生成客户端、Dispatcher 或 Codec；
5. 真实三节点 NATS 集群测试方案已写入实施计划；
6. M15 实施计划包含第 2.10 节全部 TCP 协议迁移、回归测试和性能对比，不把共享
   SessionID 类型修改遗留到后续里程碑；
7. M15 实施计划包含第 3.2 节全部 TCP 配置重命名、M5/M6 历史字节额度删除和旧字段拒绝
   测试，不把 TCP 配置迁移遗留到后续里程碑。
