# Origin MongoDB Module 核心设计

> 状态：已确认；MySQL 暂缓，等待 Kafka 和 Blueprint 设计完成后对本轮范围统一制定实施计划
> 目标版本：v3.2
> 设计基线：Origin v3 当前 `Service`、`Module`、严格配置和 `Await` 外观
> Driver 基线：实施前复核并固定官方最新稳定版；2026-08-11 的最新稳定版为 `go.mongodb.org/mongo-driver/v2 v2.8.0`

## 1. 文档定位

本文是 `sysmodule/mongodbmodule` 实现、测试、Example 和使用教程的唯一核心设计。后续实施不得从 v2
代码或调研资料中任意恢复另一套外观；需要改变本文公共结论时，必须先更新设计并重新确认。

本轮目标不是包装一套新的 MongoDB Driver，而是提供：

1. 与 Origin Module 生命周期一致的 MongoDB Client 所有权；
2. 多集群可组合、单集群易使用的明确外观；
3. 不阻塞 Service 串行工作协程的 `Await` 使用方式；
4. 对标准 MongoDB、AWS DocumentDB 和其他 MongoDB-compatible 服务友好的配置边界；
5. 少量真正降低错误概率的索引、TLS、Session 和事务便利能力；
6. 面向游戏服务常见存取和原子更新场景的完整 Example。

设计遵守范围控制：不实现 ORM、Repository 框架、缓存、ID 生成器、自动迁移、自动业务重试或服务能力
模拟；不因“以后可能需要”复制官方 Driver 的完整 API。

## 2. v2 复核与迁移结论

v2 `mongodbmodule` 提供 `MongoModule`、`TakeSession`、默认 Context、计数、序号和索引包装。复核后结论
如下：

| v2 外观 | v3.2 结论 | 原因 |
| --- | --- | --- |
| `MongoModule.Init/Start/Stop` | 重建为标准 Module 生命周期 | v2 使用固定后台 Context，未接入 v3 生命周期取消 |
| `TakeSession()` | 删除 | 返回值并非 MongoDB Session，只是 `*mongo.Client` 包装，名称误导 |
| `GetDefaultContext()` | 删除 | 丢弃调用方取消、Deadline 和 Trace Value |
| `CountDocument()` | 删除 | 官方 `Collection.CountDocuments` 已足够直接 |
| `NextSeq()` | 删除 | `Seq` 字段和自增文档属于业务数据模型 |
| `EnsureIndex()` | 重新设计并保留 | 索引初始化高频，但必须保留官方有序 Key 和 Options |
| `EnsureUniqueIndex()` | 重新设计并保留 | 唯一约束常用于账号、角色和幂等键 |
| 固定 5 秒 Connect/Ping | 删除 | 使用启动 Context 和 URI/调用 Context 的超时语义 |
| 后台自动重连 | 不增加 | 官方 Driver 已管理拓扑监控、连接池和连接恢复 |

v3.2 不承担 v2 外观兼容。项目尚未对外发布，不保留别名、过渡层或已废弃 Driver v1 API。

## 3. 总体架构

### 3.1 一个 Module 对应一个集群

采用“一个 `mongodbmodule.Module` 对应一个 MongoDB 集群和一个官方 `mongo.Client`”的方案。每个
Module 配置一个默认业务数据库；同一集群访问其他数据库时使用 `Client().Database(name)`。

同一 Service 需要多个集群时注册多个 Module，而不是由一个 Module 管理
`map[string]*mongo.Client`：

```go
type PlayerStore struct {
    service.Module

    primary *mongodbmodule.Module
    archive *mongodbmodule.Module
}
```

该方案具有以下边界：

- 字段名在编译期表达用途，不依赖运行期字符串查找；
- 每个集群独立配置、启动、Ping、停止和报告错误；
- 单个集群失败不会产生不清楚的内部 Map 状态；
- Client 不按请求或操作创建，整个 Module 生命周期只创建一次；
- 不引入 Node 全局 Client Registry 和跨 Service 所有权。

若未来实际证明大量同进程 Service 必须共享同一连接池，应单独设计 Node/Application 级共享资源，
不能在本模块中提前建立隐式全局单例。

### 3.2 单集群业务组合

单集群业务可以匿名嵌入：

```go
type GameMongoModule struct {
    mongodbmodule.Module
}

func (module *GameMongoModule) OnInit() error {
    return module.Setup(moduleConfig)
}
```

业务集合名、BSON 表达式和数据模型方法放在 `GameMongoModule`，Service 不散落数据库细节。多个集群
场景使用一个业务父 Module 加多个命名 MongoDB 子 Module。

## 4. 包与公共类型

包路径固定为：

```text
sysmodule/mongodbmodule
```

Module 和构造外观固定为：

