# Application 级 RPC 与 Origin Discovery 自举设计

> 状态：待审阅
> 基线：v3.0（尚未对外发布）
> 兼容性：不兼容现有未发布配置；不提供旧字段兼容层

## 目标

同时解决以下两个配置与架构问题：

1. Origin 内置服务发现不再要求额外配置 `server.listen` 和 `server.address`，只通过
   `server.node` 指定承载 `DiscoveryService` 的 Node。
2. NATS 的 `namespace`、`urls`、认证和 TLS 等应用级连接参数只配置一次，不在每个 Node
   下重复。
3. TCP 和 NATS 使用同一套清楚的所有权规则：Application 决定 RPC Transport 和公共参数，
   Node 只保存自身独有的 TCP 地址；运行时资源仍由各 Node 独立拥有。
4. Discovery 自举不依赖尚未建立的服务发现目录，不形成“先发现目标才能连接发现服务”的
   启动环。
5. 保留现有 Origin Provider 的 TTL、会话、全量快照、恢复和退休语义，只替换控制消息的
   承载方式。

## 最终配置外观

### NATS

```yaml
rpc:
  transport: nats
  max_payload_size: 4M
  max_broadcast_size: 64M
  nats:
    namespace: origin-tutorial
    urls:
      - nats://127.0.0.1:4222

discovery:
  type: origin
  origin:
    ttl: 5s
    server:
      node: discovery-1

nodes:
  - id: discovery-1
    services:
      - DiscoveryService

  - id: player-1
    services:
      - PlayerService

  - id: gateway-1
    services:
      - GatewayService
```

NATS 配置属于 Application 的 RPC 域。每个 Node 仍创建并关闭自己的 NATS Connection、
Subscription、pending 表和恢复 goroutine；顶层配置只作为构造每个 Node 冻结配置的只读
模板，不让 Application 共享一个可变连接。

### TCP

```yaml
rpc:
  transport: tcp
  max_payload_size: 4M
  max_broadcast_size: 64M
  tcp:
    send_queue_messages: 16384
    read_idle_timeout: 15s
    write_timeout: 15s

discovery:
  type: origin
  origin:
    ttl: 5s
    server:
      node: discovery-1

nodes:
  - id: discovery-1
    rpc:
      tcp:
        listen: 0.0.0.0:7000
        advertise: 10.0.1.10:7000
    services:
      - DiscoveryService

  - id: player-1
    rpc:
      tcp:
        listen: 0.0.0.0:7001
        advertise: 10.0.1.11:7001
    services:
      - PlayerService
```

TCP 的队列、超时和业务消息上限位于顶层，`listen`、`advertise` 位于 Node，因为这两个
地址必须随 Node 不同。Discovery 客户端从完整配置中找到 `server.node`，使用该 Node 的
`advertise` 自举；Discovery 服务端复用该 Node 的 `listen`，不再占用第二个端口。

## 配置规则

### Application RPC

- 顶层 `rpc` 省略时，Application 中的 Node 只支持本地 RPC。
- 顶层 `rpc` 省略时出现任何 `nodes[].rpc` 都是配置错误，不能恢复旧的 Node 级 Transport
  选择语义。
- 顶层 `rpc.transport` 显式选择 `tcp` 或 `nats`，不提供隐式默认 Transport。
- 一个 Application 只允许一种 RPC Transport，不再支持同一配置中的 TCP/NATS 混用。
  不同 Transport 的 Node 本来也不能直接互调，配置期拒绝比运行期无路由更明确。
- `max_payload_size` 和 `max_broadcast_size` 是 Application RPC 域的公共限制。
- TCP 公共队列和超时只允许出现在顶层 `rpc.tcp`；每个 Node 的 `rpc.tcp` 只允许
  `listen` 和 `advertise`。
- NATS 的 URL、namespace、接收队列、认证和 TLS 只允许出现在顶层 `rpc.nats`；
  NATS 模式下 `nodes[].rpc` 是无效配置。
