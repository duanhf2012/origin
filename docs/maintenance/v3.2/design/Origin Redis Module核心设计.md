# Origin Redis Module 核心设计

> 状态：设计结论已确认并完成书面自审，待使用者最终复核；之后继续 MySQL、Kafka 和 Blueprint 设计
> 基线：Origin v3.0，目标版本：v3.2
> 兼容性：项目尚未对外发布，不保留 Origin v2 Redis API、命名或行为兼容层
> Driver 基线：实施前再次复核；截至 2026-08-11 固定为 `github.com/redis/go-redis/v9 v9.22.0`
> 分布式锁基线：`github.com/bsm/redislock v0.10.0`

## 1. 文档定位

本文是 `sysmodule/redismodule` 实现、测试、Example 和使用教程的单一核心设计。后续实施不得从
Origin v2、调研资料或第三方库示例中任意恢复另一套公共外观；需要改变本文已经确认的公共结论时，
必须先更新设计并重新确认。

本模块的目标是：

1. 让一个 Origin Module 明确拥有一个逻辑 Redis 部署和一个官方 Client；
2. 同时支持 Standalone、Sentinel 和 Cluster，并允许同一 Service 组合多个 Redis Module；
3. 提供覆盖游戏服务高频 Redis 操作的普通 Go 返回值外观；
4. 保留 `Client`、Pipeline、事务、Lua 等原生组合能力，不重复包装整个 go-redis；
5. 通过有界连接池、Context、明确重试和完整生命周期保证稳定性；
6. 提供基于 `bsm/redislock`、不泄漏第三方类型的便利分布式锁；
7. 通过配置表、完整中文注释、可编译 Example 和游戏场景教程降低接入成本。

范围控制同样是设计目标。本模块不实现缓存框架、Repository、对象序列化、业务重试、排行榜规则、
队列框架、自动续租、Redlock、多 Redis Client Registry 或服务状态代理。只有形成重复、稳定且跨业务
一致的真实需求后，才评估新增便利层。

## 2. 既有实现复核与依赖结论

### 2.1 Origin v2

v2 `redismodule` 使用 `redigo`，只支持单地址 Standalone、手工连接池和约四十个 String、Hash、List、
Sorted Set 包装。它缺少调用级 Context、Sentinel、Cluster、严格生命周期、完整配置校验和真实集成测试，
部分函数命名还包含历史缩写或拼写问题。

v3.2 的迁移结论如下：

| v2 能力 | v3.2 结论 | 原因 |
| --- | --- | --- |
| `redigo` 与手工 Pool | 删除，使用官方 go-redis v9 | 官方 Client 已管理连接池、拓扑和恢复 |
| 单地址连接 | 重建为显式 Mode 和地址列表 | 必须覆盖 Standalone、Sentinel、Cluster |
| 无 Context 包装 | 删除 | 丢失取消、Deadline 和 Trace Value |
| `SetString` 等历史命名 | 不兼容保留 | 项目未发布，应直接形成规范 API |
| 全量薄转发 | 不采用 | 高频便利层加 `Client()` 足够，避免复制官方 API |
| 隐式业务重试 | 不增加 | 非幂等 Redis 命令不能盲目重试 |

### 2.2 go-redis

采用 Redis 官方维护的 `github.com/redis/go-redis/v9`。截至设计日，最新稳定版为 v9.22.0，支持
Standalone、Sentinel、Cluster、Context、Pipeline、事务、Lua、连接池和 RESP2/RESP3；最低 Go 版本为
1.24，Origin 当前 Go 版本满足要求。

Origin 首批服务端兼容基线为 Redis 7.0+，其中 Redis 7.x 属于 go-redis 声明的非官方兼容范围，因此实施时
必须至少在 Redis 7.2 和当前官方稳定版 Redis 8.10 上运行完整 Standalone 集成测试；Sentinel 和 Cluster
至少分别覆盖团队生产实际使用的 Redis 版本。兼容 Redis 协议的云服务或代理必须按产品、版本、拓扑、
RESP、TLS 和 Failover 行为单独验证，不能只因“协议兼容”就在文档中承诺完全兼容。

v9.22.0 新增的客户端缓存和自动 Pipeline 仍标记为实验能力，而且会增加隐藏缓存、批处理队列和后台
goroutine。首批不启用、不包装，也不把它们作为性能基线；只有明确压测收益、失效一致性、顺序语义和
关闭流程后，才能回到设计阶段评估。v9.22.0 同时调整了读写超时、重试退避和 Cluster 拓扑刷新默认值，
所以 Origin 对本文列出的生产关键默认值显式赋值，避免后续 Driver 小版本升级静默改变行为。

不在模块外观中返回 `StringCmd`、`IntCmd` 等命令对象。复杂或低频能力仍通过
`redis.UniversalClient` 使用，避免 Origin 跟随每个 Redis 命令版本重复扩张。

### 2.3 分布式锁

首批采用 `bsm/redislock`，原因如下：

- 直接复用 go-redis Client 和连接池，不引入第二套 Redis Client；
- 依赖规模小，API 聚焦 Redis Lease Lock；
- 使用 Lua 保证获取、刷新和释放的所有权检查原子性；
- 相比 Redsync，不要求部署三个或五个真正独立的 Redis Master；
- 相比 rueidislock，不引入 rueidis、第二个连接池和双生命周期。

`bsm/redislock` 仍处于 v1 之前，因此必须由 Origin 自有薄外观隔离。Redis 锁定位为带 TTL 的 Lease，
不是绝对互斥证明；金币、道具、奖励、订单和支付不能只依赖该锁，必须继续使用 Lua 原子更新、数据库
事务、唯一约束或幂等记录。

## 3. 总体架构

### 3.1 一个 Module 对应一个逻辑 Redis 部署

一个 `redismodule.Module` 只拥有一个 Config、一个运行时 Client 和一个锁 Client。多个 Redis 部署通过
多个命名 Module 组合，不在一个 Module 内建立字符串索引的 Client Map。

```go
type GameRedisModule struct {
    redismodule.Module
}

type GameStorageModule struct {
    service.Module

    cache   *redismodule.Module
    session *redismodule.Module
}
```

这样配置、启动、Ping、停止和错误边界都与资源所有者一一对应，也不会产生进程级可变 Client Registry。

### 3.2 三种拓扑显式选择

```go
type Mode string

const (
    ModeStandalone Mode = "standalone"
    ModeSentinel   Mode = "sentinel"
    ModeCluster    Mode = "cluster"
)
```

Mode 不根据地址数量推断。Mode 省略时使用 Standalone，但多个地址不会因此被推断成 Cluster，而是按
Standalone 规则返回配置错误。

- Standalone：必须且只能有一个地址；允许非零 Database；
- Sentinel：至少一个 Sentinel 地址，必须有 MasterName；允许非零 Database；
- Cluster：至少一个 Seed 地址，Database 必须为 0；默认只读 Primary。

### 3.3 运行时访问

模块启动时按 Mode 选择明确的官方构造路径，创建 Client、安装 Hook、执行连接验证，全部成功后才原子
发布运行时指针。Standalone/Sentinel 必须连到实际数据节点并 Ping；Cluster 必须完成拓扑发现并 Ping 当前
全部 Primary，不能只连通一个 Seed 就报告启动成功。任一节点失败时逆序关闭已经创建的资源，不发布半成品。

停止时先把运行时指针交换为 `nil`，阻止新包装调用，再关闭 Client。普通命令热路径只做一次原子读取，
不增加每次调用互斥锁、Channel 跳转或新的 goroutine。

## 4. 包、Module 与 Option

包路径固定为：

```text
sysmodule/redismodule
```

公共构造外观固定为：

```go
// New 根据配置创建一个由调用方随后注册到 Service 或父 Module 的 Redis Module。
func New(config Config, options ...Option) (*Module, error)

// Setup 配置匿名嵌入到业务 Module 中的 Redis Module。
// 同一个 Module 只能成功调用一次，启动后禁止重新配置。
func (module *Module) Setup(config Config, options ...Option) error
```

`New` 与 `Setup` 共用同一套默认值、校验、Option 合并和运行时构造逻辑。Option 为封闭接口，首批只提供：

```go
// WithTLSConfig 使用调用方提供的高级 TLS 配置。
// Module 会克隆配置；不允许 InsecureSkipVerify，也不能与 YAML CA 来源冲突。
func WithTLSConfig(config *tls.Config) Option

// WithHook 按传入顺序安装 go-redis Hook。
// Hook 在启动 Ping 前生效，调用方必须保证 Hook 自身可并发使用。
func WithHook(hooks ...redis.Hook) Option
```

首批不提供任意修改 `redis.UniversalOptions` 的回调。自定义 Dialer、动态凭证等只有出现具体生产需求后，
才增加对应的窄 Option，不能用一个通用回调形成第二套连接配置源。

## 5. 配置设计

### 5.1 Config 外观

所有 Duration 字段使用 Origin 带单位字符串配置类型，YAML 中只接受 `500ms`、`3s`、`30m` 等值。
配置结构不机械添加 Tag，按 Origin `snake_case` 规则映射。