```go
package mongodbmodule

type Module struct {
    service.Module
}

func New(config Config, options ...Option) (*Module, error)

func (module *Module) Setup(
    config Config,
    options ...Option,
) error
```

`New` 用于创建随后交给 `Service.AddModule` 或父 `Module.AddModule` 的独立对象；`Setup` 用于匿名嵌入
后的业务 Module。两者使用完全相同的校验和运行时 Options 构造逻辑。`Setup` 只能成功一次，且不能在
启动后修改配置。

运行时代码扩展采用封闭 Option：

```go
type Option interface {
    // 由包内未导出方法封闭。
}

func WithTLSConfig(config *tls.Config) Option

func WithDriverOptions(
    options ...*mongooptions.ClientOptions,
) Option
```

普通使用者只传 `Config`。`WithDriverOptions` 仅服务于官方 Driver 的高级能力，例如 Server API、
Command/Pool Monitor、特殊认证或 BSON 配置；它不能成为第二套普通 YAML 配置。

为保持配置来源唯一，`WithDriverOptions` 还必须遵守以下限制：

- 不接受 `nil`；
- 不接受调用过 `ApplyURI` 的 `ClientOptions`，连接 URI 只能来自 `Config.URI`；
- 不接受已经设置 `TLSConfig` 的 `ClientOptions`，自定义 TLS 统一使用 `WithTLSConfig`；
- Module 合并到自己的 Options 快照，不修改调用方传入对象；Monitor、Registry 等由官方 API 明确定义为
  共享引用的高级对象，调用方在 `Setup/New` 后不得并发修改。

## 5. 配置与 URI

### 5.1 Config

生产配置只保留三项：

```go
type Config struct {
    // URI 是完整 MongoDB Driver 连接字符串。凭证应通过环境变量注入。
    URI string

    // Database 是 Database() 和 Collection() 使用的默认业务数据库。
    Database string

    // TLSCAFile 是需要追加到系统 Root CA Pool 的可选 PEM CA 文件路径。
    // 留空时使用 URI 产生的 TLS 配置或系统 Root CA。
    TLSCAFile string
}
```

不提供伪造地址和数据库名的 `DefaultConfig()`。`URI` 与 `Database` 必须显式填写；默认连接到本机
会把生产配置遗漏静默变成错误目标，比启动失败更危险。

标准配置示例：

```yaml
mongodb:
  uri: ${MONGODB_URI}
  database: game
  tls_ca_file: ${MONGODB_TLS_CA_FILE}
```

`Database` 是 Origin 外观的默认业务数据库，不等同于 URI 的 `authSource`。URI 中的路径数据库不会
替代该字段，避免认证数据库和业务数据库含义混淆。

### 5.2 URI 是连接参数的唯一普通配置来源

以下参数不在 Config 中重复声明，统一放入 URI：

- `appName`、`timeoutMS`、`connectTimeoutMS`、`serverSelectionTimeoutMS`；
- `minPoolSize`、`maxPoolSize`、`maxConnecting`、`maxIdleTimeMS`；
- `replicaSet`、`directConnection`、`readPreference`；
- `retryReads`、`retryWrites`、Write Concern；
- `tls`、认证机制、压缩和服务商专属连接选项。

这样可以避免“URI 与结构化字段谁覆盖谁”的普通配置歧义，也允许服务商发布的新连接参数直接交给
官方 Driver 解析。教程必须解释高频参数、官方默认值和调整依据，但不在框架中复制字段。

Option 合并顺序固定为：

```text
ApplyURI -> 按传入顺序合并 WithDriverOptions -> 应用 WithTLSConfig 或 TLSCAFile
```

后传入的高级 Driver Options 按官方合并语义覆盖前值。TLS 的专用 Option 最后应用；普通使用者不应
通过 Driver Options 重复配置 URI 中已有的常规连接池和超时字段。

### 5.3 TLS 边界

TLS 采用以下规则：

1. `tls=true` 属于连接语义，推荐放入 URI；`mongodb+srv` 按官方 Driver 规则默认启用 TLS；
2. `TLSCAFile` 属于当前主机文件系统配置，放在 Config，推荐通过环境变量提供绝对路径；
3. `TLSCAFile` 非空时读取 PEM，追加到系统 Root CA Pool，并通过原生 `tls.Config` 注入 Driver；
4. 系统 Root CA 无法取得时创建空 Pool，再追加指定 CA；PEM 不含有效证书时启动失败；
5. `TLSCAFile` 和 `WithTLSConfig` 都会启用 TLS；URI 显式 `tls=false` 与任意一种冲突时返回
   `CodeInvalidConfig`；
6. URI 自带 `tlsCAFile` 时仍交给官方 Driver 处理，但不得再同时使用 Config `TLSCAFile` 或
   `WithTLSConfig`；