- 配置加载阶段为每个 Node 创建深拷贝后的 `rpc.Config`。URL 切片以及认证、TLS 值不能
  被其他 Node 或后续配置修改污染。
- 顶层 `rpc` 存在时，全部 Node 启用该 RPC Transport。纯本地 Application 通过省略顶层
  `rpc` 表达，不增加 `enabled`、`disabled` 等第二套开关。

### Origin Discovery

- `discovery.origin.server` 只包含 `node`；删除 `listen` 和 `address`。
- `server.node` 必须存在于完整 `nodes` 列表，并且必须包含唯一、公开原名声明的
  `DiscoveryService`。
- 选择 Origin Provider 时必须配置顶层 `rpc`；`server.node` 必须拥有解析完成的有效
  RPC 配置。
- TCP 模式下，所有 Node 都必须提供有效的 `listen`，以及其他进程可连接且在配置中唯一的
  `advertise`。同一次命令选中的共进程 Node，其 `listen` 不能冲突；部署到不同主机的 Node
  可以复用相同本地端口。Discovery 使用服务端 Node 的同一 RPC 地址。
- NATS 模式下，所有 Node 使用相同 namespace 和服务器集合，Discovery 使用保留 Subject。
- `DiscoveryService` 仍是框架保留系统 Service，不进入普通业务发现目录，也不能由业务通过
  生成 RPC 客户端调用。
- 同一进程选择多个 Node 时，`server.node` 仍必须位于有效启动顺序第一位，使它先 Ready、
  最后停止；框架不静默重排用户给出的顺序。

## 静态自举与循环依赖

配置加载发生在选择和启动 Node 之前，因此 Application 可以从完整 `nodes` 列表解析出
Discovery 自举目标，即使当前进程通过 `--node` 只启动其中一个业务 Node。

自举路由不写入普通服务发现目录，也不经过 `RemoteResolver`：

- TCP 自举目标由 `server.node` 的 `rpc.tcp.advertise` 产生。
- NATS 自举目标由顶层 namespace 和 `server.node` 产生。
- 自举只允许访问框架保留的 Discovery 系统通道，不能借此绕过目录调用业务 Service。
- Discovery 首次返回权威快照后，普通 RPC 路由仍完全由每个 Node 的私有发现目录决定。

RPC Runtime 在 Provider 启动前已经建立 Listener 或 NATS Connection。Discovery Provider
随后打开系统通道并完成首次同步，因此不存在“先通过 Discovery 找到 DiscoveryService”
的环。

## 装配边界

Application 配置层把顶层 RPC 公共值和 Node TCP Endpoint 归一化为现有的逐 Node
`rpc.Config`。因此 `rpc.Runtime` 仍只接收当前 Node 的完整冻结配置，不读取 Application
配置，也不共享其他 Node 的运行状态；直接使用 `node.New`/`rpc.Runtime` 的底层测试与编程
接口可以继续传入完整逐 Node 配置。

Origin 需要的系统通道通过 Node 装配期的私有 Factory Builder 注入：Node 先创建并配置
`rpc.Runtime`，再把仅含系统 Dial/Accept 能力的窄接口交给 Origin Factory。普通 etcd 和
自定义 Provider 继续只接收现有 `provider.Context`，不向公开 Provider SPI 增加 RPC Runtime、
静态 Node 清单或任意系统调用能力，也不建立 Application 级运行时注册表。

承载 `DiscoveryService` 的绑定在 `rpc.Runtime.Freeze` 前完成系统处理器注册。Freeze 后处理器
不可替换；Listener/Connection 恢复只重建传输资源，不改变已冻结的处理器或自举目标。

## RPC 系统通道

Discovery 不伪装成普通业务 RPC 方法。RPC Runtime 增加一个只供框架装配层使用的保留
系统通道，业务 API、生成器和公开客户端均不能注册或调用该通道。

系统包络至少包含：

- 固定系统通道标识；
- 源 NodeID 和源 SessionID；
- 目标 `server.node`；
- Origin 控制协议 payload；
- 传输代次或等价的旧连接隔离信息。