```go
type Config struct {
    // Mode 指定 Redis 拓扑，支持 standalone、sentinel、cluster；省略时为 standalone。
    Mode Mode

    // Addresses 是 host:port 地址列表。Standalone 必须一个，其他模式至少一个。
    Addresses []string

    // Username 和 Password 是数据节点 ACL 凭证；留空表示不认证。
    Username string
    Password string

    // Database 是逻辑数据库编号；默认 0，Cluster 只能为 0。
    Database int

    // ClientName 是 CLIENT SETNAME 使用的连接名称；留空不设置。
    ClientName string

    // Protocol 是 RESP 协议版本；省略时为 3，只允许 2 或 3。
    Protocol int

    // TLS 控制是否启用 TLS。
    TLS bool

    // TLSCAFile 是追加到系统 Root CA Pool 的可选 PEM CA 文件。
    TLSCAFile string

    // DialTimeout 是单次建连超时；省略时为 5s。
    DialTimeout config.Duration

    // DialAttempts 是一次取连接时最多执行的建连尝试总数（包含第一次）；0 使用 5。
    // 它只重试建立连接，不表示 Redis 命令可以安全重放。
    DialAttempts int

    // DialRetryInterval 是建连失败后再次尝试前的固定等待时间；省略时为 100ms。
    DialRetryInterval config.Duration

    // ReadTimeout 和 WriteTimeout 是网络读写兜底超时；省略时均为 5s。
    ReadTimeout  config.Duration
    WriteTimeout config.Duration

    // PoolTimeout 是连接池达到上限后等待连接的最长时间；省略时为 6s。
    PoolTimeout config.Duration

    // PoolSize 是每个 Redis 节点的基础连接数；0 时 Standalone/Sentinel 使用
    // 10×GOMAXPROCS，Cluster 使用 5×GOMAXPROCS。
    PoolSize int

    // MaxConcurrentDials 是并发创建连接的上限；0 取最终 PoolSize。
    // 它限制故障恢复时的建连风暴，不改变连接池总上限。
    MaxConcurrentDials int

    // MaxActiveConnections 是每个节点的连接硬上限；0 取最终 PoolSize。
    MaxActiveConnections int

    // MinIdleConnections 是每个节点预热的最小空闲连接数；默认 0。
    MinIdleConnections int

    // ConnectionMaxIdleTime 是连接最大空闲时间；省略时为 30m。
    ConnectionMaxIdleTime config.Duration

    // MaxRetries 是每条命令的最大自动重试次数；默认 0，表示禁用命令自动重试。
    MaxRetries int

    // MinRetryBackoff 和 MaxRetryBackoff 仅在开启重试时生效；默认 10ms 和 1s。
    MinRetryBackoff config.Duration
    MaxRetryBackoff config.Duration

    // Sentinel 保存 Sentinel 模式专属配置。
    Sentinel SentinelConfig

    // Cluster 保存 Cluster 模式专属配置。
    Cluster ClusterConfig
}

type SentinelConfig struct {
    // MasterName 是 Sentinel 监控的 Master 名称；Sentinel 模式必填。
    MasterName string

    // Username 和 Password 是 Sentinel 自身凭证，不回退使用数据节点凭证。
    Username string
    Password string
}

type ClusterConfig struct {
    // ReadFromReplicas 允许只读命令访问 Replica；默认关闭，避免无意读取复制延迟数据。
    ReadFromReplicas bool

    // RouteByLatency 在允许 Replica 读取后按延迟选择节点。
    RouteByLatency bool

    // MaxRedirects 是 Cluster 对网络错误和 MOVED/ASK 的最大处理次数；省略时为 3。
    // 它不是单纯的重定向次数，过大同样会放大故障尾延迟。
    MaxRedirects int
}
```

`DefaultConfig()` 返回 Mode、Protocol 和 Duration 等固定默认值；依赖拓扑或 `GOMAXPROCS` 的 PoolSize、
MaxConcurrentDials 和连接硬上限仍以 0 表示“启动时计算”。`Setup/New` 对零值 Config 执行同一套默认化，
避免调用方必须先调用默认函数。Addresses 永远没有伪造默认值，缺失时启动前返回错误。默认化只在
Setup/New 内复制后进行，不回写调用方 Config。

### 5.2 字段、默认值与生产建议起点

生产建议是典型同地域游戏服务的压测起点，不是所有部署的统一最优值。教程必须同时展示默认值、建议
起点、调整依据和作用范围，不得把“建议值”写成无条件最佳实践。

| 配置项 | 必填 | 默认值 | 生产建议起点 | 何时调整与注意事项 |
| --- | --- | --- | --- | --- |
| `mode` | 否 | `standalone` | 显式填写实际拓扑 | 不按地址数推断 |
| `addresses` | 是 | 无 | 填内网域名，不建议证书环境使用 IP | Cluster 地址是 Seed，不是固定完整节点表 |
| `username` | 否 | 空 | 为服务建立最小权限 ACL 用户 | 不在日志输出 |
| `password` | 否 | 空 | 使用环境变量注入 | 不在错误中回显 |
| `database` | 否 | `0` | 保持 0；确有隔离需求再改 | Cluster 必须为 0 |
| `client_name` | 否 | 空 | `服务名-实例ID` | 便于服务端排障 |
| `protocol` | 否 | `3` | 标准 Redis 使用 3 | 代理或兼容服务不支持时改 2 |
| `tls` | 否 | `false` | 云服务或跨不可信网络时开启 | 不提供跳过证书验证 |
| `tls_ca_file` | 否 | 系统 CA | 私有 CA 时配置 | 仅 TLS 开启时有效 |
| `dial_timeout` | 否 | `5s` | 同地域从 `2s`～`5s` 起测 | 过短会放大暂时网络抖动 |
| `dial_attempts` | 否 | `5` | 在线低延迟服务从 1～3 起测 | 表示总尝试次数；最坏建连时间还受 Context 限制 |
| `dial_retry_interval` | 否 | `100ms` | 先保持默认 | 只用于建连失败，不是命令重试退避 |
| `read_timeout` | 否 | `5s` | 在线普通命令可从 `1s` 起测 | 业务 Context 应按请求 SLA 更早截止 |
| `write_timeout` | 否 | `5s` | 在线普通命令可从 `1s` 起测 | 超时不代表写入一定未执行 |
| `pool_timeout` | 否 | `6s` | 在线路径可从 `100ms`～`500ms` 起测 | 通常应早于请求总 Deadline |
| `pool_size` | 否 | Standalone/Sentinel `10×GOMAXPROCS`；Cluster `5×GOMAXPROCS` | 按公式估算，常见起点 32 | Cluster 下按每个节点生效，总连接数随节点数相乘 |
| `max_concurrent_dials` | 否 | 最终 `pool_size` | 先保持默认；大规模同时重连时按压测降低 | 只限制并发建连，不是连接总数 |
| `max_active_connections` | 否 | 最终 `pool_size` | 先等于 PoolSize，有实测突发再增至约 2 倍 | 必须有硬上限 |
| `min_idle_connections` | 否 | `0` | 延迟敏感服务从 4 或 8 起测 | Cluster 节点多时连接数会相乘 |
| `connection_max_idle_time` | 否 | `30m` | 先保持默认 | 应小于服务端或代理空闲超时 |
| `max_retries` | 否 | `0` | 混合读写 Client 保持 0 | 全局重试可能重复非幂等写 |
| `min_retry_backoff` | 否 | `10ms` | 开启重试时先保持默认 | 只在 MaxRetries 大于 0 时生效 |
| `max_retry_backoff` | 否 | `1s` | 开启重试时先保持默认 | 总时间仍受 Context 限制 |
| `sentinel.master_name` | Sentinel 必填 | 无 | 使用运维声明的名称 | 不从地址或数据节点推断 |
| `cluster.read_from_replicas` | 否 | `false` | 强一致读保持关闭 | 开启后可能读到复制延迟数据 |
| `cluster.route_by_latency` | 否 | `false` | 只有允许副本读且跨机房时评估 | 未开启副本读时配置即报错 |
| `cluster.max_redirects` | 否 | `3` | 先保持默认 | 同时处理网络错误和 MOVED/ASK；过高会放大故障尾延迟 |

连接池建议使用以下并发近似作为起点：

```text
连接数 ≈ 峰值 QPS × Redis P99 秒数 ÷ 目标连接利用率
```

例如峰值 20,000 QPS、P99 为 1ms、目标利用率 70%，估算约 29，可从 32 开始压测。该公式只是起点，
必须结合 Pipeline、慢命令、Cluster 节点数、连接建立速度、GC 和 P99 继续调整。

### 5.3 校验与唯一配置源

启动前至少校验：

- Mode 合法，Addresses 数量符合拓扑，地址非空且不重复；
- Protocol 只能为 2 或 3；Database 非负且 Cluster 为 0；
- 所有 Duration 非负，DialAttempts、PoolSize、并发建连、连接上限、重试和 Redirect 非负；
- 默认化后的 `DialAttempts >= 1`；
- `MaxConcurrentDials <= PoolSize`，`MaxActiveConnections >= PoolSize`，
  `MinIdleConnections <= MaxActiveConnections`；