7. Config `TLSCAFile`、`WithTLSConfig`、URI 中的 CA/客户端证书/私钥文件是三种 TLS 材料来源，
   一次连接只能选择其中一种；`tls=true` 或 `mongodb+srv` 的 TLS 开关不算重复材料；
8. `WithTLSConfig` 与 Config `TLSCAFile` 互斥，传入配置由模块克隆后持有；
9. 合并后的 TLS 配置只要形成 `InsecureSkipVerify=true` 就一律拒绝，包括 URI 的
   `tlsInsecure`/无效证书或主机名兼容别名，以及高级 Option 的等效设置；
10. X.509 客户端证书、私钥和特殊 TLS 策略使用一份完整的 `WithTLSConfig`，或完全使用官方 Driver
    支持的 URI TLS 文件选项，不扩大普通 YAML 字段，也不把两种方式混用。

CA 公钥本身不是密码，但 URI、用户名、密码、Token、私钥内容不得写入日志、错误、测试文件或验收
报告。

## 6. 标准 MongoDB 与兼容服务

### 6.1 支持定义

本模块承诺的是“使用官方 Go Driver 连接符合相应 MongoDB Wire Protocol 的服务”，不是“所有
MongoDB-compatible 服务具备完整 MongoDB 功能”。公共能力分成三层：

1. Client 生命周期、Ping、Database、Collection 和基础 CRUD 走共同 Driver 外观；
2. Session、事务及高级索引由目标服务和引擎版本决定；
3. 未包装能力通过官方 `Client`、`Database` 和 `Collection` 使用并返回原始服务端错误。

不增加 `provider: mongodb/documentdb/cosmos` 枚举，也不按域名猜测服务商。相同服务商的不同引擎
版本和账号 Capability 可能不同；硬编码矩阵会过期并错误拒绝新能力。教程提供连接示例和已知限制，
运行时以服务端响应为准。

### 6.2 标准 MongoDB URI

教程至少提供本地、Replica Set 和 Atlas 三类 URI。示意如下，真实凭证使用环境变量：

```text
mongodb://user:password@host1:27017,host2:27017/
?replicaSet=rs0
&retryWrites=true
&maxPoolSize=100
&maxConnecting=2
&connectTimeoutMS=10000
&serverSelectionTimeoutMS=10000
&timeoutMS=10000
```

Atlas `mongodb+srv` 使用服务商生成的 URI；教程说明 SRV 默认 TLS、Stable API 的适用范围和系统
Root CA 用法，不要求额外 `TLSCAFile`。

### 6.3 AWS DocumentDB URI

教程必须给出 AWS DocumentDB 示例：

```text
mongodb://user:password@cluster.docdb.amazonaws.com:27017/
?tls=true
&replicaSet=rs0
&readPreference=secondaryPreferred
&retryWrites=false
&maxPoolSize=100
&connectTimeoutMS=10000
&serverSelectionTimeoutMS=10000
&timeoutMS=10000
```

CA 文件通过 `tls_ca_file` 提供。AWS DocumentDB 不支持 retryable writes，因此 URI 必须显式
`retryWrites=false`；通过集群 Endpoint 正常连接时推荐 `replicaSet=rs0`，但 SSH Tunnel 场景不能
机械套用该参数。教程必须链接 AWS 当前官方说明，而不是把示意 URI 当成所有部署方式通用模板。

DocumentDB 的 Session、事务、索引、聚合和压缩支持随引擎版本变化。特别说明：DocumentDB 支持
Session 用于事务，但不支持因果一致性或 retryable writes；事务内插入需要集合已经存在。模块不模拟
缺失功能，也不吞掉不支持命令的服务端错误。

### 6.4 其他兼容服务

教程说明 Azure DocumentDB/Cosmos MongoDB API 等服务应以控制台生成的 URI 和对应 API 版本能力表
为准。示例可引用其 `mongodb+srv`、TLS、SCRAM、`retryWrites=false` 和 `maxIdleTimeMS` 配置，但不
声称完成服务商认证。基础兼容性只有在真实环境 Smoke Test 通过后才能写入验收报告。

## 7. 生命周期与所有权

### 7.1 OnInit

- 验证 `New` 或 `Setup` 已完成；
- 验证 URI、默认数据库、Option 组合和 TLS 冲突；
- 不建立网络连接，不启动后台 goroutine；
- 不自动创建集合、索引或迁移数据。

### 7.2 OnStart

- 使用冻结的 ClientOptions 创建唯一官方 `mongo.Client`；
- 使用框架传入的启动 Context 执行一次 `Ping`；
- Ping 失败时释放已经创建的 Client 并使 Module/Service 启动失败；
- 不使用 `context.Background()` 绕过启动取消；
- 不添加模块自有重连、定时 Ping 或健康 goroutine。

官方 Driver 的 Connect 构造、拓扑监控和 Ping 语义必须按实施时的最新稳定 API 复核，不能照抄 v1
的 `NewClient/Connect` 调用。

