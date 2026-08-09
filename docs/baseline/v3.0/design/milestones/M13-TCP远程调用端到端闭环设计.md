# Origin 第三版 M13 TCP 远程调用端到端闭环设计

> 文档状态：M13 开发期基线已实现并验收；最终 TCP Wire v1 精简并入 M15
>
> 创建日期：2026-07-28
>
> 最后更新：2026-08-01
>
> 前置里程碑：M5 TCP 网络基础库、M8 Node 时间轮内核、M9 Service 调度与协作式等待、
> M11 RPC 契约与代码生成、M12 Origin 自定义静态编解码扩展
>
> 配置与 Discovery 更新：TCP 共享参数现位于顶层 `rpc.tcp`，各 Node 只声明自己的
> `listen/advertise`。Origin Discovery 在同一 RPC Listener 上使用独立保留控制连接，
> 不再拥有 `server.listen/address`。

## 1. 目标

M13 在不改变 M11/M12 业务契约、生成客户端和数据编码规则的前提下，把一个 Node 的
RPC Runtime 与另一个 Node 的 RPC Runtime 通过 M5 TCP 基础库连接起来，形成第一个真实
跨 Node 端到端闭环。

本里程碑需要完成：

1. 固定 Origin v3 TCP RPC 线协议代次、字节序和错误布局；
2. 通过 `NodeID + ServiceName` 精确调用远端 Service；
3. 为 Request 建立 RequestID、pendingCall、超时、取消、响应和断线完成；
4. 为 Notify 保留无响应、无 pending 的低成本投递语义；
5. 每个 Node 独立监听、独立连接和独立 RPC Runtime；
6. 逻辑远端目标有效期间执行有界退避重连，但不自动重发已经失败的调用；
7. 使用完整契约指纹在业务 payload 解码前识别不兼容 Service；
8. 复用 Application 共享 BufferPool，并避免为了拼接 RPC 协议头复制完整 payload；
9. 单进程双 Node 与双进程单 Node 使用相同真实 TCP 路径；
10. 为 M15 NATS 适配保留最小的 Runtime 发送边界，但不建立复杂通用 Transport 框架。

M13 是 Transport 接入里程碑，不重新设计 RPC 外观。业务仍只使用 `origingen` 已生成的：

```go
client := contract.NewPlayerRPCClient(
    service,
    rpc.ToServiceOnNode("player-2", "PlayerService"),
)

player, err := client.AwaitGetPlayer(ctx, playerID)
err = client.AsyncGetPlayer(ctx, playerID, callback)
err = client.NotifyPlayerOnline(ctx, playerID)
```

生成客户端不持有 TCP 连接，也不增加 `AwaitNodeXxx`、`AsyncNodeXxx` 或
`NotifyNodeXxx` 等重复方法。

## 2. 不在 M13 实现

M13 不包含：

- Origin 内置服务发现、etcd Provider、TTL、关注筛选和 Lost/Retired 业务事件；
- 正式的 `static` Discovery Provider 或配置中的静态 peer 清单；
- 自动实例选择、轮询、随机、按 Key 取模和多 Node Broadcast；
- NATS RPC；它属于 M15；
- `OnStart` 生命周期 Await 属于 M14；`OnStop` 独占 Await 和最终优雅停止顺序属于 M16；
- RPC 请求取消帧和远端主动取消通知；
- 自动重发 Request、Notify 或 Broadcast；
- 流式 RPC；
- RPC payload 压缩及其协商字段；
- TLS、Node 身份认证、ACL 或跨不可信网络安全协议；
- TcpModule 业务语义；
- 业务可见 Buffer、借用 Slice 或零拷贝业务结果；
- 修改 M11/M12 已确认的基础类型、普通结构体、Protobuf 和自定义 Codec 编码规则。

M13 不为尚未实现的压缩预留 Flags、算法或协商字段。以后只有真实业务数据证明压缩可以
降低总体延迟或带宽成本时，才通过新的 WireVersion 引入完整设计，不能让每一条低延迟
小包长期承担无效字段。

## 3. v2 对照与 v3 调整

### 3.1 v2 值得保留的行为

Origin v2 已经具备以下正确方向：

- 每个已关注远端 Node 复用 RPC TCP 连接；
- 连接断开后自动重连；
- Request 使用序号关联响应；
- 连接断开时清理该连接相关等待调用；
- Notify 不等待响应；
- TCP 与 NATS 在业务调用层保持相近外观。

M13 保留这些语义，不照搬 v2 实现。

### 3.2 v2 不继续沿用的实现

v2 使用包级共享 CallSet、字符串 `Service.Method`、反射、Protobuf RPC 外壳和每秒轮询
超时。它还把压缩、Processor 选择、网络连接和 RPC 状态混在同一处理链中。

M13 改为：

- 每个 Node 一个独立 RPC Runtime，不使用包级可变全局状态；
- ContractID、MethodID 和完整契约指纹代替方法字符串和反射查找；
- 每条有向 Node 会话持有自己的 pending 表，降低无关 Node 之间的锁竞争；
- 调用方继续使用 M8/M9 已建立的唯一 Await Deadline，不再建立 v2 式一秒扫描器；
- 目标端使用 Node 共享 M8 DeadlineQueue 管理远端 Request 的执行截止时间；
- 固定轻量二进制协议头，不用 Protobuf 再包装一次 RPC 元数据；
- TCP 连接管理、RPC 状态和生成编解码分别保持明确边界；
- 队列、pending、连接数、包长、重连间隔和目标生命周期全部有界。

## 4. 核心结论

M13 固定采用以下单一方案：

1. TCP 连接按“关注方向”建立，而不是强制每对 Node 共用一条双向业务连接；
2. A 关注 B 时建立一条 A→B 有向连接；双方互相关注时最多存在两条物理连接；
3. 有向连接只承载发起端的 Request/Notify 以及接收端沿原连接返回的 Response/Pong；
4. 每个远端 Node 最多一个当前出站会话；已有会话未关闭前拒绝后来的重复连接；
5. NodeID 不放入每个热路径数据包，只在握手中固定连接两端身份；
6. 握手响应返回远端可公开 Service 的实际名称和完整契约指纹目录；
7. 每次 Request 只携带真正需要的 ServiceName、MethodID、RequestID 和剩余超时；
8. ContractID 和完整指纹不进入每次调用，完整指纹只在会话建立时检查；
9. Request 与 Notify 使用各自最小固定头，Notify 不携带无意义的 RequestID 和超时；
10. Response 只携带 RequestID、稳定错误码和成功 payload，不传动态错误消息；
11. 调用进入重连状态时立即返回 Transport 不可用，不在连接管理器中排队等待连接恢复；
12. 旧连接断开立即完成其全部 pendingCall；重连只服务后续新调用；
13. RPC Runtime 与 M5 TCP 之间只保留私有最小适配层，不建立公共大接口；
14. TCP RPC Adapter 直接放在 `rpc` 包的私有实现文件中，避免多一层只转发参数的包；
15. BufferPool 增加通用 headroom 操作，使 RPC 头可以原地前置，不复制完整 payload；
16. M13 不恢复正式 static Discovery；集成测试通过内部目标生命周期入口注入 NodeID 和地址；
17. TCP 打开或断开只记录内部状态和日志，不伪装成后续 Discovery Added/Lost 业务事件。

## 5. 分层与包边界

### 5.1 `rpc`

`rpc` 继续拥有：