普通业务 RPC 继续要求由发现目录提供精确目标 SessionID。只有 Discovery 系统通道允许在
尚未知服务端 SessionID 时自举；该例外不能进入业务 Request、Notify 或 Broadcast 路径。

共享底层传输不等于共享消息上限。当前业务 RPC 默认 payload 上限小于 Discovery 的 16 MiB
完整快照上限，因此：

- TCP Listener 和 NATS Connection 的底层绝对上限取业务完整包络与 Discovery 完整包络的
  较大值，避免合法快照被底层提前拒绝。
- 完成系统/业务握手后分别执行各自的较小上限；业务连接不能借共享 Listener 接受超过
  `rpc.max_payload_size` 的业务帧。
- Discovery 继续保留 16 MiB 快照、64 条控制发送队列和现有连接数上限；共享 TCP Listener
  必须在逻辑通道层维持这些独立界限，不能直接继承更大的业务发送队列。
- 共享 TCP Listener 分别统计业务连接和 Discovery 控制连接，并设置两者之和的固定总上限；
  任一平面达到自己的额度时只拒绝该平面的新连接，不能挤占另一平面的保留额度。
- NATS 启动时校验 Broker `max_payload` 能覆盖两类完整包络的较大值，错误信息报告所需值，
  但不输出认证信息。

### TCP 承载

- `server.node` 的 RPC Listener 同时接受业务 RPC 握手和保留 Discovery 握手。
- Discovery 使用同一 Listener 地址，但保持独立的轻量控制连接；它不与业务 pending、
  方法分发或连接路由表混用。
- Listener 在首个有界握手帧中区分协议类型，未知类型、错误目标 NodeID、超大包或非法
  Session 立即关闭。
- Listener 启动但 Discovery Actor 尚未 Prepare 的短暂窗口内，系统握手快速返回不可用并
  关闭；客户端按现有退避重试，不能把回调阻塞在 Node 启动路径上。
- 现有 Origin Hello、心跳、发布、撤销、全量快照和 ServerEpoch 语义保持不变。
- RPC Listener 恢复后继续安装同一个冻结系统处理器；客户端沿用有界退避重新自举。

复用 Listener 而保留独立控制连接，可以删除额外地址和端口，同时避免把长生命周期的
发现推送混入业务 Request/Response pending 表。

### NATS 承载

- 每个 Node 复用自身 RPC Runtime 已建立的 NATS Connection，不创建第二个 Connection。
- Discovery 服务端使用
  `orpc.<namespace>.sys.discovery.server.<server-node>`，客户端使用
  `orpc.<namespace>.sys.discovery.client.<node>.<session>`；它们不与
  `orpc.<namespace>.req|resp.<node>` 业务 Subject 重叠。
- 客户端先建立自己的控制 Subscription，再发送 Hello，防止首次快照早于接收路径。
- 所有控制消息携带 NodeID、SessionID 和 ServerEpoch；旧进程或旧订阅的迟到消息不能覆盖
  新会话。
- 服务端继续通过单一 Actor 串行处理注册、心跳、撤销和快照发布，避免 NATS 回调并发改变
  目录顺序。
- 当前 NATS RPC Connection 使用 `NoEcho`。`server.node` 自身的 Provider 通过进程内系统
  通道连接共置 DiscoveryService，不依赖同一 Connection 收到自己的发布。
- NATS Connection 重建时，业务与 Discovery Subscription 作为同一代资源重新建立；
  Provider 报告 Recovering，并在新代完成 Hello 和全量同步后恢复 Ready。
- 服务端 Subscription 已建立但 Discovery Actor 尚未 Prepare 时丢弃或明确拒绝系统消息；
  客户端在收到 Ready 前保持重试，不能误把空快照报告为首次权威同步。

## 生命周期顺序

### 启动

每个 Node 的关键顺序固定为：