- Sentinel MasterName 必填，非 Sentinel 模式不得携带 Sentinel 专属配置；
- `RouteByLatency` 必须同时开启 `ReadFromReplicas`；
- Cluster 模式禁止配置非零 `MaxRetries`；Cluster 自身已通过 `MaxRedirects` 处理网络错误和拓扑重定向，
  叠加节点级命令重试会放大延迟并增加非幂等写的不确定性；
- TLSCAFile 只能在 TLS 开启时使用；Option TLS 与文件 CA 不得形成冲突；
- 禁止 `InsecureSkipVerify`，错误不得包含密码、Token、完整证书或带凭证地址。

`ContextTimeoutEnabled` 由 Module 固定开启，不成为 YAML 开关。调用 Context 的取消和 Deadline 优先于
读写兜底超时；Context 超时后连接可能被关闭并从池中淘汰，这是正确取消语义的一部分。

## 6. 公共命令外观原则

所有包装方法都遵循：

1. 第一个参数必须是 `context.Context`；
2. 当前 goroutine 同步执行，不切换到 Service 工作协程；
3. 不自动 JSON/PB 编解码，不增加业务重试；
4. 返回普通 Go 值和 `error`，不返回 go-redis Cmd 对象；
5. 保留有意义的 Redis 返回结果，不把新增数量、删除数量和条件结果丢弃；
6. 单值读取不存在时返回 `ErrNil`；批量集合/Map 读取不存在时返回长度为 0 的结果和 `nil` Error；
   `MGet/HMGet` 使用位置对应的 `OptionalString`，TTL/PTTL 和 Type 保留各自特殊值；
7. 空 Key 是合法 Redis Key，不由基础模块禁止；空批量参数和非法范围返回明确参数错误；
8. 高级、低频或版本变化快的命令通过 `Client()` 使用。

传入 `nil` Context 返回 `ErrInvalidArgument`，不替调用方创建 `context.Background()`；这能保留取消、Deadline
和 Trace Value 的强制性。`ErrInvalidArgument` 同样用于空批量、非法 Count/Range、Nil 回调和非法 Duration。

实现中的每个导出类型、常量、错误、接口和方法都必须有完整中文 GoDoc，说明参数、单位、默认值、
返回语义、Context、空值、Cluster Slot 和性能风险。复杂方法必须另有可编译 `Example...`。

Cluster 下，`Del/Unlink/Exists/Rename`、`MGet/MSet/MSetNX`、`LMove`、`SMove/SDiff/SInter/SUnion`、
BitOp、事务、Watch 和 Lua 等所有服务端要求单 Slot 的多 Key 操作都必须使用相同 Hash Tag。普通 Pipeline
是例外：它可以包含不同 Slot，但会被 Client 按节点拆批，不因此获得跨 Slot 原子性。教程必须先教
`player:{1001}:profile`、`player:{1001}:session` 这类可读 Key 命名，再讲命令；不增加会隐藏最终 Redis Key
的 Key Builder 框架。

### 6.1 公共错误与辅助类型

```go
var (
    ErrNotSetup        error
    ErrAlreadySetup    error
    ErrNotRunning      error
    ErrInvalidConfig   error
    ErrInvalidArgument error
    ErrUnsupportedMode error
    ErrNil             error
    ErrInvalidScore    error
    ErrLockNotObtained error
    ErrLockNotHeld     error
)

const (
    TTLNoExpiration = -1 * time.Second
    TTLKeyNotFound  = -2 * time.Second

    PTTLNoExpiration = -1 * time.Millisecond
    PTTLKeyNotFound  = -2 * time.Millisecond

    MinExactScore int64 = -(1 << 53)
    MaxExactScore int64 = 1 << 53
)

type ListSide uint8

const (
    ListLeft ListSide = iota
    ListRight
)

type ScoredMember struct {
    Member string
    Score  int64
}

// OptionalString 表示 MGet/HMGet 中一个与输入位置对应的可选字符串。
// Exists 为 false 时 Value 必须为空，调用方不得把它与真实空字符串混淆。
type OptionalString struct {
    Value  string
    Exists bool
}
```

`ErrNil` 与官方 `redis.Nil` 保持 `errors.Is` 可判断关系。Sorted Set 便利层只接受 Redis 双精度格式能精确
表达的整数；需要小数、无穷边界、排除边界或复杂 ZAdd Args 时使用 `Client()`。

`MGet/HMGet` 不返回 `[]any`。从使用者角度看，普通缓存批量读取若必须逐项断言 `string` 并辨认 `nil`，
会反复产生样板代码和类型错误；`[]OptionalString` 在保留输入顺序、空字符串和不存在三种状态的同时，
不加入序列化或业务语义。

## 7. 高频命令层

以下是首批完整公共签名。实现时每个方法必须补充第 16 章约定的中文 GoDoc；本章重点冻结名称、参数、
返回类型和行为，不用表格省略 `ctx`。

### 7.1 Key

```go
func (module *Module) Del(ctx context.Context, keys ...string) (int64, error)
func (module *Module) Unlink(ctx context.Context, keys ...string) (int64, error)
func (module *Module) Exists(ctx context.Context, keys ...string) (int64, error)
func (module *Module) Type(ctx context.Context, key string) (string, error)
func (module *Module) Expire(ctx context.Context, key string, expiration time.Duration) (bool, error)
func (module *Module) ExpireAt(ctx context.Context, key string, expiration time.Time) (bool, error)
func (module *Module) Persist(ctx context.Context, key string) (bool, error)
func (module *Module) TTL(ctx context.Context, key string) (time.Duration, error)
func (module *Module) PTTL(ctx context.Context, key string) (time.Duration, error)
func (module *Module) Rename(ctx context.Context, key string, newKey string) error
func (module *Module) Scan(
    ctx context.Context,
    cursor uint64,
    pattern string,
    count int64,
) (keys []string, nextCursor uint64, err error)
```

- `Del/Unlink/Exists` 返回实际命中数量；空 Key 列表返回参数错误；
- `Expire/ExpireAt/Persist` 返回操作是否实际应用；
- `TTL` 使用秒精度，`PTTL` 使用毫秒精度，均保留 Redis 的不存在和无过期特殊值；
- `Scan` 的 Count 只是服务端提示，调用方必须循环到 Cursor 为 0；该便利方法只支持 Standalone/Sentinel，
  Cluster 调用返回 `ErrUnsupportedMode`，避免只扫描某个节点却让使用者误认为覆盖整个集群。Cluster 全量
  扫描属于低频运维能力，应优先维护显式索引集合；确需扫描时在受控工具中通过具体 `ClusterClient` 遍历
  每个 Primary，并处理拓扑变化、重复 Key、限速和取消。

### 7.2 String

```go
func (module *Module) Set(
    ctx context.Context,
    key string,
    value any,
    expiration time.Duration,
) error

func (module *Module) SetNX(
    ctx context.Context,
    key string,
    value any,
    expiration time.Duration,
) (bool, error)

func (module *Module) SetXX(
    ctx context.Context,
    key string,
    value any,
    expiration time.Duration,
) (bool, error)

func (module *Module) SetKeepTTL(ctx context.Context, key string, value any) error
func (module *Module) Get(ctx context.Context, key string) (string, error)
func (module *Module) GetBytes(ctx context.Context, key string) ([]byte, error)
func (module *Module) GetDel(ctx context.Context, key string) (string, error)
func (module *Module) GetEx(
    ctx context.Context,
    key string,
    expiration time.Duration,
) (string, error)
func (module *Module) MGet(ctx context.Context, keys ...string) ([]OptionalString, error)
func (module *Module) MSet(ctx context.Context, values map[string]any) error
func (module *Module) MSetNX(ctx context.Context, values map[string]any) (bool, error)
func (module *Module) Incr(ctx context.Context, key string) (int64, error)
func (module *Module) IncrBy(ctx context.Context, key string, increment int64) (int64, error)
func (module *Module) Decr(ctx context.Context, key string) (int64, error)
func (module *Module) DecrBy(ctx context.Context, key string, decrement int64) (int64, error)
func (module *Module) Append(ctx context.Context, key string, value string) (int64, error)
```

- `expiration == 0` 表示持久 Key，负 Duration 返回参数错误；
- `SetKeepTTL` 用于修改值但保留现有 TTL，不用特殊负 Duration 暗示行为；
- `GetEx` 原子读取并更新 TTL，适合滑动会话；Duration 必须大于 0；
- `MGet` 结果与 Keys 一一对应，不存在项 `Exists=false`，真实空字符串仍为 `Exists=true`；
- `MSet/MSetNX` 使用 Map 消除奇数参数和 Key/Value 顺序错误；空 Map 返回参数错误；
- Cluster 下 `MGet/MSet/MSetNX` 的全部 Key 必须同 Slot，教程使用 `{playerID}` 等 Hash Tag；
- 不提供 Float 增减包装，确有小数需求时使用 `Client()`。