- 生成客户端使用的 `Client` 和 `Target`；
- 本地 Service 目录和 Dispatcher；
- RequestID 分配；
- 本地/远端统一调用完成状态；
- 出站会话、pending 表和远端契约目录；
- RPC 线协议编码与解析；
- 私有 TCP Adapter；
- 远端目标增加、幂等刷新和显式移除的框架入口；
- 入站 Request/Notify 分发和 Response 关联。

M13 不新建公共 `transport` 包，也不让业务实现 RPC Transport。

### 5.2 `internal/tcpnet`

M5 `tcpnet` 保持通用网络层，只负责：

- 四字节长度帧；
- socket、Listener、Conn 和读写循环；
- Buffer 唯一所有权；
- 有界发送消息队列；
- ReadTimeout、WriteTimeout 和 TCP KeepAlive；
- 单次 Dial、立即 Close 和 Wait。

`tcpnet` 不识别 NodeID、RequestID、ServiceName、ContractID、MethodID 或 RPC 错误码，
也不自动重连。

### 5.3 `internal/bufferpool`

M13 只增加以下通用缓冲区能力：

- 取得带前置 headroom 的 Buffer；
- 在 headroom 内原地前置协议头；
- 解析后丢弃前缀并把 payload 作为新的有效视图；
- Release 时仍按原始完整容量归还和统计。

这些能力是协议缓冲区的通用所有权操作，继续放在已有 `bufferpool`，不另建只被一个
实现使用的算法包。

### 5.4 `node` 与 `application`

`application` 负责严格解析每个 Node 的 RPC 配置并交给 `node`。`node` 负责：

- 为每个 Node 创建唯一 RPC Runtime；
- 把 Node/Service 私有标志交给 Runtime；
- 在 Node TimerEngine 启动后启动 TCP RPC；
- 启动失败时逆序关闭已经建立的 RPC 资源；
- Stop/Rollback 时关闭新 RPC 准入、连接和 pending；
- 不共享不同 Node 的 Listener、连接管理器或会话。

## 6. 配置外观

TCP Transport 在 Application 顶层选择一次；全局限制与调优参数只配置一次，每个 Node 仅声明
自己的监听和对外地址：

```yaml
rpc:
  transport: tcp
  max_payload_size: 4M
  tcp:
    send_queue_messages: 16384
    read_idle_timeout: 15s
    write_timeout: 15s

nodes:
  - id: gateway-1

    scheduler:
      max_tasks: 20000
      max_await_tasks: 10000
      default_await_timeout: 15s

    rpc:
      tcp:
        listen: 0.0.0.0:7101
        advertise: 10.0.1.20:7101

    services:
      - GatewayService
```

规则如下：

1. 顶层 `rpc` 省略时所有 Node 都只支持同 Node RPC，不创建网络资源；
2. `rpc.transport` 选择 `tcp`，`nats` 到 M15 才有运行语义；
3. `nodes[].rpc.tcp.listen` 是当前 Node 绑定地址；
4. `nodes[].rpc.tcp.advertise` 是 Discovery 与远程调用对外使用的可达地址；
5. `advertise` 不允许通配地址或零端口；
6. 顶层 `rpc.max_payload_size` 表示业务 payload 上限，默认 `4M`；
7. TCP 实际长度帧上限为业务上限加固定 `512B` RPC 包络余量；
8. 接收端先用“业务上限 + 512B”限制完整帧分配，再在解析固定头后校验真实业务 payload；
9. 顶层 `rpc.tcp.send_queue_messages` 表示每条连接最多等待发送的完整 RPC 包数量，默认 `16384`，最大
   `65536`；
10. TCP RPC 队列只按 `send_queue_messages` 限制消息数量，不再配置或内部维护
    `send_queue_bytes`；
11. 队列达到消息数量上限时立即返回 Transport 过载；
12. `max_payload_size` 只限制单个 RPC 业务 payload 的字节大小，不表示队列长度；
13. `write_timeout` 默认 `15s`，必须大于零；
14. `read_idle_timeout` 默认 `15s`；`0s` 显式关闭应用层心跳和读空闲检测；
15. RPC 不增加独立 `default_timeout`，继续使用 Service/Node 的统一 Await 默认链；
16. M13 不增加 `peers`、`static_targets`、`heartbeat` 或 `reconnect` 配置段。

拨号超时、握手超时、重连退避和抖动先使用框架固定安全默认值。真实部署证明必须调整时，
再增加最小配置，避免把实现细节提前暴露给项目。

上述名称是 M15 后的最终公开外观。M13 已提交代码仍使用开发期字段
`max_message_size`、`send_queue_frames` 和 `read_timeout`；M15 在同步精简 TCP Wire 时
一次性迁移配置结构、错误信息、测试和示例，不维护两套字段或长期兼容别名。

## 7. 为什么采用有向连接

### 7.1 连接数量

设 A 关注 B：

```text
A -- Request / Notify --> B
A <-- Response / Pong --- B
```

A 的连接管理器主动拨号 B。B 不需要因为接受这条连接而关注 A。

如果 B 同时关注 A，则 B 再建立 B→A 连接。每对 Node 因此可能有：

- 零条连接：双方都不关注；
- 一条连接：单向关注；
- 两条连接：双向关注。

### 7.2 不采用“每对 Node 强制一条连接”的原因

一条全双工连接看起来可以减少 socket，但它要求双方对“谁主动拨号、谁拥有连接、重复
连接如何裁决”达成一致。更重要的是，服务关注天然可以不对称：B 关注 A 时，A 未必能从
自己的发现筛选中看到 B。

若使用按 NodeID 决定唯一主动方：

- 可能出现真正需要调用的一方不能主动拨号；
- Discovery 必须反向通知无关注关系的一方；
- 关注筛选、连接生命周期和重复连接裁决会明显变复杂。

有向连接与 v2 的基本行为一致，连接数量等于实际关注边数。后续关注筛选可以直接减少
连接，不需要额外协调协议，代码也更容易验证。

### 7.3 同一 Application 内的多个 Node

同一 Application 中的不同 Node 不允许通过 Go 指针或 Application 共享连接短路：

- `ToServiceOnNode` 指向当前 Node 自身时走同 Node Dispatcher；
- 指向另一个 Node 时，即使两个 Node 位于同一进程，也必须走真实 TCP；
- 每个 Node 使用自己的 Listener、出站管理器、pending 表和 RequestID 空间；
- 测试使用 loopback 地址复现生产中的编码、队列、握手、断线和重连行为。

### 7.4 本 Service 调用本 Service

本 Service 调用自己的 RPC 必须继续经过同 Node Dispatcher 和 Service FIFO，禁止为了减少
一次排队而直接内联执行 RPC 方法。内联执行会破坏“任意时刻只有一个任务持有 Service
执行权”的基本约束，也会让测试行为与正常 RPC 不一致。

三种调用的规则如下：

- `NotifyXxx`：把目标 RPC 任务放入当前 Service FIFO 后立即返回；当前任务结束或释放
  执行权后，目标任务才可以执行；
- `AsyncXxx`：先预留完成任务，再把目标 RPC 任务放入 FIFO；目标完成后，回调仍作为
  当前 Service 的普通任务串行执行；
- `AwaitXxx`：当前任务登记等待并释放 Service 执行权，目标 RPC 任务随后从同一 FIFO
  执行；响应唤醒原任务，原任务重新取得执行权后从 `AwaitXxx` 后继续。

因此，`AwaitXxx` 自调用不会因为“等待自己”而死锁；等待期间允许 Timer、其他 RPC 和本地
事件按既定调度规则交错执行。Dispatcher 对一个入站任务只调用一次真实业务实现，不会把
它再次转发给生成客户端，所以框架自身不会形成 RPC 转发环。