### 7.3 OnStop

- 使用框架传入的停止 Context 调用 `Client.Disconnect`；
- 不因某个关闭错误跳过状态清理；
- 重复停止安全收敛；
- 停止后不接受新的便利层操作；
- 不创建不受框架 StopTimeout 约束的后台清理。

每个 Module 独占自己的 Client。Client 可以被该 Module 的多个 Await Worker 并发调用，但 Module
对象在绑定 Service 后不得复制，也不得被多个 Service 共享。

## 8. 数据访问外观

公开访问方法固定为：

```go
func (module *Module) Client() *mongo.Client

func (module *Module) Database() *mongo.Database

func (module *Module) Collection(name string) *mongo.Collection

func (module *Module) Ping(ctx context.Context) error
```

语义如下：

- `Client()` 服务于高级能力和同一集群的其他数据库；
- `Database()` 返回 Config 中的默认数据库；
- `Collection(name)` 返回默认数据库集合 Handle；
- Module 尚未启动或已经停止时，三个 Handle 查询返回 `nil`；
- `Ping` 在未运行、Context 为空或对象无效时返回稳定参数/生命周期错误；
- `Collection` 不建立动态缓存 Map；官方 Handle 足够轻量且可并发使用；
- 业务回调只在全部 Module 启动完成后开放，正常业务路径不需要反复检查 nil。

CRUD 不重复包装，统一使用官方链式风格：

```go
module.Collection("players").InsertOne(ctx, player)
module.Collection("players").UpdateOne(ctx, filter, update)
module.Collection("players").FindOne(ctx, filter).Decode(&player)
module.Collection("players").DeleteOne(ctx, filter)
module.Collection("players").CountDocuments(ctx, filter)
```

不提供 `FindOne`、`Exists`、`InsertOne`、`UpdateOne` 等 Module 同名转发方法。它们只减少一次
`Collection` 调用，却扩大长期同步官方 API 的维护面。

## 9. 便利层

便利层只包装确实增加安全性、兼容性或资源释放保证的能力。

全部便利方法先验证 Module 处于运行状态和 Context 非空；集合名、字段名或回调等必需参数为空时返回
稳定参数错误。可变参数中的 `nil` Option 视为调用错误。`EnsureIndexes` 未传任何 IndexModel 时作为
无操作成功并返回空名称列表，不访问服务端。

### 9.1 普通索引

```go
func (module *Module) EnsureIndex(
    ctx context.Context,
    collection string,
    keys bson.D,
    options ...mongooptions.Lister[mongooptions.IndexOptions],
) (string, error)
```

`keys` 必须是非空有序 `bson.D`；方向和类型直接使用官方值，支持复合、text、hashed 等服务端接受
的表达方式。Module 不重建 `[][]string + asc + sparse` 旧模型。

### 9.2 唯一索引

```go
func (module *Module) EnsureUniqueIndex(
    ctx context.Context,
    collection string,
    keys bson.D,
    options ...mongooptions.Lister[mongooptions.IndexOptions],
) (string, error)
```

调用方 Options 先合并，`Unique=true` 最后强制应用；即使调用方传入 `SetUnique(false)`，方法名称与
结果也不会矛盾。

### 9.3 TTL 索引

```go
func (module *Module) EnsureTTLIndex(
    ctx context.Context,
    collection string,
    field string,
    expireAfter time.Duration,
    options ...mongooptions.Lister[mongooptions.IndexOptions],
) (string, error)
```

TTL Helper 固定创建单字段升序索引，并最后应用 `ExpireAfterSeconds`。约束如下：

- 字段名非空；
- `expireAfter` 不得为负数，必须能无损表达为整秒并落入 Driver 支持范围；
- `0` 合法，表示按文档日期字段到期；
- 调用方 Options 不能覆盖 Helper 的 TTL 秒数；
- TTL 删除由服务端后台异步执行，不承诺精确删除时刻，不能替代 Origin Timer；
- 兼容服务的 TTL 字段、数量和 Capability 限制由服务端决定。

### 9.4 批量确保索引

```go
func (module *Module) EnsureIndexes(
    ctx context.Context,
    collection string,
    indexes ...mongo.IndexModel,
) ([]string, error)
```

为兼容 DocumentDB 同一集合单索引构建限制，按输入顺序逐个调用 `CreateOne`，不并行构建。失败时
立即停止并返回已经成功的名称和当前错误。冷启动索引属于低频路径，兼容性和确定性优先于一次
`CreateMany` 往返优化。

`Ensure` 的准确含义是：相同定义已经存在时成功；同名但定义冲突时返回服务端错误。Module 不自动
Drop、隐藏、重建或修改生产索引，也不回滚先前成功的索引。

### 9.5 Session

```go
func (module *Module) WithSession(
    ctx context.Context,
    fn func(context.Context) error,
    options ...mongooptions.Lister[mongooptions.SessionOptions],
) error
```