### 7.3 Hash

```go
func (module *Module) HSet(
    ctx context.Context,
    key string,
    field string,
    value any,
) (bool, error)

func (module *Module) HSetMany(
    ctx context.Context,
    key string,
    values map[string]any,
) (int64, error)

func (module *Module) HSetNX(
    ctx context.Context,
    key string,
    field string,
    value any,
) (bool, error)

func (module *Module) HGet(ctx context.Context, key string, field string) (string, error)
func (module *Module) HGetBytes(ctx context.Context, key string, field string) ([]byte, error)
func (module *Module) HMGet(
    ctx context.Context,
    key string,
    fields ...string,
) ([]OptionalString, error)
func (module *Module) HGetAll(ctx context.Context, key string) (map[string]string, error)
func (module *Module) HExists(ctx context.Context, key string, field string) (bool, error)
func (module *Module) HDel(ctx context.Context, key string, fields ...string) (int64, error)
func (module *Module) HLen(ctx context.Context, key string) (int64, error)
func (module *Module) HKeys(ctx context.Context, key string) ([]string, error)
func (module *Module) HVals(ctx context.Context, key string) ([]string, error)
func (module *Module) HIncrBy(
    ctx context.Context,
    key string,
    field string,
    increment int64,
) (int64, error)
func (module *Module) HScan(
    ctx context.Context,
    key string,
    cursor uint64,
    pattern string,
    count int64,
) (values map[string]string, nextCursor uint64, err error)
```

- `HSet` 返回 Field 是否为新增，`HSetMany` 返回新增 Field 数量；
- `HMGet` 与 Fields 一一对应，使用 `OptionalString` 保留不存在状态；
- `HGetAll/HKeys/HVals` 可能一次返回大结果，教程必须建议大 Hash 使用 `HScan`；
- 不提供 Hash Struct 自动映射和 JSON/PB 编解码，业务 Module 自行选择数据格式。

### 7.4 List

```go
func (module *Module) LPush(ctx context.Context, key string, values ...any) (int64, error)
func (module *Module) LPushX(ctx context.Context, key string, values ...any) (int64, error)
func (module *Module) RPush(ctx context.Context, key string, values ...any) (int64, error)
func (module *Module) RPushX(ctx context.Context, key string, values ...any) (int64, error)
func (module *Module) LPop(ctx context.Context, key string) (string, error)
func (module *Module) LPopBytes(ctx context.Context, key string) ([]byte, error)
func (module *Module) LPopN(ctx context.Context, key string, count int64) ([]string, error)
func (module *Module) RPop(ctx context.Context, key string) (string, error)
func (module *Module) RPopBytes(ctx context.Context, key string) ([]byte, error)
func (module *Module) RPopN(ctx context.Context, key string, count int64) ([]string, error)
func (module *Module) LIndex(ctx context.Context, key string, index int64) (string, error)
func (module *Module) LSet(ctx context.Context, key string, index int64, value any) error
func (module *Module) LRange(ctx context.Context, key string, start int64, stop int64) ([]string, error)
func (module *Module) LLen(ctx context.Context, key string) (int64, error)
func (module *Module) LTrim(ctx context.Context, key string, start int64, stop int64) error
func (module *Module) LRem(
    ctx context.Context,
    key string,
    count int64,
    value any,
) (int64, error)
func (module *Module) LMove(
    ctx context.Context,
    source string,
    destination string,
    from ListSide,
    to ListSide,
) (string, error)
```

- Push 返回操作后的列表长度；Pop 在列表不存在或为空时返回 `ErrNil`；
- `LPopN/RPopN` 要求 Count 大于 0，适合有界批量领取，不建立后台消费协程；
- `LRange` 的 Stop 包含在结果中，必须在业务层设置合理上限；
- `LRem` 的 Count 正负和零语义必须在 GoDoc 和 Example 中完整说明；
- `LMove` 原子移动一个元素，Cluster 下 Source 与 Destination 必须同 Slot；
- 首批不包装 BLPop/BRPop，避免隐藏专用连接、阻塞和退出所有权。

### 7.5 Set

```go
func (module *Module) SAdd(ctx context.Context, key string, members ...any) (int64, error)
func (module *Module) SRem(ctx context.Context, key string, members ...any) (int64, error)
func (module *Module) SIsMember(ctx context.Context, key string, member any) (bool, error)
func (module *Module) SMIsMember(ctx context.Context, key string, members ...any) ([]bool, error)
func (module *Module) SMembers(ctx context.Context, key string) ([]string, error)
func (module *Module) SCard(ctx context.Context, key string) (int64, error)
func (module *Module) SPop(ctx context.Context, key string) (string, error)
func (module *Module) SPopN(ctx context.Context, key string, count int64) ([]string, error)
func (module *Module) SRandMember(ctx context.Context, key string) (string, error)
func (module *Module) SRandMemberN(ctx context.Context, key string, count int64) ([]string, error)
func (module *Module) SMove(
    ctx context.Context,
    source string,
    destination string,
    member any,
) (bool, error)
func (module *Module) SDiff(ctx context.Context, keys ...string) ([]string, error)
func (module *Module) SInter(ctx context.Context, keys ...string) ([]string, error)
func (module *Module) SUnion(ctx context.Context, keys ...string) ([]string, error)
func (module *Module) SScan(
    ctx context.Context,
    key string,
    cursor uint64,
    pattern string,
    count int64,
) (members []string, nextCursor uint64, err error)
```

- `SPop/SRandMember` 在集合不存在或为空时返回 `ErrNil`；
- `SMembers/SDiff/SInter/SUnion` 可能产生大结果，教程必须给出规模警告；
- `SScan` 用于有界遍历，不承诺顺序或单轮返回数量；
- Cluster 下跨 Key 运算和 SMove 必须同 Slot。

### 7.6 Sorted Set

```go
func (module *Module) ZAdd(
    ctx context.Context,
    key string,
    members ...ScoredMember,
) (int64, error)
func (module *Module) ZAddNX(
    ctx context.Context,
    key string,
    members ...ScoredMember,
) (int64, error)
func (module *Module) ZAddXX(
    ctx context.Context,
    key string,
    members ...ScoredMember,
) (int64, error)
func (module *Module) ZIncrBy(
    ctx context.Context,
    key string,
    increment int64,
    member string,
) (int64, error)
func (module *Module) ZRem(ctx context.Context, key string, members ...string) (int64, error)
func (module *Module) ZScore(ctx context.Context, key string, member string) (int64, error)
func (module *Module) ZRank(ctx context.Context, key string, member string) (int64, error)
func (module *Module) ZRevRank(ctx context.Context, key string, member string) (int64, error)
func (module *Module) ZRange(ctx context.Context, key string, start int64, stop int64) ([]string, error)
func (module *Module) ZRevRange(ctx context.Context, key string, start int64, stop int64) ([]string, error)
func (module *Module) ZRangeWithScores(
    ctx context.Context,
    key string,
    start int64,
    stop int64,
) ([]ScoredMember, error)
func (module *Module) ZRevRangeWithScores(
    ctx context.Context,
    key string,
    start int64,
    stop int64,
) ([]ScoredMember, error)
func (module *Module) ZRangeByScore(
    ctx context.Context,
    key string,
    min int64,
    max int64,
    offset int64,
    count int64,
) ([]string, error)
func (module *Module) ZRevRangeByScore(
    ctx context.Context,
    key string,
    min int64,
    max int64,
    offset int64,
    count int64,
) ([]string, error)
func (module *Module) ZRangeByScoreWithScores(
    ctx context.Context,
    key string,
    min int64,
    max int64,
    offset int64,
    count int64,
) ([]ScoredMember, error)
func (module *Module) ZRevRangeByScoreWithScores(
    ctx context.Context,
    key string,
    min int64,
    max int64,
    offset int64,
    count int64,
) ([]ScoredMember, error)
func (module *Module) ZCount(ctx context.Context, key string, min int64, max int64) (int64, error)
func (module *Module) ZCard(ctx context.Context, key string) (int64, error)
func (module *Module) ZRemRangeByRank(
    ctx context.Context,
    key string,
    start int64,
    stop int64,
) (int64, error)
func (module *Module) ZRemRangeByScore(
    ctx context.Context,
    key string,
    min int64,
    max int64,
) (int64, error)
func (module *Module) ZPopMin(
    ctx context.Context,
    key string,
    count int64,
) ([]ScoredMember, error)
func (module *Module) ZPopMax(
    ctx context.Context,
    key string,
    count int64,
) ([]ScoredMember, error)
func (module *Module) ZScan(
    ctx context.Context,
    key string,
    cursor uint64,
    pattern string,
    count int64,
) (members []ScoredMember, nextCursor uint64, err error)
```

- 所有 Score 输入先校验 `MinExactScore <= score <= MaxExactScore`；
- 读取到小数或精度范围外值返回 `ErrInvalidScore`，不能静默截断；
- `ZAdd/ZAddNX` 返回新增成员数；`ZAddXX` 内部使用 `XX+CH`，返回已有成员中 Score 实际改变的数量，
  避免官方 `XX` 默认永远不能返回新增数而使包装结果失去意义；