框架不对业务显式递归自调用维护调用栈或循环检测：Await 递归链会受到
`max_await_tasks` 和 Deadline 限制，但顺序 Notify/Async 仍可能被业务写成无限逻辑循环。
框架无法在不误伤合法状态机的情况下猜测业务意图；Service Stop 会停止后续准入，业务方法
本身必须具有终止条件。测试必须覆盖 Await、Async、Notify 自调用、Await 递归到期以及 Stop
能够终止后续自调用准入。

## 8. 远端目标生命周期

M13 为后续 Discovery 预先实现一套最小目标状态输入，但不实现 Provider：

```text
Absent
  └─ Add(NodeID, Address) → Backoff/Dialing

Backoff → Dialing → Handshaking → Ready
   ↑          │          │          │
   └──────────┴──────────┴──────────┘
            连接失败或断开

任意状态 ─ Remove(相同 NodeID、Address) → Closed
```

规则如下：

1. 目标只由 `NodeID + Address` 标识，不引入没有可靠来源的 Revision；
2. `Add` 相同 NodeID、相同 Address 是幂等刷新，不重复建连接；
3. 已存在相同 NodeID、不同 Address 时拒绝新的 Add，保留正在工作的旧目标和旧连接；
4. 地址迁移必须由 Provider 先完成 `Remove(旧 NodeID, 旧 Address)`，再
   `Add(新 NodeID, 新 Address)`，不能由后来地址隐式抢占；
5. Remove 必须同时匹配 NodeID 和 Address，迟到的旧地址 Remove 不能删掉新地址；
6. Remove、Node Stop 或目标 Context 结束时立即中断拨号、退避、握手和心跳；
7. Ready 之前的新调用立即返回 `CodeTransportUnavailable`，不等待重连；
8. M13 集成测试使用有限生命周期的内部目标源驱动 Add/Remove；
9. 后续 Origin/etcd Discovery 先在 Provider 内解决自身会话顺序，再复用同一入口；
10. 不把目标生命周期 API 宣布为业务项目的公共服务发现接口。

### 8.1 重连默认值

- 首次拨号立即执行；
- 单次拨号超时 `5s`；
- 单次握手超时 `5s`；
- 失败后从 `200ms` 开始指数退避；
- 退避上限 `5s`；
- 每次加入正负 `20%` 抖动；
- 成功完成握手并发布 Ready 后重置退避；
- 使用可停止并复用的 `time.Timer`，不使用 `time.Sleep`；
- 目标生命周期结束时停止重试。

重试间隔有上限，单次操作有 Deadline，总重试生命周期由目标 Context 限制。后续
Discovery 的 TTL/Lost 会成为生产环境的目标生命周期边界。

## 9. Node 握手

### 9.1 握手职责

握手只负责：

- 确认连接确实是 Origin v3 TCP RPC；
- 确认源 NodeID 和目标 NodeID；
- 返回目标 Node 当前公开 RPC Service 的契约目录；
- 在 Ready 前拒绝业务包。

握手不是身份认证。M13 假定运行于受信内部网络；需要 TLS、证书或授权时必须单独设计。

### 9.2 握手目录

握手响应为每个公开 RPC Service 返回：

```text
ServiceName + ContractFingerprint
```

`ContractID` 是生成期对完整 Go 包路径与 RPC interface 名称计算的稳定标识，不包含运行时
`ServiceName`。同一份 RPC 契约可以由不同 Service 实例实现，所以两者概念不能合并。

但是，握手目录中的 `ContractFingerprint` 已经覆盖契约全名、方法、参数顺序、内置 Codec、
自定义 Codec ID 与版本；请求中的全局 `MethodID` 也已经包含契约身份。因此 TCP 线上不再
重复携带 `ContractID`。Runtime 在调用前使用目录完成：

- ServiceName 不存在：`CodeRPCNoRoute`；
- 完整指纹不同：`CodeRPCContractMismatch`；
- 完全一致：允许发送。

单个 Service 不兼容不会关闭整个 Node 连接。这样在滚动更新或一个 Node 承载多个独立
Service 时，兼容 Service 仍可通信；不兼容目标在 payload 编码或发送前快速失败。

### 9.3 私有边界

- 私有 Node 的握手目录为空；
- 名称以 `_` 配置的私有 Service 不进入目录；
- 没有 Dispatcher 的 Service 不进入目录；
- 私有 Service 即使与某个公开 Service 位于同一 Node，也不能通过精确远端 Target 调用；
- 同 Node 本地查询和本地 RPC 不受 Discovery 私有规则影响。

### 9.4 重复入站连接

出站管理器保证同一目标只存在一个当前拨号流程，但误启动两个相同 NodeID 的进程，或者
旧连接尚未完全释放时重连，仍可能让目标端看到重复源 NodeID。

目标端采用“先建立者保留，后来者拒绝”：

1. Hello 通过协议和目标 NodeID 校验后，按 SourceNodeID 查询当前入站会话；
2. 已存在活动会话时，HelloAck 返回稳定冲突错误并关闭后来连接；
3. 后来连接不得替换、关闭或干扰已经处理业务的旧连接；
4. 只有旧连接完成关闭并从会话表移除后，新的握手才可以成功；
5. 若重连与旧连接关闭发生竞态，新连接暂时失败，主动方按既定退避重新连接。

该规则也使重复 NodeID 的误启动快速暴露，不需要 Revision 或“最新者优先”的复杂裁决。

## 10. TCP RPC 线协议

### 10.1 外层长度帧

M13 固定复用 M5：

- 四字节无符号长度；
- Big Endian；
- 长度只表示后续 RPC 线协议包字节数；
- 长度头由 M5 Conn 单独发送，不进入 RPC Buffer；
- 收到声明长度超过“业务上限 + 512B”时，在申请完整 Buffer 前关闭连接。

### 10.2 握手包

握手发生在 TCP 连接建立后的固定阶段：主动方第一包只能是 Hello，被连接方第一包只能是
HelloAck。因此握手包不携带 Kind、Reserved 或 Nonce。

M13 开发期首次实现使用四字节 ASCII `ORP1` Magic 和字符串 SessionID。M15 已确认在
引入 NATS RPC 和全局 `uint64` SessionID 时同步完成最终 TCP Wire v1 精简；Origin v3
尚未正式发布，不保留开发期协议兼容分支。最终协议删除 ASCII Magic，由 Hello 首字节
固定携带：

```text
WireVersion uint8 = 1
```

WireVersion 是框架固定的 TCP RPC 线布局代次，不从构建版本、配置或服务发现生成。未来
只有线布局发生不兼容变化时才递增；收到不支持的值立即按传输协议错误关闭连接，不猜测
字段或回退旧布局。

Hello 完整布局为：

```text
WireVersion       uint8 = 1
SourceNodeLength  uint8
TargetNodeLength  uint8
TargetSessionID   uint64
SourceNodeID      []byte
TargetNodeID      []byte
```

HelloAck 完整布局为：

```text
StatusCode        uint32
ServiceCount      uint16
ServiceEntries    []ServiceEntry
```

每个 `ServiceEntry` 为：

```text
ServiceNameLength   uint8
ServiceName         []byte
ContractFingerprint [32]byte
```

字段保留原因如下：