Module 创建真实官方 Session，调用回调并保证 EndSession。业务必须把回调收到的 Context 传给全部
Session 操作；Session 不得逃逸，不得被多个 goroutine 并发使用。目标服务不支持 Session 时保留
官方/服务端错误。

### 9.6 事务

```go
func (module *Module) WithTransaction(
    ctx context.Context,
    fn func(context.Context) error,
    options ...mongooptions.Lister[mongooptions.TransactionOptions],
) error
```

Module 负责 Session 创建、事务执行和 Session 释放。事务回调必须遵守：

1. 使用回调 Context；
2. 顺序执行，不启动并行事务操作；
3. 返回全部数据库错误，不能吞掉可能表示服务端已中止事务的错误；
4. 可以被官方 Driver 因瞬时事务错误重复执行，因此必须幂等；
5. 不在回调中发送 RPC、消息、HTTP、日志型外部副作用或修改 Service 可变状态；
6. 事务结果通过局部变量带出，`Await` 返回 Service 工作协程后再提交内存状态；
7. 手动 Commit/Abort、因果一致性等高级流程直接使用 `Client` 官方 API。

## 10. Context、协程与 Await

MongoDB Module 不创建异步 CRUD API，也不自动决定是否调用 `Await`。全部 Driver、索引、Session 和
事务方法在调用方 goroutine 同步执行；I/O 会阻塞该 goroutine。

Service 业务路径固定使用：

```go
var player Player
err := module.Await(ctx, func(waitCtx context.Context) error {
    return module.Collection("players").
        FindOne(waitCtx, bson.D{{"_id", playerID}}).
        Decode(&player)
})
if err != nil {
    return err
}

// Await 返回后已经回到 Service 串行工作协程，可以提交业务状态。
module.players[player.ID] = player
```

执行位置固定如下：

| 调用/回调 | 执行位置 |
| --- | --- |
| `Client/Database/Collection` | 当前调用 goroutine，不执行 I/O |
| CRUD、`Ping`、索引方法 | 当前调用 goroutine，同步等待 I/O |
| `WithSession` 回调 | 当前调用 goroutine；在 Await 中即 Await Worker |
| `WithTransaction` 回调 | 当前调用 goroutine；Driver 可能在同一调用内重试 |
| `Await` 回调 | Origin Await Worker |
| `Await` 完成后的业务代码 | Service 串行工作协程 |

不提供 `GetDefaultContext()`。所有操作必须继承调用方 Context。URI 的 `timeoutMS` 只在操作 Context
没有更早 Deadline 时提供 Driver 兜底；`Await`、请求或业务 Context 更早取消时以其为准。

## 11. 错误与安全语义

错误分层如下：

- Config、TLS 冲突、空参数和非法生命周期使用 Origin `errs` 稳定错误码；
- Connect、Ping、CRUD、索引、Session 和事务保留官方 MongoDB 错误链；
- 使用者继续通过 `errors.Is(err, mongo.ErrNoDocuments)`、`mongo.IsDuplicateKeyError(err)` 和官方错误
  Label 判断；
- 不把全部 Driver 错误转换成 Origin 通用错误，以免丢失 Duplicate Key、Write Concern、事务 Label
  和服务端 Code；
- 启动错误补充安全的阶段信息，但不得包含完整 URI、凭证或证书内容；
- URI 解析、TLS 文件读取和 Connect/Ping 的错误路径都必须有凭证脱敏测试；
- `EnsureIndexes` 明确返回部分成功名称，调用者不得把非空错误误认为整体成功。

Module 不增加业务重试。官方 retryable reads/writes 和事务重试按 URI、目标服务与 Driver 规则工作；
业务需要额外重试时必须结合幂等键、唯一约束和有界退避单独设计。

## 12. 原子性边界

教程和 Example 必须准确区分：

| 操作 | 原子性 |
| --- | --- |
| `InsertOne`、单文档 `UpdateOne`、`FindOneAndUpdate`、`DeleteOne` | 单文档原子 |
| 带条件的 `$inc` | 条件判断与单文档更新整体原子 |
| `UpdateOne(..., Upsert=true)` | 单条 Upsert 原子；并发去重仍必须依靠唯一索引 |
| `FindOneAndUpdate(..., Upsert=true, ReturnDocument=After)` | 插入/更新并返回结果为单文档原子操作 |
| `BulkWrite` | 每个模型按服务端规则原子，整个批次默认不是跨文档事务 |
| 普通多行 `Find` | 单次查询；不承诺跨多次读取的事务快照 |
| `WithTransaction` | 仅在目标服务支持的事务范围内提供跨操作原子性 |
| TTL | 服务端异步删除，不保证精确时刻 |