1. 完成所有 Service 的纯 `OnInit`。
2. 启动 TimerEngine。
3. 启动 Node RPC Listener 或 NATS Connection，并安装冻结的系统通道处理器。
4. 如果当前 Node 承载 `DiscoveryService`，Prepare 其控制 Actor；不执行用户回调。
5. 启动当前 Node 的 Origin Provider，通过静态自举完成首次权威同步。
6. 按声明顺序执行 Service `OnStart`。
7. 激活业务入站并发布当前 Node 的完整服务记录。

Discovery Server 与业务 Node 共置时，步骤 4 早于该 Node 自身 Provider 的步骤 5，因此本地
自举不会死锁。远端进程在服务端尚未启动时沿用当前有界重试，并受启动 Context 控制。

### 停止

停止顺序固定为：

1. 停止新的发现发布并撤销当前 Node Session。
2. 停止业务 RPC 入站；Discovery 系统通道暂时保持可用。
3. 反序停止已启动的业务 Service。
4. 关闭当前 Node 的 Discovery Provider。
5. 如果当前 Node 承载 `DiscoveryService`，关闭其控制 Actor 和全部控制会话。
6. 关闭 RPC Transport、TimerEngine 和发现订阅。

同进程的 `server.node` 最后停止，确保其他 Node 能先撤销。`BeginStop` 必须只关闭业务面，
不能提前切断系统通道。

## 错误和安全边界

- 所有结构错误在创建 Listener、Connection 或 goroutine 前以 `ErrInvalidConfig` 返回。
- Origin 未配置顶层 RPC、Server Node 缺失、DiscoveryService 位置错误、TCP Endpoint 缺失、
  旧 `server.listen/address` 或 NATS Node 级配置均返回包含完整字段路径的错误。
- TCP 与 NATS 系统消息沿用现有发现快照大小上限和队列上限；不接受无限 pending。
- 非法系统通道消息不进入 Service Scheduler，不携带认证密钥、URL 凭据或动态错误文本到日志。
- NATS URL、Token、密码、Credentials 和 NKey 继续使用现有脱敏规则。
- 系统通道不能路由任意 ServiceName/MethodID，防止静态自举成为绕过发现状态、Retired 过滤
  或公开性校验的后门。

## 测试设计

测试采用分层方式，任何一层失败都不以更高层测试掩盖。

### 配置单元测试

1. NATS 顶层配置为每个 Node 生成值相同、容器互不共享的冻结 `rpc.Config`。
2. TCP 顶层公共参数与各 Node Endpoint 正确合并，默认值只在配置加载阶段应用一次。
3. 拒绝缺少/未知 Transport、错误联合块、TCP 缺少 Endpoint、重复监听地址、通配
   advertise、NATS 下出现 `nodes[].rpc`、Origin 下缺少顶层 RPC。
4. 拒绝旧 `discovery.origin.server.listen/address`，确保未发布旧模型不会被静默接受。
5. 拒绝不存在的 `server.node`、多个或错误位置的 DiscoveryService、错误启动顺序。
6. 现有日志、Scheduler、过滤规则和服务声明的严格解码不回退。

### RPC 系统通道单元测试

1. TCP 首帧只接受业务或 Discovery 两种已知握手，畸形、截断、超大和错误 NodeID 均关闭。
2. Discovery 自举 Session 例外只作用于系统通道；普通 Call/Notify/Broadcast 仍要求精确
   Session 和正常发现路由。
3. 系统处理器在 Freeze 后不可替换，Listener 恢复后仍使用原处理器。
4. NATS 保留 Subject 与业务 req/resp Subject 不冲突，非法 NodeID/namespace 被拒绝。
5. NATS 连接代次变化会重建全部系统 Subscription，旧代消息不能改变当前状态。
6. 队列满、发布失败、Context 取消和关闭竞态均释放 Buffer、pending 与 goroutine。
7. 底层共享上限允许 16 MiB 内合法发现快照，同时业务路径仍拒绝超过
   `rpc.max_payload_size` 的 payload；Discovery 的独立 64 条发送队列不会被业务队列放大。