- WireVersion：用一个字节拒绝不兼容线布局，不在每个包重复四字节 ASCII Magic；
- SourceNodeID：识别调用来源，并拒绝相同 NodeID 的后来重复连接；
- TargetNodeID 与 TargetSessionID：发现错误地址、错误 Node 或陈旧服务发现代次；
- StatusCode：明确返回重复 NodeID、目标不符、契约目录过大等握手失败；
- ServiceCount 与各长度：提供有界、无反射的确定性解析；
- ContractFingerprint：在发送业务 payload 前检查实际 Service 契约兼容性。

TCP 连接本身已经唯一标识来源连接生命周期，开发期 Hello 中的 SourceSessionID 只被
保存而没有参与任何判断，因此删除。服务端只有在 TargetNodeID 和 TargetSessionID 都
命中自身时才返回成功，所以 HelloAck 不再重复 WireVersion、NodeID、SessionID 及对应
长度。Ack 位于同一条有序 TCP 连接上，不存在跨连接匹配问题，所以也无需 Nonce。

`StatusCode != 0` 时 `ServiceCount` 必须为零；主动方读取稳定错误码后关闭连接。全部多
字节整数使用 Big Endian。NodeID 和 ServiceName 都限制为 `1～255` 个 UTF-8 字节，
正好使用 `uint8` 长度；完整握手包仍受 RPC 线协议包上限保护。最终 Hello 和 HelloAck
固定部分分别为 `11B` 和 `6B`。

### 10.3 业务包类型

握手完成后，主动方到被连接方的 Request、Notify 和 Ping 使用首字节 Kind：

| Kind | 数值 | 允许方向 |
|---|---:|---|
| Request | 1 | 主动方 → 被连接方 |
| Notify | 2 | 主动方 → 被连接方 |
| Ping | 3 | 主动方 → 被连接方 |
| Pong | 4 | 被连接方 → 主动方 |

被连接方到主动方在握手后只可能发送 Response 或一字节 Pong；Response 最小为十二字节，
因此通过连接角色和帧长度确定，不再携带 Kind。Request 与 Notify 无法从方法契约、payload
或连接方向推导，继续保留一字节 Kind。未知 Kind、错误阶段、错误方向或非法长度都按协议
错误关闭连接。协议不为未来功能保留 Flags、Compression 或 Reserved 字节。

### 10.4 Request 与 Notify

Request 使用：

```text
Kind                    uint8 = 1
RequestID               uint64
MethodID                uint64
RemainingTimeoutMillis  uint32
ServiceNameLength       uint8
ServiceName             []byte
BusinessPayload         []byte
```

固定部分为 `22B`。约束如下：

- `RequestID != 0`；
- `MethodID != 0`；
- `RemainingTimeoutMillis > 0`；
- 剩余时间向上取整到毫秒，最大约 `49.71` 天，超过 `uint32` 上限直接返回参数错误；
- ServiceName 非空且不超过 255 字节。

Notify 不需要响应关联和执行 Deadline，使用更短的独立布局：

```text
Kind                    uint8 = 2
MethodID                uint64
ServiceNameLength       uint8
ServiceName             []byte
BusinessPayload         []byte
```

固定部分为 `10B`。MethodID 必须非零，ServiceName 使用相同限制。

`ServiceName` 选择目标运行时 Service，`MethodID` 全局定位已生成的静态方法；契约身份已经由
握手指纹验证，因此不重复传 ContractID。业务 payload 允许为零字节，外层 M5 长度帧已经
提供 payload 边界，所以业务包不再单独携带长度。

不使用 `RequestID=0` 表示 Notify，否则每个 Notify 反而增加八字节；也不占用 MethodID
标志位，避免改变稳定 ID 空间和碰撞规则。

### 10.5 Response

Response 使用：

```text
RequestID         uint64
ErrorCode         uint32
BusinessPayload   []byte
```

规则如下：

- 固定 Response 头为 `12B`；
- `RequestID != 0`；
- `ErrorCode = 0` 表示成功，payload 可以为空；
- `ErrorCode != 0` 表示失败，payload 必须为空；
- 只传 `errs.Code`，不传本地 error 指针、动态 Message、Stack 或底层 cause；
- 未识别的错误码仍由 `errs.New(code)` 保留数值；
- 目标业务 panic 只在目标 Node 记录一次 Stack，并返回 `CodeRPCExecutionPanic`。

主动方先识别唯一的一字节 Pong，其余合法帧按 Response 解析；小于十二字节、RequestID
为零或错误响应携带 payload 都是协议错误。连接方向已经提供类型边界，因此不重复携带
Response Kind。

### 10.6 Ping 与 Pong

Ping 和 Pong 都只有一个字节：

```text
Kind uint8
```

主动拨号方发送 `Kind=3` 的 Ping，被连接方通过同一连接回送 `Kind=4` 的 Pong。TCP 本身
保证同一连接内有序，不需要心跳 Nonce。它们不进入 Service 队列、不创建 pendingCall，
也不使用 RPC RequestID。

理论上可以把零长度帧解释为心跳，但心跳不在高频业务热路径，每次节省一字节没有实际
收益，反而会把意外空帧静默解释为存活信号，因此继续使用显式一字节 Kind。

### 10.7 压缩调研与结论

成熟 RPC 对压缩没有唯一规则：

- gRPC 提供可选的消息压缩、按调用控制与算法协商，但明确存在 CPU、延迟和安全权衡；
- Connect 也把压缩作为协议能力；
- Go 标准库 `net/rpc` 没有内置压缩，只允许使用者替换 Codec。

参考：

- <https://grpc.io/docs/guides/compression/>
- <https://connectrpc.com/>
- <https://pkg.go.dev/net/rpc>

Origin 面向游戏服务器内部低延迟 RPC，主要是已经紧凑编码的小包；当前没有 Benchmark
证明压缩能抵消算法、Buffer 和分支成本。因此 M13 不支持压缩，也不在线协议中预留压缩
字段。将来只有真实流量数据证明有收益时，才以新 WireVersion 设计完整的算法协商、
压缩前后大小上限和解压炸弹保护。

## 11. 发送与接收流程

### 11.1 Await Request

```text
生成客户端编码业务 payload
  → Runtime 解析精确 Target
  → 检查 Ready 会话和远端契约目录
  → 分配非零 RequestID
  → 在当前出站会话登记 pendingCall
  → 原地前置 RPC Request 头
  → 非阻塞提交 M5 发送队列
  → 调用方 goroutine 在 Service.Await 中等待
  → Response 按“会话 + RequestID”删除 pending
  → 恢复原 Service Task
  → 静态解码业务结果
```

pending 必须先登记，再允许包进入发送队列，避免 loopback 或高速网络响应先于 pending
发布。发送失败时撤销同一 pending，并由调用方释放尚未转移的请求 Buffer。

### 11.2 Async Request

Async 继续保持 M11 语义：

- 立即静态校验、目标校验或发送队列拒绝时直接返回 error，业务 callback 不执行；
- 返回 nil 后 callback 严格一次；
- callback 在 owner Service 的串行执行上下文执行；
- 响应网络协程不直接运行解码和业务 callback；
- 目标断线、超时和远端错误通过已经预留的完成任务进入 callback。

### 11.3 Notify

远端 Notify 返回 nil 只表示：

> 当前 Node 的目标会话存在，并且完整 RPC 包已经被本地 TCP 有界发送队列接受。

它不表示远端 socket 已经写完、远端 Service 队列已接受或业务方法已执行。若要获得远端
明确结果，契约必须带业务返回值并使用 Await/Async。

Notify 不创建 RequestID、pendingCall、响应或 Deadline 状态。发送队列满时立即返回
`CodeTransportOverloaded`。