- `ZIncrBy` 使用模块私有 Lua 原子执行读取、整数校验、累加、范围校验和写入，避免先写后报溢出；
- Offset 必须非负，Count 大于 0；首批范围查询只提供有限、包含端点的整数区间；
- 不提供组合分数、先到先排、多字段排行等业务封装；业务通过基础 ZSet、Lua 和 Client 自行实现。

### 7.7 Bitmap

```go
func (module *Module) SetBit(
    ctx context.Context,
    key string,
    offset int64,
    value bool,
) (previous bool, err error)
func (module *Module) GetBit(ctx context.Context, key string, offset int64) (bool, error)
func (module *Module) BitCount(
    ctx context.Context,
    key string,
    startByte int64,
    endByte int64,
) (int64, error)
func (module *Module) BitOpAnd(
    ctx context.Context,
    destination string,
    keys ...string,
) (int64, error)
func (module *Module) BitOpOr(
    ctx context.Context,
    destination string,
    keys ...string,
) (int64, error)
func (module *Module) BitOpXor(
    ctx context.Context,
    destination string,
    keys ...string,
) (int64, error)
func (module *Module) BitOpNot(
    ctx context.Context,
    destination string,
    source string,
) (int64, error)
```

- Offset 必须非负，SetBit 的 Bool 外观避免使用 0/1 魔法值；
- `SetBit` 返回修改前的 Bit；BitOp 返回 Destination 字符串长度；
- `BitCount` 范围单位固定为字节，并在名称和 GoDoc 参数中明确；
- Cluster 下所有 Source 和 Destination 必须同 Slot。

### 7.8 明确不包装的命令

首批通过 `Client()` 使用：

- `KEYS`：教程明确禁止在线业务使用；Standalone/Sentinel 遍历使用 Scan，Cluster 使用显式索引或受控
  逐 Primary 扫描工具；
- Pub/Sub、Streams 和阻塞 List：具有独立连接、goroutine、背压和退出所有权；
- Geo、HyperLogLog、BitField、Search、JSON Module 等专项能力；
- 小数命令、复杂 Score 边界、Lex 查询和完整 ZAdd Args；
- Cluster 节点管理、Client Tracking、缓存和版本变化快的管理命令。

go-redis v9.21+ 的 `GetToBuffer` 可以把 String 直接读取到调用方缓冲区，但当前缓冲区过小错误没有稳定的
可判断类型，而且调用方必须自行保证缓冲区大小、独占写入和复用生命周期。首批不为它增加高频包装；
只有 Benchmark 证明 `GetBytes` 分配成为实际瓶颈时，才通过 `Client()` 在局部热路径使用并补充所有权测试。
普通 `Set(..., []byte, ttl)` 已走官方字节写入路径，不需要重复增加 `SetFromBuffer` 别名。

## 8. 原生 Client 与组合能力

### 8.1 Client、Ping 与 Do

```go
// Client 返回当前运行中的官方 UniversalClient。
// 返回值只借用，所有权仍属于 Module；调用方不得 Close 或保存到 Module 生命周期之外。
func (module *Module) Client() redis.UniversalClient

// Ping 使用调用方 Context 检查当前逻辑 Redis 部署。
// Cluster 会检查当前全部 Primary，调用成本随 Shard 数量增长，不应用于业务热路径。
func (module *Module) Ping(ctx context.Context) error

// Do 执行未进入高频便利层的普通 Redis 命令，并返回原始 Result。
func (module *Module) Do(ctx context.Context, args ...any) (any, error)

// WithClient 在当前 goroutine 中同步执行回调，不独占连接，也不建立事务。
func (module *Module) WithClient(
    ctx context.Context,
    fn func(context.Context, redis.UniversalClient) error,
) error
```

Module 未运行时 `Client()` 返回 `nil`，其余方法返回 `ErrNotRunning`。正常业务回调只在所有 Module 启动
完成后开放，因此业务热路径无需反复调用 `Client()` 做 Nil 检查；包装方法内部仍必须稳定处理非法生命
周期调用。

`WithClient` 用于业务 Module 封装少量官方高级操作。回调和外层调用在同一 goroutine，回调不得调用
`Close()`，不得把 Client 交给失去生命周期所有权的后台 goroutine。

### 8.2 Pipeline 与事务

```go
// Pipelined 在回调中收集命令并一次发送，减少网络往返，但不保证命令原子性。
func (module *Module) Pipelined(
    ctx context.Context,
    fn func(context.Context, redis.Pipeliner) error,
) ([]redis.Cmder, error)

// TxPipelined 使用 MULTI/EXEC 执行命令，不提供数据库式回滚。
func (module *Module) TxPipelined(
    ctx context.Context,
    fn func(context.Context, redis.Pipeliner) error,
) ([]redis.Cmder, error)

// Watch 对指定 Key 执行乐观并发控制；冲突时保留 redis.TxFailedErr。
func (module *Module) Watch(
    ctx context.Context,
    fn func(context.Context, *redis.Tx) error,
    keys ...string,
) error
```

回调返回错误时不执行已经收集的命令。`Pipelined` 只优化往返，不保证原子性；`TxPipelined` 保证 EXEC
批次执行，不表示其中某条运行时错误会让其他命令回滚；`Watch` 发生业务冲突时不由 Module 自动重试，
业务需要时必须在 Await Worker 中使用 Context、有界次数和退避自行组织幂等重试。

Cluster 下普通 `Pipelined` 可以包含不同 Slot，go-redis 会按节点拆分执行，因此它既不是一次全局网络往返，
也不保证跨节点顺序或原子性。`TxPipelined` 和 `Watch` 的全部 Key 必须使用相同 Hash Tag。v9.22.0 的
Cluster Client 可能因 MOVED、ASK、TRYAGAIN、只读节点或可重试网络错误重新路由，并再次调用 `Watch`
回调或把整个事务重新执行；Origin 不额外叠加重试，但回调仍必须幂等且不能包含发邮件、发奖励、写数据库
等不可重复外部副作用。Pipeline 回调中使用官方类型是高级组合入口的有意例外，不要求 Origin 复制全部
Cmd API。

### 8.3 Lua

```go
// RunScript 执行一个可复用的官方 Script。
// keys 只放 Redis Key，args 只放普通参数；Cluster 下所有 Key 必须同 Slot。
func (module *Module) RunScript(
    ctx context.Context,
    script *redis.Script,
    keys []string,
    args ...any,
) (any, error)
```

业务脚本声明为包级不可变 `redis.NewScript()`，不得在每次请求中重复创建。go-redis 负责 EVALSHA 和
NOSCRIPT 后的 EVAL 回退，Module 不自建 SHA Registry 或预加载状态。动态 Key 必须通过 KEYS 传入，
不能藏在 ARGV；Cluster 教程统一使用 `{playerID}` 等 Hash Tag。

模块不内置金币扣减、排行榜、背包、奖励等业务脚本。整数 `ZIncrBy` 的私有校验脚本只保证基础整数
外观正确，不公开脚本内容或引入排行语义。

## 9. 分布式锁

### 9.1 外观

```go
// TryLock 立即尝试一次。锁被占用不是系统错误，返回 acquired=false、err=nil。
func (module *Module) TryLock(
    ctx context.Context,
    key string,
    ttl time.Duration,
) (lock *Lock, acquired bool, err error)

// Lock 在 waitTimeout 和 ctx 共同形成的边界内等待获得锁。
func (module *Module) Lock(
    ctx context.Context,
    key string,
    ttl time.Duration,
    waitTimeout time.Duration,
) (*Lock, error)

// WithLock 获得锁、同步执行回调并在结束时有界释放。
func (module *Module) WithLock(
    ctx context.Context,
    key string,
    ttl time.Duration,
    waitTimeout time.Duration,
    fn func(context.Context) error,
) error

// Key 返回当前 Lease 使用的 Redis Key。
func (lock *Lock) Key() string

// TTL 查询服务端确认的剩余 Lease 时间。
func (lock *Lock) TTL(ctx context.Context) (time.Duration, error)

// Refresh 在锁仍属于当前持有者时把 Lease 更新为 ttl。
func (lock *Lock) Refresh(ctx context.Context, ttl time.Duration) error

// Release 仅在 Token 仍匹配时释放锁；重复释放或 Lease 已失效返回 ErrLockNotHeld。
func (lock *Lock) Release(ctx context.Context) error
```

### 9.2 行为与错误

- Lock Key 必须非空，TTL 必须大于 0；Lock/WithLock 的 WaitTimeout 必须大于 0；
- `TryLock` 不重试；`Lock` 首次立即尝试，后续使用 50ms 加约 ±20% 抖动的有界间隔；
- 实际等待时间取 WaitTimeout 与 Context 剩余时间的较小值；
- Context 先结束返回 `ctx.Err()`，WaitTimeout 先结束返回 `ErrLockNotObtained`；
- 不启动自动续租 goroutine，长任务必须评估最坏耗时并显式 Refresh；
- Lock 不暴露第三方对象和 Token，避免业务绕过所有权校验；
- WithLock 回调与调用方在同一 goroutine；它使用独立且不超过 2s 的清理 Context 尝试 Release，避免
  业务 Context 已取消后完全跳过释放；