### Origin Provider 契约测试

1. TCP 与 NATS 分别通过 `providertest` 的首次同步、Publish、Withdraw、Close 和幂等套件。
2. 保持 TTL、心跳、ServerEpoch、Session 接管、全量快照和 Retired 更新语义。
3. Server Node 的 NATS Provider 使用本地系统通道成功自举，证明不受 `NoEcho` 影响。
4. 服务端暂时不可用时进入 Recovering；TTL 内保留旧快照，超时后清空；恢复后重新 Hello、
   注册并用权威全量快照收敛。

### Application 集成测试

1. TCP：Discovery、Provider、调用方和目标 Node 只使用各自 RPC 端口完成发现及远程调用，
   不存在额外 Discovery Listener。
2. NATS：所有 Node 从一份顶层配置建立独立 Connection，完成发现、Call、Notify、Broadcast。
3. 仅选择业务 Node 启动时，仍能从完整配置解析远端 `server.node` 自举目标。
4. 同进程多 Node 按 Discovery 优先顺序启动、严格反序停止；撤销发生在系统通道关闭之前。
5. TCP Listener 和 NATS Server/Connection 故障恢复后，Discovery 与业务 RPC 都重新可用。
6. 启动中取消、服务端不可达、部分启动失败和回滚不遗留端口、订阅或 goroutine。
7. Broker `max_payload` 只满足业务 RPC、但不足以承载发现快照包络时，在启动阶段稳定失败；
   满足两类上限后大快照能够完成同步。

### 回归和质量门槛

实现完成后至少执行：

```text
go test ./application ./node ./rpc ./internal/discovery/origin
go test -race ./application ./node ./rpc ./internal/discovery/origin
go test ./...
go vet ./...
```

更新后的教程配置必须纳入仓库现有配置加载回归；TCP/NATS 集成测试使用动态端口和仓库测试
基础设施启动的临时 NATS Server，不依赖开发机常驻服务。若全量 `-race ./...` 在可接受时间
内可运行，再把它作为最终附加门槛执行并记录结果。

## 文档与示例迁移

- 更新完整配置模型、Origin Provider、NATS RPC 和相关里程碑文档中的旧结论。
- 更新服务发现及跨节点 RPC 教程，分别给出 Application 级 TCP/NATS 完整配置并逐项注释。
- 更新所有 `examples/**/config/*.yaml`，删除 Discovery 专用地址和 Node 级 NATS 重复块。
- 明确说明 Application 级配置是只读模板，各 Node 仍拥有独立网络资源和生命周期。
- 搜索并清除旧 `discovery.origin.server.listen/address`、Node 级 `rpc.transport` 和重复 NATS
  配置示例，避免新旧写法同时出现。

## 非目标

- 不实现多个 DiscoveryService、选主、复制、持久目录或跨 namespace 发现。
- 不把 DiscoveryService 暴露为业务 RPC 契约，也不为生成器增加系统方法。
- 不支持同一 Application 中混合 TCP/NATS 或为单个 Node 覆盖 NATS 凭据。
- 不改变 etcd 和自定义 Provider 的后端协议；它们继续使用公开 Provider SPI。
- 不顺带修改 RPC 路由、Retired 默认过滤、负载均衡或业务序列化语义。

## 验收标准

1. 用户只需通过 `discovery.origin.server.node` 指定内置发现端，不配置 Discovery 地址或端口。
2. NATS URL、namespace、认证和 TLS 在 Application 中只出现一次。
3. TCP Discovery 与业务 RPC 复用 `server.node` 的 Listener 地址，运行时不存在第二个
   Discovery Listener。
4. 自举不依赖发现目录，服务端共置、跨进程启动和故障恢复均无启动环。
5. 每个 Node 的 NATS Connection、订阅、pending 和恢复状态保持独立所有权。
6. 系统通道不能用于业务调用，现有 Session、Retired 和公开性边界不回退。
7. 配置、单元、Provider 契约、TCP/NATS 集成、恢复、停止与竞态测试全部通过。