### 11.4 入站 Request

TCP ReadLoop 只执行：

1. 校验并解析固定协议头；
2. 校验目标 Service、MethodID、payload 和剩余超时；
3. 为 Request 建立目标执行 Context、Deadline 绑定和只读响应会话引用；
4. 把完整 Buffer 唯一所有权转移给目标 Service FIFO；
5. 立即返回继续读包。

网络 goroutine 不调用业务方法、不等待业务结果，也不执行生成解码器。

目标 Service Task：

1. 检查远端执行 Context 是否已经到期；
2. 丢弃 RPC 头视图，只把业务 payload 借给 Dispatcher；
3. 运行静态解码、业务方法和静态响应编码；
4. 清除 Deadline 绑定；
5. 原地前置 Response 头；原连接仍可发送时非阻塞提交，已经断线时直接释放；
6. 最终释放或转移所有 Buffer。

### 11.5 入站 Notify

Notify 通过独立短头和相同目标校验后进入目标 Service FIFO，但不建立 pending、Deadline
或响应。队列拒绝、契约错误和业务 error 只在目标侧按既定规则记录；panic 根据 M16 最新
规则每次输出一次完整堆栈，不能限频省略。发送方不会收到结果。

## 12. pendingCall

### 12.1 表的归属

每个 Ready 出站会话持有自己的 pending Map：

```text
RequestID → pendingCall
```

不建立 v2 式全 Node 共享 CallSet。这样：

- 不同远端 Node 的响应不竞争同一把锁；
- 连接断开只需分离并完成自己的 Map；
- 新旧会话天然隔离；
- RequestID 相同也不能跨会话误完成。

每条会话最多 `65536` 个 pending Request，达到上限立即返回
`CodeTransportOverloaded`。Map 不按上限预分配，只按真实并发增长。

### 12.2 RequestID

- RequestID 由当前 Node RPC Runtime 的 `atomic.Uint64` 单调分配；
- 零值保留给 Notify 和无效状态；
- 达到 `MaxUint64` 后永久拒绝新远端 Request，要求重启 Node；
- RequestID 不在进程重启后保持；
- 响应必须同时命中原物理会话和 RequestID；
- 迟到响应找不到 pending 时释放 Buffer 并计入内部诊断，不关闭健康连接。

### 12.3 一次性终态

以下事件竞争同一个终态：

- 正常 Response；
- 调用 Context 取消；
- Deadline 到期；
- 发送失败；
- 物理连接断开；
- 目标被 Remove；
- Node Runtime 关闭。

只有从 pending 表成功删除记录的一方可以提交终态。任何迟到事件只能观察“记录不存在”，
不得保存并直接调用一个可能已经失效的 `pendingCall` 副本。

### 12.4 调用方出站断线清理

连接关闭时：

1. 在短锁内把会话标记为 Closed，并整体分离 pending Map；
2. 禁止新 pending 登记；
3. 释放锁；
4. 遍历旧 Map，以 `CodeTransportUnavailable` 完成全部调用；
5. 唤醒 Await 或投递已预留 Async 完成任务；
6. 不自动重发请求；
7. 重连创建全新会话和空 pending Map。

完成回调和 Service 调度不能在 pending 锁内执行。

### 12.5 被调用方已接收任务的断线规则

Request 或 Notify 成功进入目标 Service FIFO 后，就已经成为被调用 Node 接受的工作。随后
调用方断开 TCP 时：

1. 已排队但尚未开始的任务继续等待并执行，不从 Service FIFO 中扫描或删除；
2. 已经开始的任务继续执行，不因网络断开取消它的 Context；
3. Request 仍受原包携带的 `RemainingTimeoutMillis` 限制，Node/Service Stop 仍按各自规则
   终止或排空；
4. 业务中的 Redis、数据库和其他阻塞调用应遵守该 Context，框架不能强杀 Go goroutine；
5. 执行结束时若原会话已经关闭，直接释放 Response Buffer，不发送、不重试，也不产生
   每条响应一条的错误日志；
6. 已经进入 M5 发送队列但尚未写出的 Response，由连接 Close 按 M5 所有权规则释放；
7. Notify 始终没有确认或响应，断线不改变它已经被接收的事实。

目标任务持有的是不会复用的会话对象引用；会话 Close 只原子改变状态并关闭网络资源，
不会把对象放入池中。任务结束时调用会话的非阻塞 Send，Closed 状态直接返回并由当前任务
释放 Buffer，因此不需要引用计数或按连接维护活动业务任务表。

这样不需要为每条入站连接维护“断线时遍历并取消全部业务”的活动 Request Map，也不会让
网络抖动中断已经产生副作用的业务。调用方收到 `CodeTransportUnavailable` 后无法判断远端
是否已经执行；框架绝不自动重发。业务若手工重试非幂等操作，必须在契约中携带幂等键。
Origin 保证的是“每个成功解析并准入的网络帧最多自动投递一次”，不承诺跨断线重试的
Exactly Once。

### 12.6 对象池决策

M13 最终不为 `pendingCall` 增加对象池，采用最小值类型：

```go
type pendingCall struct {
    complete func(*Buffer, error)
}
```

1. `pendingCall` 直接以值存入会话 Map，不为每次调用单独 `new`；
2. 响应、超时、取消和断线都先按 RequestID 从原会话 Map 删除，再调用取出的完成函数；
3. 迟到事件只会观察到记录不存在，不持有可复用对象指针，因此没有对象池 ABA 和 Reset
   状态遗漏；
4. Windows 基准约为 `35.03 ns/op`，Linux 基准约为 `31.23 ns/op`，两端均为
   `0 B/op`、`0 allocs/op`；
5. 逃逸分析只保留长生命周期会话和 Map 的预期逃逸，没有每次 pendingCall 独立堆分配；
6. 对象池不能再减少当前热路径分配，却会增加取得、归还、Reset 和所有权分支，因此不启用。

同 Node `localCall` 继续保持 M11 已确认的未池化实现。两条路径共享一次性完成语义，但不为
形式统一引入公共池或远程字段。

## 13. Context、Deadline 与取消

### 13.1 调用方唯一计时

调用方继续遵守 M9/M11：

- Context 已有显式 Deadline 时只使用调用方现有 Go Runtime Timer；
- Context 没有 Deadline 时只使用 Service/Node 默认链和一条 M8 Deadline；
- `context.Background()` 进入允许普通 Context 的 Async 入口时仍使用同一默认链；
- RPC 不建立第二个调用方 Timer；
- 默认值仍为 `15s`，并可由 Node 配置或 Service `SetDefaultAwaitTimeout` 覆盖。

M13 从最终有效调用 Context 读取剩余时长写入 Request 头，不新增超时配置。

### 13.2 Async 的剩余时长

Async 在实际完成任务进入 Await 前已经执行发送。为避免复制默认超时规则，`service`
提供一个只供框架使用的轻量查询，按与 Await 相同的冻结配置计算：

- 显式 Deadline：`deadline - now`；
- 无 Deadline：当前 Service 已冻结的 `DefaultAwaitTimeout`。

该查询只计算 Duration，不创建 Go Timer、M8 条目、goroutine 或 Context。真正的调用方
计时仍只在异步完成任务的 `Service.Await` 中建立。

### 13.3 目标端 Deadline

调用方和目标端位于不同进程，不能共享同一个 Timer。目标端必须有自己的执行取消边界，
否则调用方超时后目标 Redis/数据库操作可能无限继续。

M13 为每个 Node RPC Runtime 建立一条共享 M8 DeadlineQueue：