- 回调和 Release 同时失败时使用 `errors.Join` 保留两个错误；进程崩溃时依靠 TTL 最终释放。

锁适用于缓存重建抑制、单实例任务抢占、匹配结算协调和非关键刷新任务。锁不能作为金币、背包、奖励、
支付或跨系统事务的唯一正确性保证；这些场景必须继续具有数据库或 Redis 内部的原子、唯一和幂等依据。

## 10. 游戏项目接口易用性 Review

设计完成前按真实游戏用法复核，而不是只按 Redis 命令分类计数。复核结果如下：

| 游戏场景 | 推荐基础外观 | 使用注意事项 |
| --- | --- | --- |
| 玩家对象缓存 | `Set/Get/GetBytes/SetKeepTTL` | JSON/PB 由业务 Module 编解码；缓存不是最终数据源 |
| 登录会话与滑动过期 | `SetNX/GetEx/GetDel/PTTL` | 一次性 Token 用 GetDel；GetEx 原子读取并续期 |
| 批量加载玩家摘要 | `MGet` | `OptionalString` 区分不存在与空字符串；Cluster Key 使用 Hash Tag |
| 玩家字段缓存 | `HSet/HSetMany/HMGet/HScan` | 大 Hash 不使用 HGetAll 全量热读 |
| 在线玩家集合 | `SAdd/SRem/SIsMember/SScan` | SMembers 只用于规模明确的小集合 |
| 匹配候选或有界任务列表 | `LPush/RPopN/LMove` | List 不是可靠队列；需要确认、重放时评估 Streams/Kafka |
| 简单积分有序集合 | `ZAdd/ZIncrBy/ZRevRangeWithScores` | 只包装整数；复合排名规则由业务实现 |
| 每日签到或活动标记 | `SetBit/GetBit/BitCount` | Offset 与日期换算属于业务；范围单位为字节 |
| 一次请求中的多条独立读写 | `Pipelined` | 只减少 RTT，不自动保证原子性；Cluster 可能按节点拆批 |
| 乐观并发更新 | `Watch` 与 `TxPipelined` | 业务冲突有界重试；Cluster 拓扑恢复还可能重新调用回调，回调必须幂等且无外部副作用 |
| Redis 内多命令原子操作 | `RunScript` | Key 进入 KEYS；Cluster 使用同一 Hash Tag |
| 缓存重建、每日重置、匹配结算抢占 | `TryLock/Lock/WithLock` | Lease 可能过期，不能代替业务幂等和最终约束 |

推荐业务 Module 组合 `redismodule.Module`，把 Key 命名、JSON/PB 编解码、缓存策略和领域错误集中在业务
边界内，不让 Service 到处拼 Key 或直接处理 `[]byte`：

```go
type PlayerCacheModule struct {
    redismodule.Module
}

func (module *PlayerCacheModule) SavePlayer(
    ctx context.Context,
    playerID int64,
    player *playerpb.Player,
) error {
    data, err := proto.Marshal(player)
    if err != nil {
        return fmt.Errorf("marshal player %d: %w", playerID, err)
    }

    key := fmt.Sprintf("player:{%d}:profile", playerID)
    return module.Set(ctx, key, data, 15*time.Minute)
}
```

对应的读取方法必须在业务 Module 内判断 `ErrNil`、校验反序列化结果，并决定回源、负缓存或返回错误。
基础 Redis Module 不知道 `Player`、不自动回源，也不把缓存 Miss 记录为系统错误。Example 必须展示这种
“基础外观 + 业务组合”的推荐结构，而不是把全部 Redis 调用零散放在 Service 中。

本轮易用性 Review 对原始外观作出以下收敛：

1. `MGet/HMGet` 从 `[]any` 改为 `[]OptionalString`，消除反复类型断言；
2. 增加 `PTTL`，避免毫秒级缓存与 Lease 诊断只能绕到 Client；
3. 增加 `SetKeepTTL` 与 `GetEx`，覆盖会话更新和滑动过期的基础 Redis 语义；
4. 增加 `LPopN/RPopN`，支持有界批量领取而不引入消费者框架；
5. 明确 `ZAddXX` 返回“实际更新数”，避免包装一个永远返回 0 的结果；
6. Cluster 下拒绝便利层 `Scan`，防止不完整结果被当作全量结果；
7. 仍不增加 JSON/PB、排行组合、可靠队列、自动业务重试和自动锁续租，防止基础库承担业务策略。

这些调整都对应 Redis 原生命令或结果类型整理，没有增加游戏规则。若后续用法只能通过复杂 Option、
大量类型断言或重复不安全脚本才能完成，应先回到本文做同样的场景 Review，不能直接在实现中随意补接口。

### 10.1 易用性验收规则

每个公共接口在进入实现前必须能用一个真实场景回答以下问题：

1. 使用者是否能从名称和签名知道它返回的是值、旧值、新增数、删除数还是条件结果；
2. 缓存 Miss、真实空字符串、空集合、Context 取消和 Module 停止是否容易区分；
3. 一般用法是否只需要基础参数，是否为了少数场景引入了大而难懂的 Options；
4. 是否把 Key、TTL、序列化、排行规则、可靠队列或重试策略等业务决策错误地下沉到基础库；
5. Cluster 同 Slot、可能重复执行、一次返回大结果和锁 Lease 风险是否在调用点附近可见；
6. 普通方法是否能直接使用，高级能力是否仍能通过 `Client/WithClient` 组合，而不需要复制官方 API；
7. 教程是否同时给出成功、Miss/占用、超时、冲突或失败处理，不能只有 Happy Path。

无法满足这些规则的接口不得仅因为 v2 存在或官方 Driver 提供就进入首批外观。

## 11. 所有权、Context 与 goroutine

| 操作 | 执行位置 |
| --- | --- |
| `Client()`、`Lock.Key()` | 当前 goroutine，不执行网络 I/O |
| 高频命令、Ping、Do、锁方法 | 当前 goroutine，同步等待 Redis I/O |
| `WithClient` 回调 | 当前 goroutine |
| Pipeline/TxPipeline/Watch 回调 | 当前 goroutine |
| RunScript | 当前 goroutine，同步等待 Redis I/O |
| WithLock 回调 | 当前 goroutine |
| Origin `Await` 回调中的上述操作 | Await Worker |
| Await 完成后的业务代码 | Service 串行工作协程 |

Module 不提供默认后台 Context。所有操作必须继承调用方 Context；在 Service 串行工作协程中不能直接
执行可能阻塞的 Redis I/O，应放入 `Await`。业务请求取消、服务停止或 Deadline 到达时，由同一 Context
终止等待。

Client 和锁 Client 由 Module 唯一拥有。使用者不得关闭 Client、不得在 Stop 后复用 Handle，也不得把
回调中的 Client、Pipeline、Tx 或 Lock 交给无所有者的后台 goroutine。Module 不为命令创建辅助 goroutine，
也不自建连接池、对象池、结果缓存和请求队列。

## 12. 重试、超时和原子性边界

### 12.1 命令重试

Origin 配置中的 `max_retries: 0` 明确表示不做节点级命令重试，内部映射为 go-redis 禁用重试所需值，
而不是继承 Standalone/Sentinel Client 的三次默认。全局重试可能让 INCR、LPush、Lua 等在响应丢失时
重复执行，因此必须由使用者明确开启并理解 Client 中全部命令的幂等性。

Cluster 模式固定 `max_retries: 0`，但这不等于 Cluster 完全不重试：go-redis 仍会在 `max_redirects` 边界内
处理网络错误和 MOVED/ASK 等拓扑变化，事务还可能作为整体重新执行。教程不能把 MaxRedirects 描述为
“只有重定向”，也不能对 Cluster 写操作作“配置 0 就绝不重复”的承诺。

Context 或读写超时不证明写入一定没有发生。教程和错误注释必须提醒：超时后的业务重试需要幂等 Key、
唯一记录、脚本状态检查或数据库最终约束。

### 12.2 原子性

| 操作 | 原子性说明 |
| --- | --- |
| 单条 Redis 命令 | 按 Redis 服务端命令语义原子执行 |
| Pipeline | 不保证批次原子性，只减少网络往返 |
| TxPipeline | MULTI/EXEC 批次执行，不提供数据库式运行时错误回滚 |
| Watch | 提交前检测被监控 Key 变化，冲突返回 TxFailedErr；Cluster 拓扑恢复可能再次调用回调 |
| Lua | 脚本在 Redis 内原子执行，但长脚本会阻塞 Redis |
| Lock | 带 TTL 的 Lease，不等于永不失效的互斥证明 |
| Cluster 多 Key | 只有同 Slot 时才能执行要求单 Slot 的命令、事务或脚本 |

## 13. 错误与安全