不得用“先 Find、再 Insert/Update”的两次往返模拟需要原子性的 Upsert。并发 Upsert 的 Filter 字段
必须有唯一索引，否则两个请求仍可能各自插入文档。

## 13. 游戏场景 Example

### 13.1 目录与定位

统一建立一个完整示例：

```text
examples/15-mongodb/01-game-store
```

同一个 Example 同时承担“可运行验收”和“对 MongoDB 不熟悉的使用者教程”。核心业务类型为：

```go
type GameMongoModule struct {
    mongodbmodule.Module
}
```

所有集合名、BSON、索引和游戏存取方法放在该 Module；Service 只通过业务方法调用，并在方法外层正确
使用 `Await`。示例不得把数据库逻辑散落到 Service 或 `main`。

### 13.2 必须覆盖的场景

| 场景 | 演示重点 |
| --- | --- |
| Schema 初始化 | 账号联合唯一索引、排行榜复合索引、邮件 TTL 索引 |
| 创建玩家 | `InsertOne`、ObjectID/业务 ID、Duplicate Key 判断 |
| 查询玩家 | `FindOne().Decode()`、`ErrNoDocuments` |
| 普通保存 | `$set` 与 `UpdateOne`，不覆盖不相关字段 |
| 不存在插入、存在更新 | `UpdateOne + SetUpsert(true)` |
| 不存在插入并返回、存在更新并返回 | `FindOneAndUpdate + Upsert + ReturnDocument(After)` |
| 条件扣金币 | Filter 包含余额下限并使用 `$inc`，通过 MatchedCount 判断失败 |
| 乐观锁保存 | Filter 携带 `version`，更新同时 `$inc version` |
| 多行查询 | Filter、Projection、稳定 Sort、Limit、Cursor Close 和 Err |
| 排行榜/最近邮件 | 与查询条件和排序一致的复合索引，限制最大结果数 |
| 批量保存 | `BulkWrite`、Ordered 选择及“批次非事务”说明 |
| 奖励防重 | `player_id + event_id` 唯一索引和 Duplicate Key 幂等处理 |
| 双玩家转账 | `WithTransaction`、回调幂等和跨文档回滚 |
| 数据删除 | `DeleteOne/DeleteMany` 的明确使用场景和 Filter 安全检查 |

### 13.3 Upsert 示例约束

场景一使用：

```go
module.Collection("players").UpdateOne(
    ctx,
    bson.D{
        {"server_id", serverID},
        {"account_id", accountID},
    },
    bson.D{
        {"$set", bson.D{
            {"nickname", nickname},
            {"last_login_at", now},
        }},
        {"$setOnInsert", bson.D{
            {"created_at", now},
            {"gold", 0},
            {"version", 1},
        }},
    },
    mongooptions.UpdateOne().SetUpsert(true),
)
```

`server_id + account_id` 必须先建立唯一索引。

场景二使用：

```go
err := module.Collection("players").FindOneAndUpdate(
    ctx,
    filter,
    update,
    mongooptions.FindOneAndUpdate().
        SetUpsert(true).
        SetReturnDocument(mongooptions.After),
).Decode(&player)
```

该操作一次返回插入或更新后的文档，不再额外 Select。Example 注释必须说明 `$setOnInsert` 只在插入
分支生效，并说明唯一索引是并发去重前提。

### 13.4 条件扣款和乐观锁

扣金币使用同一 Update Filter 同时判断余额：

```go
result, err := module.Collection("players").UpdateOne(
    ctx,
    bson.D{
        {"_id", playerID},
        {"gold", bson.D{{"$gte", cost}}},
    },
    bson.D{{"$inc", bson.D{{"gold", -cost}}}},
)
```

`MatchedCount == 0` 表示玩家不存在或余额不足，不能在更新前单独读取余额。乐观锁同理把 `_id` 和
`version` 同时放入 Filter，并在成功写入时递增 version；零匹配作为冲突返回，不进行无界自动重试。

### 13.5 多行查询

多行示例必须：

- 使用业务硬上限，不能无界 `Find(...).All()`；
- 排序最后加入 `_id`，在相同业务排序值下保持稳定；
- 正确 `defer cursor.Close(ctx)` 并在结束后检查 `cursor.Err()`；
- 展示 Projection，避免读取不需要的大字段；
- 大数据翻页使用稳定排序键范围查询，不推荐大偏移量 `Skip`；
- 说明普通查询不是跨多次操作的事务快照。

### 13.6 事务示例

双玩家转账在支持事务的 Replica Set/兼容服务上运行。扣款和入账任一步失败即整体返回错误；事务
回调不发送事件、RPC 或通知。若需要在提交成功后发消息，先完成 `Await`，回到 Service 工作协程后再
发送，并使用业务幂等键处理“数据库已提交但后续消息失败”的独立可靠性问题。