- 每个入站 Request 按 `RemainingTimeoutMillis` 还原相对剩余时间并登记一条 Deadline；
- 目标 Context 使用 `context.WithCancelCause`，不为每个请求创建 Go Runtime Timer；
- Deadline watcher 到期后取消对应目标 Context；
- 请求完成或队列拒绝时取消 Deadline 并清理绑定；
- 该 Timer 是另一个 Node 上的目标执行保护，不是调用方的第二个 Timer；
- M8 的 `10ms` 精度只影响目标侧尽快停止，调用方自身显式 Deadline 精度不变。

### 13.4 取消传播边界

M13 不实现 Cancel 包：

- 调用方手工取消或超时时，立即从本地 pending 表移除；
- 目标端不知道手工取消，只会执行到原始剩余 Deadline 或正常完成；
- 物理连接断开时，已经进入目标 Service FIFO 的任务继续到原 Deadline 或正常完成；
- 迟到 Response 被调用端安全丢弃；
- 不因为取消自动重发或回滚已经发生的业务副作用。

若真实业务证明“保持连接时立即取消远端工作”有必要，再单独设计 Cancel 包、目标活动表
和竞态语义，不能顺手加入 M13。Cancel 包也不能改变“已发生的业务副作用无法自动回滚”
这一事实。

### 13.5 Context Value

Go `context.Value` 只在当前进程有效，M13 不自动序列化它：

- 同 Node RPC 继续保留 M11 的只读 Value 传播；
- TCP 远端只传播 Deadline/取消边界；
- 目标 Context 仍包含目标 Service 自己的执行令牌；
- TraceID、账号信息或其他跨进程元数据需要以后建立显式元数据协议。

## 14. 连接心跳与超时

### 14.1 心跳

Ping/Pong 只用于传输层健康检查：检测空闲连接、半开连接和远端进程失联。它不表示目标
Service 已经 Ready，不代替业务健康检查，也不决定 Discovery TTL、Retired 或 Lost 状态。
该边界与 gRPC 官方 Keepalive 文档区分“连接 Keepalive”和“Service Health Checking”的
方式一致：<https://grpc.io/docs/guides/keepalive/>。

当 `read_idle_timeout > 0`：

- 主动拨号方每 `read_idle_timeout / 3` 发送一次 Ping；
- 被连接方收到后立即通过同一连接回送 Pong；
- 正常 Response 和 Pong 都会刷新主动方读空闲时间；
- Request、Notify 和 Ping 都会刷新被连接方读空闲时间；
- 心跳不进入 Service、不产生 pending、不记录普通成功日志；
- Ping/Pong 无法进入发送队列时关闭连接，让过载快速显现。

当 `read_idle_timeout = 0s` 时不启动应用层心跳，M5 ReadTimeout 也关闭；系统 TCP KeepAlive
仍按 M5 默认工作。

### 14.2 关闭原因

- 读写超时、EOF、协议错误、心跳失败和 socket 错误关闭当前物理连接；
- 出站会话的全部 pending 立即以 Transport 不可用完成；
- 逻辑目标仍有效时进入重连；
- 入站连接关闭不取消已经被 Service 接受的任务；任务结束时发现会话已关闭则释放响应；
- 同一个关闭原因最多记录一条连接生命周期日志，避免故障日志风暴。

## 15. 过载与错误处理

### 15.1 新 Request

以下任一情况立即失败且不进入等待：

- 没有远端目标；
- 会话未 Ready；
- 远端目录没有 Service；
- 契约不一致；
- pending 达到 `65536`；
- M5 发送帧数达到上限；
- payload 或完整包超过上限；
- Context 已取消或已经到期。

### 15.2 新 Notify

Notify 使用相同目标、契约、包长和发送队列校验，但不占 pending。发送队列满时返回
`CodeTransportOverloaded`，不阻塞、不关闭健康连接，也不静默丢弃。

### 15.3 入站 Service 过载

Request 无法进入目标 Service FIFO 时，目标端返回对应稳定错误码，例如：

- `CodeServiceQueueFull`；
- `CodeServiceNotReady`；
- `CodeServiceStopping`；
- `CodeServiceStopped`。

`Retired` 只是一项服务发现可观察状态。框架在 TCP 与 NATS 下都继续正常准入 Request、
Notify 和 Broadcast；`CodeServiceRetired` 只保留给业务主动返回。

Notify 无响应，只在目标侧限频记录。

### 15.4 Response 过载

Response 已经对应对端等待中的 Request，不能静默丢失。若 Response 无法进入 M5 发送队列：

1. 释放仍属于目标端的 Response Buffer；
2. 关闭该物理连接；
3. 让调用端通过连接断开立即完成全部 pending；
4. 不在 Service Runner 中阻塞等待队列空间。

Pong 过载采用相同关闭策略。

### 15.5 错误码映射

M13 不新增重型错误结构，优先复用：

- `CodeRPCNoRoute`；
- `CodeRPCContractMismatch`；
- `CodeRPCMethodNotFound`；
- `CodeRPCEncodeFailed`；
- `CodeRPCRequestDecodeFailed`；
- `CodeRPCResponseDecodeFailed`；
- `CodeRPCExecutionPanic`；
- `CodeTransportUnavailable`；
- `CodeTransportClosed`；
- `CodeTransportOverloaded`；
- `CodeTransportProtocol`；
- `CodeTransportMessageTooLarge`；
- 既有 Service 和通用 Context 错误码。

动态错误文本、底层网络错误和 Stack 只留在本地结构化日志，不跨网络传输。

## 16. Buffer 所有权与低拷贝

### 16.1 当前问题

M11 生成编码器取得的 Buffer 只包含业务 payload。若 M13 重新申请
`RPCHeader + Payload` Buffer 再复制 payload，会给每个远端调用增加一次完整消息复制和
一次额外大 Buffer 生命周期，对游戏服务器的小消息频率和大消息 GC 都不利。

### 16.2 headroom 方案

M13 扩展 Buffer 视图：

```text
底层完整容量：
[      headroom      ][ business payload ][ spare capacity ]

生成编码阶段 Bytes()：
                       [ business payload ]

Prepend 后 Bytes()：
[ rpc header         ][ business payload ]
```

规则如下：

1. `Client.AllocateRequest` 已经知道 Target ServiceName；
2. 精确远端 Target 取得带准确 RPC 头 headroom 的 Buffer；
3. 生成代码仍只看到业务 payload，不修改公开生成 API；
4. Runtime 分配 RequestID 后在 headroom 原地写头；
5. M5 Conn 直接发送完整有效视图；
6. 接收端解析后执行 `DiscardPrefix`，Dispatcher 只借用业务 payload 视图；
7. Release 仍归还原始完整底层容量；
8. 本地精确 Target 不预留网络头，避免小本地 RPC 因最多 `281B` 的 Request headroom
   升高档位；
9. M13 的自动远端选择尚未实现，不提前解决后续多目标 Broadcast 共享问题。

### 16.3 所有权

- 编码成功到调用 API 前，请求 Buffer 属于生成客户端；
- M5 `Conn.Send` 返回 nil 后，请求 Buffer 属于 Conn Writer；
- Send 返回 error 时仍属于 Runtime/调用方错误路径；
- 入站 ReadLoop 把完整 Buffer 转给 RPC Adapter；
- Request/Notify 准入成功后转给目标 Service Task；
- Response 命中 pending 后转给调用完成状态；
- 解码结果必须独立持有，不能引用随后释放的 Buffer；
- 任一时刻不能让同一个 Buffer 同时属于多个连接、pending 或 Service Task。