- 配置、空参数、非法范围和生命周期错误使用 Origin 稳定错误码或 Sentinel；
- Redis 服务端、网络、Context、事务和脚本错误保留官方错误链；
- `errors.Is(err, redismodule.ErrNil)` 可判断不存在，`errors.Is(err, redis.TxFailedErr)` 可判断 Watch 冲突；
- 锁占用在 TryLock 中不是错误；Lock 等待耗尽才返回 `ErrLockNotObtained`；
- 错误不得包含 Password、完整 URI、Token、证书内容、Lua ARGV 业务数据或 Hook 捕获的敏感值；
- TLS 使用系统 Root CA 加可选 CA 文件，不替换系统池；禁止跳过证书校验；
- Hook 默认不安装，教程提醒命令参数可能包含玩家 Token、会话和业务数据，监控不得无差别记录参数；
- KEYS、无界 SMembers/HGetAll/LRange、长 Lua 和大 Pipeline 必须在 GoDoc 与教程标出线上风险。

## 14. 三种拓扑教程配置

教程必须分别提供最小配置和带注释生产配置。以下只冻结外观，真实文档还要配套字段表和调整依据。

### 14.1 Standalone

```yaml
redis:
  # 可选，默认 standalone。建议显式填写，便于 Review 配置意图。
  mode: standalone

  # 必填。Standalone 只能配置一个地址。
  addresses:
    - redis.internal:6379

  # 可选。建议使用环境变量，不把密码写入仓库。
  username: game-service
  password: ${REDIS_PASSWORD}

  # 可选，默认 0。
  database: 0
```

### 14.2 Sentinel

```yaml
redis:
  mode: sentinel
  addresses:
    - sentinel-1.internal:26379
    - sentinel-2.internal:26379
    - sentinel-3.internal:26379
  username: game-service
  password: ${REDIS_PASSWORD}
  database: 0

  sentinel:
    # 必填。必须与 Sentinel monitor 名称一致。
    master_name: game-master

    # 可选。只有 Sentinel 自身启用 ACL 时配置，不继承数据节点凭证。
    username: sentinel-service
    password: ${REDIS_SENTINEL_PASSWORD}
```

### 14.3 Cluster

```yaml
redis:
  mode: cluster
  addresses:
    # Seed 地址不要求列出集群全部节点。
    - redis-cluster-1.internal:6379
    - redis-cluster-2.internal:6379
  username: game-service
  password: ${REDIS_PASSWORD}
  database: 0

  cluster:
    # 默认 false。强一致在线读保持关闭。
    read_from_replicas: false

    # 默认 false。只有 read_from_replicas=true 时才能开启。
    route_by_latency: false

    # 可选，默认 3。过高会放大故障时尾延迟。
    max_redirects: 3
```

TLS 示例同时覆盖系统 CA 和 `tls_ca_file`，并说明 Windows、Ubuntu、容器挂载路径差异。三个配置示例
不能包含真实凭证或用户提供的测试服务器密码。

## 15. 教程与游戏场景 Example

### 15.1 文档入口

新增面向使用者的：

```text
docs/maintenance/v3.2/guides/Redis Module使用指南.md
examples/16-redis/01-cache-and-session
examples/16-redis/02-collections-and-ranking
examples/16-redis/03-pipeline-lua-and-concurrency
examples/16-redis/04-distributed-lock
```

根 README 的扩展组件教程表和 `docs/maintenance/v3.2/README.md` 都增加 Redis 行，基础框架教程结构不
重排。指南先给“十分钟可运行”的最小配置和最小代码，再给选型表、完整配置表、生产建议、不同拓扑、
API 参考和故障处理。每个 Example 都必须能独立构建和运行，具有 README、Windows/Linux 启动入口、
依赖检查、预期输出和清理说明；不能要求使用者先读完核心设计才知道如何运行。

### 15.2 指南必须覆盖

1. 一个 Module 对应一个逻辑 Redis 部署，单 Service 多 Module 的组合方式；
2. Standalone、Sentinel、Cluster、TLS 和 ACL 的最小/生产配置；
3. 每个配置字段的必填性、默认值、生产建议起点、调整依据和作用范围；
4. 按“缓存/会话、集合/排行、原子组合、锁、运维排障”组织的接口选型表，再附全部高频方法、参数、
   返回值、不存在语义和 Context 参考；不能只照 Redis 数据类型罗列命令；
5. `OptionalString`、整数 Score、TTL/PTTL 特殊值；
6. Scan 循环、大集合与大结果风险，以及 Cluster 下便利层 Scan 不支持和受控全节点扫描方法；
7. Pipeline、TxPipeline、Watch 的区别、回调 goroutine、Cluster 拆批/同 Slot 和回调可能再次执行；
8. Lua KEYS/ARGV、NOSCRIPT 回退、Cluster Hash Tag 和长脚本风险；
9. Client/WithClient 的所有权与适用范围；
10. 超时后写入状态不确定、非幂等命令和有界重试；
11. 分布式锁的 Lease、等待、刷新、释放、崩溃和错误处理；
12. 连接池容量公式、Pool Stats、慢命令、超时和生产排障；
13. Windows 与 Ubuntu 的构建、配置路径和测试方法；
14. Redis 7.2、Redis 8.10 和兼容云服务/代理的支持边界与验证清单；
15. 常见错误决策表：Miss、连接池耗尽、超时后写入不确定、CROSSSLOT、Watch 冲突、锁占用/过期。

### 15.3 Example 场景覆盖

| Example | 必须演示的游戏场景 | 必须演示的异常或注意事项 |
| --- | --- | --- |
| `01-cache-and-session` | 业务 Module 组合、PB 玩家缓存、批量玩家摘要、登录会话滑动续期、一次性 Token | `ErrNil` 回源、损坏缓存反序列化、`OptionalString`、TTL/PTTL 特殊值、超时后写状态不确定 |
| `02-collections-and-ranking` | 玩家字段 Hash、在线玩家 Set、匹配候选 List、整数积分 ZSet、每日签到 Bitmap | 大集合使用 Scan、有界 Range/Pop、List 非可靠队列、整数 Score 精度、复合排行留在业务层 |
| `03-pipeline-lua-and-concurrency` | 一次请求批量独立读写、Watch 乐观更新、幂等奖励领取 Lua、Cluster Hash Tag | Pipeline 部分结果、事务无数据库式回滚、Watch 有界冲突重试、Cluster 回调可重入、Lua 超时/同 Slot |
| `04-distributed-lock` | 缓存重建、匹配结算、排行刷新、每日重置、长任务刷新 Lease | 未获得锁、Context 取消、Refresh 失败、Lease 过期、重复释放、进程退出、最终幂等约束 |

所有 Example 都使用业务 Module 承载场景方法，Service 只负责调度和展示结果。示例 Key 必须可读并包含
环境/业务前缀，避免开发、测试和生产或不同游戏区服相互覆盖；TTL 必须解释选择依据。每个示例至少包含
一个可观察的失败分支，README 给出预期输出和“生产中不要这样做”的说明。

### 15.4 锁教程必须覆盖的游戏示例

| 场景 | 演示重点 |
| --- | --- |
| 玩家缓存重建 | TryLock 未获得时不重复重建，回源结果仍需校验 |
| 同一场匹配结算 | WithLock 简化短任务，结算记录仍用唯一键或幂等号 |
| 跨服排行榜定时刷新 | Lock 有界等待，未获得锁时本实例跳过本轮 |
| 每日活动重置 | 任务抢占与“已执行日期”状态共同保证防重 |
| 长时间后台任务 | 显式 Refresh，说明 Refresh 失败后必须停止受保护操作或转入补偿 |
| Context 取消 | 业务取消后 WithLock 仍有界尝试释放 |
| 锁已过期或重复释放 | `ErrLockNotHeld` 判断与安全日志 |
| Cluster Lock Key | `{eventID}` Hash Tag 和同 Slot Lua |
| 错误使用示例 | 金币、道具、奖励、支付不能把 Redis Lock 当成唯一正确性依据 |

每个锁示例都必须解释 Key 命名、TTL、WaitTimeout 的依据、代码在哪个 goroutine 执行、Lease 过期风险和
最终业务约束。示例不能只展示成功路径。

### 15.5 GoDoc 与 Example 标准

所有导出标识符必须有中文 GoDoc，方法注释至少回答：

- 方法做什么，是否产生网络 I/O；
- 每个参数的含义、单位、特殊值和合法范围；
- 返回值是总数、新增数、删除数、旧值还是条件是否成功；
- Key 不存在、Context 取消和 Module 未运行时发生什么；
- Cluster 多 Key 是否要求同 Slot；
- 是否可能一次返回大结果或阻塞 Redis。

以下方法必须提供可编译 Go Example：Scan/HScan/SScan/ZScan、MGet/HMGet、LRem、LMove、分数范围查询、
ZAddXX、Pipelined、TxPipelined、Watch、RunScript、TryLock、Lock、WithLock 和 Cluster Hash Tag。简单访问器
不机械堆叠代码片段；复杂示例优先放在 `example_test.go`，由测试编译检查，指南再给完整业务上下文。