标准集成环境使用单节点 Replica Set，而不是不支持事务的 standalone MongoDB。兼容服务事务范围和
超时以其引擎版本为准；Example 通过独立运行开关控制事务场景，基础 CRUD 不因目标不支持事务而无法
学习或运行。

## 14. 使用教程

新增面向使用者的 v3.2 指南，并在根 README 扩展组件表和 `docs/maintenance/v3.2/README.md` 中加入
MongoDB 行。指南至少包括：

1. Module 适用范围和一个 Module 一个集群；
2. 标准 MongoDB、本地 Replica Set、Atlas、AWS DocumentDB URI；
3. URI 高频参数、默认值和生产调整建议；
4. TLS、系统 CA、`tls_ca_file`、Windows/Ubuntu/容器路径；
5. MongoDB-compatible 服务能力不是完整 MongoDB 的边界；
6. 单集群匿名嵌入和多集群命名子 Module；
7. `Client`、`Database`、`Collection`、`Ping`；
8. 全部便利层函数和参数；
9. 每个方法、回调和 `Await` 的执行 goroutine；
10. BSON、CRUD、Cursor 和官方错误判断；
11. 普通、唯一、TTL、复合索引及兼容性；
12. Upsert、条件更新、乐观锁和原子性；
13. BulkWrite、Session、事务及副作用限制；
14. 连接池、超时、慢查询与生产排障；
15. `01-game-store` 的运行方式和逐段说明。

教程语言保持简洁，首次出现的 MongoDB 名词给出一句定义；Example 对关键选项补充“为什么”，不只
翻译函数名。URI 示例不得包含真实凭证。

## 15. 测试设计

### 15.1 单元测试

必须覆盖：

- `New/Setup` 正常路径、空 URI/Database、重复 Setup、启动后 Setup；
- 严格配置解码、未知字段、环境变量和原子转换；
- URI 解析失败且错误不泄露用户名、密码和查询 Token；
- `TLSCAFile` 缺失、空 PEM、非法 PEM、有效 PEM、系统 CA 追加；
- URI `tls=false` 冲突、URI/Config/Option TLS 材料重复、Driver Option 二次 URI、
  `InsecureSkipVerify` 及其 URI 等效选项；
- Driver Options 合并顺序和调用方对象不被异步修改；
- 未启动、运行中、停止中和停止后的访问语义；
- 空 Context、空集合名、空索引 Key 和非法 TTL Duration；
- 唯一/TTL 强制 Option 不能被调用方反向覆盖；
- `EnsureIndexes` 顺序执行、部分成功和停止后续；
- 官方错误链、Duplicate Key、`ErrNoDocuments` 和 Context 错误可判断；
- 重复停止、启动失败后的清理和无 goroutine 泄漏。

需要隔离 Driver 的单元分支使用最小内部构造/测试替身，不为测试扩大公共 ClientFactory API。

### 15.2 标准 MongoDB 集成测试

Ubuntu 使用真实单节点 Replica Set，覆盖：

- Connect、Ping、默认 Database/Collection、Disconnect；
- 同一 Service 两个 Module/Client 和多集群配置路径；
- 并发 CRUD 与官方连接池复用；
- Context 取消、Deadline、Server Selection 失败；
- 普通、复合、唯一、TTL 和批量索引；
- Insert、Update、Find、Delete、Count、Cursor；
- 两类 Upsert、条件扣款、乐观锁、Duplicate Key 并发竞争；
- BulkWrite；
- Session 创建/释放；
- 事务提交、回滚、回调错误和 Context 取消；
- Example 完整启动、运行和停止。

真实测试 URI、CA 路径和事务开关通过环境变量传入，不写入仓库。Windows 执行全部不依赖外部服务
的单元测试、构建、Example 编译和 `go vet`；Ubuntu 额外执行真实 MongoDB、`go test -race`、覆盖率
和 Example 运行。

### 15.3 兼容服务 Smoke Test

AWS DocumentDB 或其他服务只有在使用者提供受控测试环境时执行：

- TLS/CA、Ping 和基础 CRUD；
- `retryWrites=false`；
- 普通、唯一和 TTL 索引中该引擎声明支持的部分；
- Session/事务仅在对应引擎版本支持时启用；
- 服务端明确不支持的能力必须保留原始错误，不得误报成功。

没有真实服务商环境时，验收报告只能写“依据官方协议设计、未取得真实环境证据”，不能写成已完成
DocumentDB/Cosmos 兼容认证。

### 15.4 覆盖率与质量门禁

`mongodbmodule` 是重点新包：可稳定触发的公共行为分支尽量达到 100% 覆盖。不能在普通环境触发的
网络、证书或服务商分支必须逐项记录真实集成证据或未覆盖原因，不能只报告总覆盖率。

统一验收至少包括：

```text
gofmt
go vet ./...
go test ./... -count=1
go test -race ./... -count=1   # Ubuntu
go test ./... -coverprofile=...
go build ./examples/15-mongodb/...
```