## 17. Runtime 与 Node 生命周期

### 17.1 启动

Node 启动顺序调整为：

1. 全部 Service `OnInit` 成功；
2. Node TimerEngine 启动；
3. RPC Runtime 创建目标端 DeadlineQueue；
4. TCP Listener 同步绑定成功；
5. 出站目标管理器异步拨号，不等待全部远端 Ready；
6. 按既有顺序 Prepare/OnStart/Activate 每个 Service；
7. Node 发布 Ready。

Listener 绑定失败属于 Node 启动失败，Application 按既有规则回滚已经启动的 Node。远端
暂时不可用不阻止当前 Node 启动；业务调用在会话 Ready 前得到明确 Transport 不可用。

### 17.2 M13 阶段停止边界

M13 继续服从当前 M7～M11 停止骨架：

1. RPC Runtime 停止新调用准入；
2. 关闭 Listener、出站管理器和全部连接；
3. 完成全部 pending；
4. 让已有 Await/Async 得到终态并参与当前 Service 排空；
5. 关闭 RPC DeadlineQueue；
6. 继续既有 Service Scheduler 和 TimerEngine 回收。

M15 先把 TCP/NATS Runtime 调整为“停止入站、最终关闭”两个内部阶段；M16 再按已经确认
的最终语义编排顺序，使 `OnStop(ctx)` 能在独占收尾阶段执行 Await RPC。M13 不把当前
临时顺序写成最终兼容承诺。

## 18. 服务发现与事件边界

M13 的目标 Add/Remove 是内部 Transport 输入，不等同于业务服务发现事件：

- TCP Ready 只表示一条物理连接和握手目录可用；
- TCP 断开可能是毫秒级抖动，不直接等于 Service Lost；
- Discovery Provider 以后依据 TTL、会话和退休状态发布 Added、Retired、Recovered、Lost；
- Discovery 目标仍有效但 TCP 断开时，连接管理器重连；
- Discovery Remove/Lost 到达时，连接管理器停止重连并清理目标；
- Service 不直接监听原始 TCP Open/Close；
- v2 类似的“发现”和“失去发现”监听机制仍在后续 Discovery 里程碑实现，不会遗漏。

## 19. 性能与低延迟约束

### 19.1 热路径禁止项

TCP RPC 正常路径禁止：

- 反射查找方法；
- 拼接 `"Node.Service.Method"` 临时字符串；
- Protobuf 包装 RPC 元数据；
- 为每个调用创建辅助 goroutine；
- 为默认调用超时创建 Go Runtime Timer；
- 为拼接协议头复制完整 payload；
- 在网络 goroutine 执行业务方法或 callback；
- 在 Service Runner 阻塞 socket I/O 或等待发送队列空间；
- 在 pending 锁内执行完成回调、日志 Stack 或 Service 调度；
- 自动重发非幂等调用；
- 无界 pending、无界发送队列或无界重试生命周期。

### 19.2 冷路径允许项

以下只发生在配置、启动或握手冷路径，可以优先保证清晰：

- 配置字符串解析和地址解析；
- 契约目录构建、排序和握手编码；
- ServiceName Map；
- 连接状态日志；
- 退避抖动计算；
- 目录兼容性检查。

### 19.3 Benchmark 门禁

M13 至少保存：

- 固定头编码/解析 `ns/op`、`B/op`、`allocs/op`；
- headroom 原地封包与完整 payload 复制的对照；
- pending 登记、响应、超时和断线批量完成；
- pendingCall 值类型 Map 的分配基线与对象池必要性结论；
- loopback 32B、1KB、64KB 和接近 `4M` payload；
- 同 Node RPC 回归基线；
- 单连接普通负载与突发过载的 P50/P95/P99；
- 多 Service 并发调用同一远端 Node 的锁竞争；
- 连接断开集中完成 pending 的尾延迟和调度尖峰；
- Windows 与 Linux 结果。

不预设一个脱离测试机器和业务模型的绝对延迟承诺。若新优化增加明显状态复杂度，必须先
展示可重复数据再决定。

## 20. 测试与验收

### 20.1 线协议单元测试

覆盖：

- Hello/HelloAck、Request、Notify、Response、Ping、Pong；
- Big Endian 黄金字节；
- 零业务 payload；
- 最大长度和超限；
- 空 NodeID、空 ServiceName、超长名称；
- 零 RequestID、零 MethodID 和零剩余超时；
- 未知 Kind、错误方向、错误阶段和错误 WireVersion；
- 错误 Response 携带 payload；
- 截断包、伪造长度和整数边界；
- Parser Fuzz 不 panic、不越界。

### 20.2 连接与状态测试

覆盖：

- 单向关注只建立一条有向连接；
- 双向关注建立两条相互独立的连接；
- 握手身份不符、目标 NodeID/TargetSessionID 不符和错误 WireVersion；
- 相同 SourceNodeID 的后来连接被拒绝，旧连接不受影响；
- 旧连接移除后新连接可以建立；
- 首次连接、断开、退避、恢复、显式地址迁移和目标 Remove；
- 同 NodeID 不同地址的后来 Add 被拒绝；
- 旧地址迟到 Remove 不删除新地址；
- Ready 前调用快速失败；
- Remove 后不再重连；
- 旧连接 pending 立即失败且不自动重发；
- 迟到 Response 不误命中新会话；
- 心跳成功、Pong 丢失、ReadTimeout 和 WriteTimeout；
- 发送队列、pending 和连接数过载；
- 重复 Close/Stop、部分启动失败和 goroutine 全部退出。

### 20.3 RPC 端到端测试

覆盖：

- Await、Async、Notify 和精确 Node Target；
- 远端业务成功、业务 error、panic、解码错误和编码错误；
- 多输入、多输出、零输入、零业务输出；
- 基础类型、普通 Go 结构体、顶层 Protobuf、嵌套 Protobuf 和 M12 自定义 Codec；
- 相同 ServiceName 的完整契约指纹不一致；
- 私有 Node、私有 Service 和无 Dispatcher Service；
- Service 队列满、未就绪、退休、停止；
- 默认 `15s`、Service 覆盖和显式 Deadline；
- 调用取消和目标 Deadline；
- 调用方断线后已准入的 Request/Notify 继续执行，完成响应安全释放；
- 本 Service 的 Await、Async、Notify 调用以及递归调用到期；
- BufferPool 统计最终回到零；
- 单进程双 Node 仍使用 loopback TCP；
- 两个独立测试进程完成真实 TCP RPC；
- 接近 `4M` 边界和并发小包峰值。

### 20.4 质量命令

实施完成时至少执行：

```text
gofmt
go vet ./...
go test ./...
go test -race ./...
go test -coverprofile=coverage.out ./...
go tool cover -func=coverage.out
go test ./... -run=^$ -bench=. -benchmem
go build ./...
```

Linux 主机执行完整测试、竞态、集成测试和 Benchmark；Windows 执行相同的可运行门禁。
此外交叉构建 `linux/amd64`、`windows/amd64` 和 `darwin/arm64`，并检查所有 RPC、网络、
headroom、pending 和目标状态函数的可达路径。

## 21. 实施顺序建议

设计确认后，M13 实施计划应按以下顺序拆分：