GoDoc Example 证明单个接口如何调用，`examples/16-redis` 证明一个游戏业务 Module 如何组合多个接口；二者
不能互相替代。指南中的代码必须来自已编译 Example 或测试，禁止复制一份无法校验、最终与代码漂移的版本。

## 16. 测试设计

### 16.1 单元测试

必须覆盖：

- New/Setup、默认化、重复 Setup、启动后 Setup、未运行和停止后调用；
- 三种 Mode、地址数量、Database、Protocol、Sentinel 和 Cluster 组合校验；
- 全部 Duration、DialAttempts、PoolSize 拓扑默认值、MaxConcurrentDials、连接池上限、重试、Redirect、
  TLS/CA 和凭证脱敏；
- Option Nil、TLS 冲突、Hook 顺序和启动 Ping 前安装；
- Context Nil、取消、Deadline 和命令错误链；
- 每个高频包装的参数转换、普通结果、不存在、空批量和错误分支；
- `OptionalString` 对空字符串、缺失和输入顺序的保持；
- Sorted Set 最小/最大精确整数、小数读取、越界和 ZIncrBy 原子范围检查；
- ZAddXX 只更新已有成员并返回 Score 实际变化数；
- Scan Cursor、Count、Pattern、结果转换和 Cluster `ErrUnsupportedMode`；
- Pipeline 回调错误不执行、Cluster 跨 Slot 拆批、TxPipeline/Watch 同 Slot 拒绝、Watch 回调可重入约束；
- TryLock 占用、Lock 等待边界、抖动退出、Refresh、Release、双错误 Join；
- Cluster 启动/Ping 覆盖全部 Primary、单个 Primary 失败和拓扑变化；
- 启动部分失败逆序清理、重复 Stop 和无 goroutine/连接所有权泄漏。

为隔离官方 Driver 分支使用最小内部构造替身，不为测试扩大公共 ClientFactory API。能够稳定触发的公共
行为分支尽量达到 100% 覆盖；不能在普通环境触发的网络和系统分支逐项记录集成证据或剩余原因。

### 16.2 Windows 验证

Windows 执行：

- 全部不依赖外部 Redis 的单元测试；
- 本地真实 Standalone Redis 集成测试（环境可用时）；
- Example 编译、GoDoc Example、`go vet` 和全仓构建；
- 配置路径、CA 文件和环境变量差异测试。

### 16.3 Ubuntu 真实集成测试

Ubuntu 使用真实 Redis 环境覆盖：

- Redis 7.2 与 8.10 Standalone、三节点 Sentinel Failover 和最小 Redis Cluster；
- ACL、TLS/CA、RESP2/RESP3、Ping 和关闭；
- 全部高频数据类型操作与大结果边界；
- Pipeline、TxPipeline、Watch 冲突和 Lua NOSCRIPT 回退；
- Cluster Pipeline 跨 Slot 拆批，TxPipeline/Watch 同 Slot，MOVED/ASK/TRYAGAIN 后事务整体恢复与 Watch
  回调可重入，Hash Tag、跨 Slot 错误和可选 Replica Read；
- 连接池耗尽、PoolTimeout、Context 取消和 MaxActive 硬上限；
- 非幂等命令在超时/断链下的剩余风险验证；
- TryLock/Lock/WithLock、TTL、Refresh、进程退出后的到期释放；
- 并发命令、锁竞争、`go test -race`、覆盖率和 Example 完整运行。

真实地址和凭证只通过环境变量或受控测试配置传入，不写入仓库、文档、命令历史或测试日志。当前已提供
的 Ubuntu 主机仅作为后续实施验收环境，不在设计阶段连接或修改。

### 16.4 统一门禁

实施完成前至少执行：

```text
gofmt
go vet ./...
go test ./... -count=1
go test -race ./... -count=1        # Ubuntu
go test ./... -coverprofile=...
go build ./...
go build ./examples/16-redis/...
```

测试通过只能证明当前范围内没有已知缺陷。发现竞态、泄漏、不稳定测试、未解释低覆盖或教程 Example 无法
运行时，Redis 切片不得标记完成。

## 17. 性能与低延迟原则

- 一个 Module 生命周期只创建一个官方 Client，不按请求创建连接池；
- 高频包装只做运行时原子读取、必要参数校验和一次官方调用；
- 不增加反射式命令分派、请求对象池、结果缓存、后台队列或每命令 goroutine；
- `OptionalString` 转换只为消除调用方反复断言，Benchmark 必须记录批量结果转换分配；
- `GetBytes` 先作为默认易用外观；只有 Benchmark 证明分配是瓶颈时，才在局部热路径评估官方
  `GetToBuffer`，并记录调用方缓冲区大小、独占写入和复用生命周期；
- Pipeline 只在能减少真实 RTT 时使用，限制命令数和参数总量，禁止无界批次；
- Lua 必须短小有界，不能遍历无界集合或执行长时间业务逻辑；
- PoolSize、MaxActive、超时和重试依据 QPS、P99、Pool Stats 与故障演练调整；
- 内存池只由 go-redis 内部负责，Origin 不增加消息池或结果对象池；
- 只有 Benchmark/Profile 证明包装转换形成瓶颈后才优化，不提前使用 unsafe 或复杂缓存。

Benchmark 至少比较直接 go-redis 与 Origin 包装的单值、MGet/HMGet、ZSet 转换和并发热路径开销，记录
`ns/op`、`allocs/op`、`B/op`；真实环境记录普通负载、池耗尽和故障恢复的 P50/P95/P99。

## 18. 明确不实现

首批不实现：

- Origin v2 API、别名、拼写兼容和 redigo 适配；
- JSON/PB 自动序列化、缓存 Repository、泛型对象缓存和 Key Builder 框架；
- 排行榜组合 Score、先到优先、多字段排行和赛季规则；
- 可靠队列、消费者组、Pub/Sub/Streams 生命周期包装；
- 自动业务重试、断路器、后台 Ping、预热任务和离线命令缓存；
- go-redis 实验性的客户端缓存和自动 Pipeline；
- 自动锁续租、Redsync/Redlock、Fencing Token 和绝对互斥承诺；
- 动态 Client Registry、进程全局 Client、Origin 自建连接池或对象池；
- Redis Modules、Search、JSON、Bloom、TimeSeries 等专项外观；
- 任意修改 UniversalOptions 的公开回调。

这些能力仍可在明确所有权下通过 `Client()`、`WithClient()` 或业务 Module 使用；形成真实跨业务重复需求
后再回到设计阶段评估。

## 19. 实施完成条件

Redis 切片只有同时满足以下条件才算完成：

1. 实施前复核并固定 go-redis 和 bsm/redislock 最新稳定版本、许可证与 Go 版本；
2. 本文冻结的 Config、生命周期、高频外观、组合能力和锁外观全部实现，无额外兼容 API；
3. 所有导出内容具有完整中文 GoDoc，复杂接口具有可编译 Example；
4. 配置表、三种拓扑、游戏场景、锁风险和生产建议全部进入使用指南；
5. Windows 基础验证和 Ubuntu Standalone/Sentinel/Cluster/`-race` 验证通过；
6. 重点公共行为分支尽量达到 100% 覆盖，例外逐项说明；
7. 连接池硬上限、Context、重试、部分失败清理和错误脱敏有测试；
8. `examples/16-redis` 的四组场景可独立构建、运行，并覆盖约定的成功和失败路径；
9. Benchmark 没有发现便利层产生不合理的热路径分配或尾延迟；
10. 根 README、v3.2 索引、设计、代码、测试、Example 和指南相互一致；
11. Redis 完成后仍不单独实施，继续完成 MySQL、Kafka、Blueprint 设计，最后统一制定实施计划。

## 20. 参考资料

- [go-redis GitHub](https://github.com/redis/go-redis)
- [go-redis v9.22.0 Release](https://github.com/redis/go-redis/releases/tag/v9.22.0)
- [go-redis v9.22.0 UniversalOptions](https://github.com/redis/go-redis/blob/v9.22.0/universal.go)
- [go-redis v9.22.0 Options](https://github.com/redis/go-redis/blob/v9.22.0/options.go)
- [Redis Go Client Production Usage](https://redis.io/docs/latest/develop/clients/go/produsage/)
- [Redis Go Client Error Handling](https://redis.io/docs/latest/develop/clients/go/error-handling/)
- [Redis Connection Pools](https://redis.io/docs/latest/develop/clients/pools-and-muxing/)
- [Redis Sorted Sets](https://redis.io/docs/latest/develop/data-types/sorted-sets/)
- [Redis ZADD 精度说明](https://redis.io/docs/latest/commands/zadd/)
- [Redis Transactions](https://redis.io/docs/latest/develop/using-commands/transactions/)
- [Redis Lua Scripting](https://redis.io/docs/latest/develop/programmability/eval-intro/)
- [Redis Cluster Specification](https://redis.io/docs/latest/operate/oss_and_stack/reference/cluster-spec/)
- [bsm/redislock](https://github.com/bsm/redislock)
- [go-redsync/redsync](https://github.com/go-redsync/redsync)
- [rueian/rueidis lock](https://github.com/redis/rueidis)