具体命令和环境准备在五个模块设计完成后的统一实施计划中冻结。

## 16. 性能原则

- 一个 Module 生命周期只创建一个 Client，绝不按请求创建连接池；
- 使用官方 Driver 内建连接池，不增加 Origin 连接池或内存池；
- CRUD 直接调用官方 Collection，避免反射 Repository 和额外结果复制；
- `EnsureIndexes` 是冷路径，顺序兼容性优先，不为一次启动往返做并行优化；
- 多行读取必须有业务 Limit；大结果使用 Cursor 流式处理；
- 批量写入使用官方 BulkWrite，不自建消息队列；
- 不默认开启 Command Debug Logging，避免负载和数据泄露；
- 连接池大小、空闲时间和超时通过 URI 按压测结果调整；
- 只有 Benchmark/Profile 证明 Wrapper 成为瓶颈后才做专门优化。

## 17. 明确不实现

首批不实现：

- v2 假 Session 和默认后台 Context；
- 全量 CRUD 同名转发；
- ORM、泛型 Repository、自动分页和自动 Schema 映射；
- `NextSeq`、Snowflake、玩家 ID 或业务计数器；
- 自动创建/删除/重建索引和数据库迁移框架；
- 自动识别 AWS/Azure/Atlas 并改写 URI；
- 对不支持的兼容服务命令做本地模拟；
- Module 自有业务重试、断路器、缓存、队列或后台 Ping；
- 自建连接池、BSON 对象池或结果对象池；
- 默认 Stats Command Monitor。生产指标通过 `WithDriverOptions` 注入官方 Monitor，待 Origin 统一
  Metrics 需求明确后再设计固定统计外观；
- GridFS、Change Stream、Client-side Encryption、Search/Vector Search 的 Origin 专属包装。

这些能力仍可通过 `Client()` 使用官方 Driver；只有形成重复、稳定且跨业务一致的需求时，才评估新增
便利层。

## 18. 实施完成条件

MongoDB 切片只有同时满足以下条件才算完成：

1. 官方最新稳定 Driver 固定为直接依赖，且完成版本迁移复核；
2. 本文冻结的公共外观全部实现，无额外兼容别名；
3. 生命周期、Context、TLS、错误脱敏和部分失败语义有测试；
4. Windows 基础验证和 Ubuntu Replica Set/`-race` 验证通过；
5. 重点公共行为分支达到约定覆盖率，例外逐项说明；
6. `01-game-store` 覆盖约定游戏场景并可以运行；
7. 标准 MongoDB、Atlas、AWS DocumentDB URI 和能力边界写入教程；
8. 根 README 和 v3.2 扩展组件教程表完成入口更新；
9. 设计、代码、测试、Example 和教程相互一致；
10. MongoDB 完成后仍不单独开始实现；Redis 设计已经确认，MySQL 暂缓，继续完成 Kafka 和 Blueprint
    设计，最后对本轮已确认范围统一制定实施计划。

## 19. 参考资料

- [MongoDB Go Driver Releases](https://github.com/mongodb/mongo-go-driver/releases)
- [MongoDB Go Driver Connection Options](https://www.mongodb.com/docs/drivers/go/current/connect/specify-connection-options/)
- [MongoDB Go Driver Connection Pools](https://www.mongodb.com/docs/drivers/go/current/connect/connection-options/connection-pools/)
- [MongoDB Go Driver TLS](https://www.mongodb.com/docs/drivers/go/current/security/tls/)
- [MongoDB Go Driver Context](https://www.mongodb.com/docs/drivers/go/current/context/)
- [MongoDB Go Driver Indexes](https://www.mongodb.com/docs/drivers/go/current/indexes/)
- [MongoDB Go Driver Compound Operations](https://www.mongodb.com/docs/drivers/go/current/crud/compound-operations/)
- [MongoDB Go Driver Transactions](https://www.mongodb.com/docs/drivers/go/current/crud/transactions/)
- [AWS DocumentDB Programmatic Connection](https://docs.aws.amazon.com/documentdb/latest/developerguide/connect_programmatically.html)
- [AWS DocumentDB Replica Set Connection](https://docs.aws.amazon.com/documentdb/latest/developerguide/connect-to-replica-set.html)
- [AWS DocumentDB Functional Differences](https://docs.aws.amazon.com/documentdb/latest/developerguide/functional-differences.html)
- [AWS DocumentDB Transactions](https://docs.aws.amazon.com/documentdb/latest/devguide/transactions.html)
- [Azure DocumentDB Go Quickstart](https://learn.microsoft.com/en-us/azure/documentdb/quickstart-go)
- [Azure Cosmos DB MongoDB API Feature Support](https://learn.microsoft.com/en-ca/azure/cosmos-db/mongodb/feature-support-42)