1. BufferPool headroom、前置和丢弃前缀能力；
2. RPC 配置模型与严格校验；
3. 线协议常量、编解码、黄金测试和 Fuzz；
4. RequestID、远端 pendingCall 和会话状态；
5. TCP Listener、握手和契约目录；
6. 出站目标生命周期、重连和心跳；
7. Runtime 本地/远端提交收敛；
8. 目标端 DeadlineQueue 和已接收任务的断线收尾；
9. Node/Application 生命周期接入；
10. 单进程与双进程端到端测试；
11. 过载、断线、取消、资源泄漏和 Race；
12. Benchmark、对象池最终决策、Linux 验收和文档回写。

这只是设计中的实施顺序建议。开发者确认 M13 设计前，不创建正式实施计划，也不编写代码。

## 22. 与既有文档的同步

M13 确认时已经同步以下细节：

1. M5 文档中“RPC Adapter 同步解码后 Release”调整为“同步解析固定头后，把 Buffer
   唯一所有权转移给 Service Task 或 pendingCall”，避免网络 goroutine 执行业务解码；
2. 完整配置示例的 TCP RPC `read_idle_timeout` 从 `0s` 调整为建议默认 `15s`；
3. M5 的 RPC 线协议帧上限使用“业务 payload 上限 + 512B 包络余量”；
4. M11 的 `localCall` 不与远端状态机长期复制：共享一次性完成语义，但同 Node 保持未池化；
   远端 pendingCall 以值存入会话 Map，跨平台零分配基准证明不需要对象池；
5. 内存复用文档补充 headroom 原地前置和接收端丢弃前缀规则；
6. 服务发现文档继续保持无正式 static Provider，后续 Provider 驱动同一目标生命周期；
7. 完整配置只公开 `send_queue_messages`；2026-07-29 最终确认进一步删除 RPC Adapter
   和 M5 发送队列的字节额度，改为只限制消息数量；开发期
   `send_queue_frames` 由 M15 一次迁移；
8. 所有“新连接替换旧连接”和 Revision 描述改为“先建立者保留，地址显式迁移”；
9. 所有“断线取消被调用方任务”描述改为“已准入任务继续执行并安全丢弃响应”。

## 23. 开工 Review 确认结果

开发者已于 2026-07-28 确认全部建议，并要求按本轮字段必要性审计进一步简化。最终结论：

| 编号 | 确认项 | 最终结论 |
|---|---|---|
| 1 | 每对 Node 的连接模型 | 使用有向连接；单向关注一条，双向关注两条 |
| 2 | TCP Adapter 位置 | 放在 `rpc` 包私有实现文件中，不新增只转发的公共包 |
| 3 | 正式 static 配置 | 不增加；M13 集成夹具驱动内部目标生命周期 |
| 4 | 握手契约处理 | 返回公开 Service 的 `ServiceName + ContractFingerprint`；单个不匹配不关闭整条连接 |
| 5 | 线协议代次 | M15 最终收敛为 Hello 首字节 `WireVersion=1`；删除 ASCII Magic，不增加 Nonce 或 Reserved |
| 6 | NodeID 在线协议中的位置 | 只放握手，不进入每个业务包 |
| 7 | 每次调用的契约字段 | 携带 ServiceName、MethodID；ContractID 不进入 TCP 线协议 |
| 8 | Context Value | 不跨进程序列化，只传播剩余 Deadline |
| 9 | 目标端超时 | 使用 Node 共享 M8 DeadlineQueue，不为每个入站 Request 创建 Go Timer |
| 10 | Cancel 包 | M13 不实现；本地立即取消，目标执行到原 Deadline；断线不取消已准入任务 |
| 11 | pending 上限 | 每条出站会话固定最多 `65536`，不预分配、不增加配置 |
| 12 | pendingCall 池化 | 以值存入会话 Map；跨平台基准均为 `0 B/op`、`0 allocs/op`，不增加对象池 |
| 13 | RPC 包长 | `max_payload_size` 是业务 payload；M5 完整帧上限额外加固定 `512B` |
| 14 | 低拷贝封包 | BufferPool 增加 headroom/Prepend/DiscardPrefix，不复制完整 payload |
| 15 | 发送队列 | 最终公开 `send_queue_messages=16384`；M15 同步迁移旧名并删除历史字节额度 |
| 16 | ReadIdleTimeout 与心跳 | 最终公开 `read_idle_timeout=15s`；Ping/Pong 只做传输健康检查；`0s` 显式关闭 |
| 17 | 重连 | `200ms` 指数退避到 `5s`，正负 `20%` 抖动，目标生命周期结束即停止 |
| 18 | 重复 NodeID | 先建立连接保留，后来连接拒绝；不使用 Revision，不自动替换地址 |
| 19 | 断线重发 | 一律不自动重发 Request/Notify |
| 20 | 已接收任务 | 调用方断线后继续执行；响应无法投递时释放，不制造日志风暴 |
| 21 | 压缩 | M13 不支持、不预留字段；以后由数据驱动并使用新的 WireVersion |
| 22 | TCP 连接事件 | 只做内部诊断，不冒充 Discovery Added/Lost 业务事件 |
| 23 | M13 停止边界 | 完成当前 Runtime 关闭和 pending 清理；最终 `OnStop Await` 顺序留 M16 |

## 24. 实施与验收结果

M13 已于 2026-07-28 完成实现和验收：

1. 实现开发期 `ORP1` Hello、HelloAck、Request、Notify、Response、Ping 和 Pong 最小
   协议；M15 按第 10 节最终布局迁移，不保留双协议兼容分支；
2. 实现每个 Node 独立 Listener、显式目标生命周期、契约指纹目录、重复 NodeID 拒绝、
   RequestID、pending、重连、心跳和断线完成；
3. Request 和 Notify 的网络 goroutine 只解析固定头和执行准入，业务解码及方法调用始终
   回到目标 Service FIFO；
4. 目标 Request 使用 Node 共享 M8 DeadlineQueue，调用方显式 Context 和默认
   `15s` Await 均保持一次调用只有一个计时来源；
5. 调用方断线不取消已经准入的目标任务，响应无法发送时按唯一 Buffer 所有权安全释放；
6. 同一 Application 中的多个 Node 也通过真实 TCP 通信，没有进程内指针短路；
7. BufferPool 已支持 headroom、原地 `Prepend` 和 `DiscardPrefix`，协议封装不再复制完整
   业务 payload；
8. 真实 TCP 集成测试覆盖 Await、Async、Notify、自定义 Codec、普通 Go 结构体、顶层
   Protobuf、结构体嵌套 Protobuf、业务错误、panic、重连、重复 NodeID、私有 Service、
   默认/显式 Deadline、调用方断线后的任务收尾和独立子进程调用；
9. Windows 与 Linux 均通过全量测试和 Race；Windows 完成协议 Fuzz，共执行
   `446230` 次输入且无失败；`linux/amd64`、`windows/amd64`、`darwin/arm64`
   交叉构建通过；
10. Linux 真实 loopback TCP 的 1000 次 Await 基线约为：平均 `21.399µs`、
    P50 `20.527µs`、P95 `39.513µs`、P99 `52.797µs`。该数据只作为当前机器和提交的
    回归基线，不作为跨硬件性能承诺；
11. Linux 32B、1KB、64KB、接近 4M payload 的端到端基线分别约为 `65.347µs`、
    `78.377µs`、`276.217µs`、`7.670ms`；
12. `pendingCall` 值类型 Map 热路径在 Windows/Linux 均为零分配，最终不增加对象池。

M13 已关闭。服务发现 Provider、NATS RPC、完整 Stop/OnStop Await 和自动路由已分别在
M17/M18、M15、M16、M19 完成；跨 Node Broadcast 进入 M20。
