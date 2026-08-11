# MongoDB Module 使用指南

> 状态：已实现
> 基线：Origin v3.0，目标版本：v3.2
> Driver：`go.mongodb.org/mongo-driver/v2 v2.8.0`

MongoDB Module 管理官方 Client 的创建、启动探活和停止释放，并提供索引、Session、事务这几类容易误用的薄包装。普通 CRUD 直接使用官方 `Collection`，不会受一套 Origin 自定义 CRUD API 限制。

## 1. 十分钟接入

把数据库能力和业务方法集中在一个 Module：

```go
type GameMongoModule struct {
    mongodbmodule.Module
}

func (module *GameMongoModule) OnInit() error {
    var config mongodbmodule.Config
    if err := module.GetServiceConfigStrict("mongodb", &config); err != nil {
        return err
    }
    return module.Setup(config)
}

func (module *GameMongoModule) FindPlayer(
    ctx context.Context,
    playerID string,
) (Player, error) {
    var player Player
    err := module.Collection("players").
        FindOne(ctx, bson.D{{Key: "_id", Value: playerID}}).
        Decode(&player)
    return player, err
}
```

在 Service 初始化时装配一次：

```go
type PlayerService struct {
    service.Service
    mongo *GameMongoModule
}

func (target *PlayerService) OnInit() error {
    target.mongo = &GameMongoModule{}
    return target.AddModule(target.mongo)
}
```

配置：

```yaml
services:
  PlayerService:
    mongodb:
      uri: "${MONGODB_URI}"
      database: "game"
      tls_ca_file: ""
```

凭证放入部署系统的 Secret/环境变量，不要提交到 YAML、日志或测试文件。

## 2. 配置字段

Origin 刻意只保留三个字段。连接池、超时、认证和拓扑全部使用官方 URI 参数，避免出现两套配置互相覆盖。

| 字段 | 必填 | 默认值 | 说明与建议 |
| --- | --- | --- | --- |
| `uri` | 是 | 无 | 完整 `mongodb://` 或 `mongodb+srv://` URI。必须显式配置，防止生产环境误连本机 |
| `database` | 是 | 无 | `Database()` 和 `Collection()` 使用的默认业务库；它不是认证库 `authSource` |
| `tls_ca_file` | 否 | 空 | 需要追加到系统证书池的 PEM CA 文件；Atlas/公共 CA 一般留空，DocumentDB 常需配置 |

模块不会提供“默认连接 localhost”。配置缺失时让 Service 启动失败，比静默连错数据库安全。

### 2.1 高频 URI 参数

下表的“Driver 默认值”对应本版本官方 Driver；“起步建议”是常见游戏服务的初始值，不是所有项目的最优值。最终应根据实例连接上限、Service 副本数、并发 I/O、P95/P99 与压测结果调整。

| URI 参数 | Driver 默认值 | 是否建议显式配置 | 常见起步值与含义 |
| --- | --- | --- | --- |
| `appName` | 空 | 建议 | 服务名或部署名，方便服务端审计和慢查询定位 |
| `replicaSet` | 空 | Replica Set 必填 | 使用服务端实际 Set 名；事务需要 Replica Set 或 Sharded Cluster |
| `authSource` | 按 URI/机制推导 | 有认证时建议 | 常见为 `admin`；不要误写成业务 `database` |
| `connectTimeoutMS` | `30000` | 建议 | 内网常从 `5000`～`10000` 起步；跨地域按网络 SLO 调大 |
| `serverSelectionTimeoutMS` | `30000` | 建议 | 常从 `5000`～`10000` 起步，使故障更快回传；不能短于正常选主窗口太多 |
| `timeoutMS` | 无 | 建议业务使用 Context | Client 全局操作上限；不同业务延迟差异大时优先给每次调用设置 Context deadline |
| `minPoolSize` | `0` | 可选 | `0` 最节省连接；对首请求延迟敏感且连接预算充足时小幅预热，如 `5`～`10` |
| `maxPoolSize` | `100`/每个服务端 | 建议核算 | 先按单副本 DB 并发设置 `50`～`100`；总连接约等于副本数 × 各实例池容量 |
| `maxConnecting` | `2` | 通常不改 | 连接突发明显时再压测调整；官方不建议超过 `100` |
| `maxIdleTimeMS` | `0`（不因空闲关闭） | 云服务建议 | 云负载均衡或兼容服务常从 `60000` 起步，低频服务避免长期保留陈旧连接 |
| `heartbeatFrequencyMS` | `10000` | 通常不改 | 最低 `500`；调小会增加监控流量，不能当业务健康检查频率使用 |
| `retryReads` | `true` | 通常保留 | Driver 对支持的瞬时读错误重试一次；业务 Context 仍需有界 |
| `retryWrites` | `true` | MongoDB 通常保留 | 兼容服务若明确不支持（如部分 DocumentDB 场景）使用 `false` |
| `readPreference` | `primary` | 按一致性选择 | 关键玩家状态保持 `primary`；可容忍延迟的只读分析再考虑 Secondary |
| `compressors` | 空 | 压测后选择 | 大文档/跨地域可能收益明显；小消息会增加 CPU，不因“可能更快”默认开启 |

连接池请求达到 `maxPoolSize` 后会等待可用连接。每个数据库调用都应有 Context deadline，防止排队无限拖长 Service 任务。

## 3. URI 示例

以下只展示结构。真实用户名、密码、主机和 CA 路径必须通过部署配置注入。

本地单节点 Replica Set：

```text
mongodb://127.0.0.1:27017/?replicaSet=rs0&directConnection=true
```

生产 Replica Set：

```text
mongodb://user:password@mongo-0:27017,mongo-1:27017,mongo-2:27017/?replicaSet=rs0&authSource=admin&appName=player-service&connectTimeoutMS=10000&serverSelectionTimeoutMS=10000&maxPoolSize=100&retryReads=true&retryWrites=true
```

MongoDB Atlas：

```text
mongodb+srv://user:password@cluster.example.mongodb.net/?authSource=admin&appName=player-service&retryWrites=true&w=majority
```

AWS DocumentDB 常见结构：

```text
mongodb://user:password@cluster.example.docdb.amazonaws.com:27017/?tls=true&replicaSet=rs0&readPreference=secondaryPreferred&retryWrites=false&maxIdleTimeMS=60000
```

```yaml
mongodb:
  uri: "${DOCUMENTDB_URI}"
  database: "game"
  tls_ca_file: "${DOCUMENTDB_CA_FILE}"
```

DocumentDB 的索引、Session、事务和命令兼容性取决于具体引擎版本。Origin 只保证使用官方 Driver 建立连接并透传服务端结果，不把“Wire Protocol 可连接”误写为“完整兼容 MongoDB”。Azure Cosmos DB for MongoDB、DocumentDB 等其他兼容服务同样应以服务商控制台 URI 和当前能力表为准。

## 4. TLS 与安全边界

选择且只选择一种 TLS 材料来源：

1. URI 中的 `tlsCAFile`/`tlsCertificateKeyFile`；
2. Config 的 `tls_ca_file`；
3. 代码中的 `WithTLSConfig`。

`tls=true` 只是启用 TLS，不算第二份材料。Module 会拒绝以下配置：

- `tls=false` 与 `tls_ca_file`/`WithTLSConfig` 同时出现；
- 多种 CA/证书来源混用；
- `InsecureSkipVerify`、`tlsInsecure=true`、允许无效证书或主机名的别名；
- `WithDriverOptions` 再次 `ApplyURI`、设置 Hosts 或 TLSConfig。

私有 CA 文件会追加到系统 Root CA Pool，不会替换系统公共根。X.509 客户端证书、私钥或特殊回调应构造完整 `tls.Config` 后通过 `WithTLSConfig` 传入：

```go
module, err := mongodbmodule.New(
    config,
    mongodbmodule.WithTLSConfig(tlsConfig),
)
```

Module 会克隆 TLSConfig；调用方不应持有或修改 Module 内部结果。

## 5. 单集群与多集群

一个 Module 固定拥有一个 Client、一个集群和一个默认数据库。大多数服务只组合一个：

```go
type GameMongoModule struct {
    mongodbmodule.Module
}
```

需要游戏库和日志库两个集群时，使用命名字段分别装配，不要在一个 Module 内维护动态 Map：

```go
type StorageModule struct {
    service.Module
    game *mongodbmodule.Module
    log  *mongodbmodule.Module
}

func (module *StorageModule) OnInit() error {
    var config struct {
        Game mongodbmodule.Config `yaml:"game"`
        Log  mongodbmodule.Config `yaml:"log"`
    }
    if err := module.GetServiceConfigStrict("mongodb", &config); err != nil {
        return err
    }
    var err error
    module.game, err = mongodbmodule.New(config.Game)
    if err != nil {
        return err
    }
    module.log, err = mongodbmodule.New(config.Log)
    if err != nil {
        return err
    }
    if err := module.AddModule(module.game); err != nil {
        return err
    }
    return module.AddModule(module.log)
}
```

每个 Client 独立启动、Ping、停止和报告错误，单个集群失败不会留下不清楚的部分状态。

## 6. 三层使用外观

### 6.1 第一层：官方 Collection，处理普通 CRUD

```go
collection := module.Collection("players")

_, err := collection.InsertOne(ctx, player)
_, err = collection.UpdateOne(ctx, filter, update)
err = collection.FindOne(ctx, filter).Decode(&player)
_, err = collection.DeleteOne(ctx, filter)
count, err := collection.CountDocuments(ctx, filter)
```

`Collection` 是轻量 Handle，官方 Driver 支持并发使用，不需要再放入动态缓存。Module 未启动或停止后返回 nil；正常业务回调只会在全部 Module 启动成功后开放。

`Client()` 用于 change stream、command、其他数据库等未包装能力；`Database()` 返回默认数据库。调用方不能 Disconnect `Client()`，所有权始终属于 Module。

### 6.2 第二层：Module 便利方法，处理安全边界

索引：

```go
_, err := module.EnsureUniqueIndex(
    ctx,
    "players",
    bson.D{{Key: "server_id", Value: 1}, {Key: "name", Value: 1}},
    options.Index().SetName("server_player_name"),
)

_, err = module.EnsureTTLIndex(
    ctx,
    "login_sessions",
    "expire_at",
    0, // 文档在 expire_at 指定的绝对时间之后由服务端异步清理。
)
```

索引键必须使用 `bson.D`，因为复合索引字段顺序有语义。`EnsureUniqueIndex` 最后强制 `Unique=true`；`EnsureTTLIndex` 最后强制 TTL 秒数。`EnsureIndexes` 为兼容 DocumentDB 按顺序调用 CreateOne，失败时返回已经成功的名称，不自动删除或回滚生产索引。

Session：

```go
err := module.WithSession(ctx, func(sessionCtx context.Context) error {
    return module.Collection("players").
        FindOne(sessionCtx, filter).
        Decode(&player)
})
```

事务：

```go
err := module.WithTransaction(ctx, func(transactionCtx context.Context) error {
    if _, err := players.UpdateOne(transactionCtx, debitFilter, debit); err != nil {
        return err
    }
    _, err := players.UpdateOne(transactionCtx, creditFilter, credit)
    return err
})
```

事务回调可能被官方 Driver 重试，必须幂等。回调内不要发 RPC、Kafka、HTTP、邮件，也不要直接修改不可回滚的 Service 内存状态。跨 MongoDB 和其他系统的一致性使用 Outbox/Inbox 等业务方案。

### 6.3 第三层：Origin Await，避免阻塞 Service

所有 MongoDB I/O 都在调用 goroutine 同步等待。Service 工作协程发起数据库请求时，用 `Await` 暂时释放 Service 执行权：

```go
var player Player
err := module.Await(ctx, func(waitCtx context.Context) error {
    return module.Collection("players").
        FindOne(waitCtx, bson.D{{Key: "_id", Value: playerID}}).
        Decode(&player)
})
if err != nil {
    return err
}

// Await 返回后恢复 Service 串行执行权，此时再提交内存业务状态。
module.players[player.ID] = player
```

若当前调用本来就在独立 I/O goroutine，且不读取或修改 Service 串行状态，可以直接调用 Driver，不需要为了形式再套一层 Await。

## 7. Context 到底做什么

每个 I/O 方法的 `ctx` 同时承担四项职责：

- 截止时间：限制连接池排队、选服、网络和服务端操作的总等待；
- 取消：客户端断开、RPC 取消或 Service 停止时尽快终止工作；
- Session/事务：Driver 通过回调 Context 关联当前 Session；
- 链路元数据：调用方可以携带 trace 等只读请求信息。

不要省略 Context，也不要在业务请求中随意改用 `context.Background()`。调用方已有 deadline 时直接传递；没有时按业务 SLO 创建：

```go
dbCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
defer cancel()

err := module.Collection("players").FindOne(dbCtx, filter).Decode(&player)
```

事务和 Session 内必须使用回调收到的 Context，不要继续使用外层 Context。

## 8. 函数与回调在哪个协程执行

| 调用或回调 | 执行位置 | 是否阻塞当前 goroutine |
| --- | --- | --- |
| `New`、`Setup`、`Client`、`Database`、`Collection` | 当前调用 goroutine | 不执行网络 I/O |
| `OnStart` 的 Connect/Ping | Origin 生命周期 goroutine | 是，受 Start Context 约束 |
| `OnStop` 的 Disconnect | Origin 生命周期 goroutine | 是，受 Stop Context 约束 |
| CRUD、`Ping`、索引方法 | 当前调用 goroutine | 是 |
| `WithSession` 回调 | 当前调用 goroutine | 是 |
| `WithTransaction` 回调 | 当前调用 goroutine；Driver 可在同一次调用中重试 | 是 |
| `Await` 的 I/O 回调 | 原 Service Task goroutine，但已释放 Service 执行权 | 是，但不阻塞其他 Service Task |
| `Await` 返回后的代码 | 恢复 Service 串行执行权 | 按普通 Service 规则 |

Module 不为 CRUD 创建 goroutine，也不添加定时 Ping 或自定义重连循环。连接池和拓扑监控由官方 Driver 管理。

## 9. 游戏业务高频写法

完整可运行代码见 [01-game-store](../../../../examples/15-mongodb/01-game-store/README.md)。其中包含：

| 场景 | 推荐原语 | 关键注意事项 |
| --- | --- | --- |
| 不存在插入，否则更新 | `UpdateOne + SetUpsert(true)` | 不需要返回文档时少一次读取 |
| 不存在插入，否则更新并返回 | `FindOneAndUpdate + Upsert + ReturnDocument(After)` | 单文档原子返回更新后值 |
| 扣金币/体力 | 条件 Filter + `$inc` | 不要先查余额再更新 |
| 乐观锁 | Filter 带 `version` + `$inc version` | `ModifiedCount==0` 表示冲突或不存在 |
| 排行/多行查询 | 复合索引 + 稳定 Sort + Limit | 排序末尾加入唯一 `_id`，禁止无界 `Find` |
| 多个独立写 | `BulkWrite` | 减少网络往返，但 BulkWrite 本身不是事务 |
| 幂等奖励 | 唯一业务 ID + 事务 | 重复消息不能重复发放 |
| 玩家转账 | `WithTransaction` | 回调可重试，不执行外部副作用 |
| 删除玩家数据 | `DeleteOne` + 精确主键/租户条件 | 禁止空 Filter 和宽泛 `DeleteMany` |

金额、计数、版本号优先使用 `int64`；不要用 `float64` 保存金币等要求精确比较的数据。

## 10. 错误处理

```go
switch {
case errors.Is(err, mongo.ErrNoDocuments):
    // 业务不存在。
case mongo.IsDuplicateKeyError(err):
    // 唯一键冲突或幂等记录已经存在。
case errors.Is(err, context.DeadlineExceeded):
    // 本次业务预算耗尽；不要在 Service 协程内无界重试。
case errors.Is(err, context.Canceled):
    // 上游取消或服务停止。
case err != nil:
    // 保留原始 Driver 错误链供日志和指标分类。
}
```

配置错误会返回脱敏的 Origin 稳定错误，不包含完整 URI。运行时 Driver/服务端错误原样保留错误链，业务日志仍不应附加请求文档、密码或 Token。

## 11. 性能与稳定性

- 一个 Module 生命周期只创建一个 Client；不要按请求创建/关闭 Client。
- `maxPoolSize` 是每个服务端的池上限，不是整个部署的总连接数。
- 慢查询先检查索引和 `explain`，不要先盲目调大连接池。
- 所有列表查询必须有业务上限和稳定排序；大结果使用 Cursor 分批处理，不一次读入内存。
- BulkWrite 用于减少往返；需要跨文档原子性时才使用事务，避免把所有写操作都放入事务。
- Context deadline 应覆盖连接池排队；只配置 socket/connect timeout 不能替代业务总预算。
- 不在 Module 增加对象池、CRUD 缓存或自有重试。BSON 编解码和连接复用交给官方 Driver，业务重试必须有次数/时间上限并验证幂等性。

## 12. 常见问题

### 启动 Ping 超时

检查 URI 主机、DNS、TLS CA、Replica Set 名、账号权限和 `serverSelectionTimeoutMS`。Module 在 Ping 失败后会 Disconnect 已创建 Client，不留下半启动状态。

### 事务提示不支持

确认服务端是 Replica Set/Sharded Cluster，且兼容服务的当前版本明确支持事务。单机 standalone 不支持事务；不要在模块中静默降级为非事务执行。

### 唯一索引创建失败

先检查存量重复数据和同名不同定义的索引。`EnsureUniqueIndex` 不会自动清理数据、Drop 或重建生产索引。

### TTL 文档没有准时删除

TTL 由服务端后台任务异步清理，不保证精确时刻。需要准点触发游戏逻辑时使用 Origin Timer/业务任务，TTL 只负责最终数据回收。

### Service 处理其他消息变慢

确认慢 MongoDB 操作是否直接阻塞了 Service 工作协程。需要等待 I/O 时使用 `Await`，并为每次调用设置 deadline；同时检查连接池排队和服务端慢查询。

## 13. API 速查

| API | 参数 | 返回 | 用途 |
| --- | --- | --- | --- |
| `New(config, options...)` | 配置与高级 Option | `*Module, error` | 构造后交给 `AddModule` |
| `Setup(config, options...)` | 配置与高级 Option | `error` | 匿名嵌入业务 Module 时在 `OnInit` 调用一次 |
| `Client()` | 无 | `*mongo.Client` | 未包装高级能力；调用方不得 Disconnect |
| `Database()` | 无 | `*mongo.Database` | 默认业务库 |
| `Collection(name)` | 非空集合名 | `*mongo.Collection` | 普通 CRUD |
| `Ping(ctx)` | 非空 Context | `error` | 按调用预算主动探活 |
| `EnsureIndex(ctx, collection, keys, options...)` | 有序非空 `bson.D` | 索引名、错误 | 普通索引 |
| `EnsureUniqueIndex(...)` | 同上 | 索引名、错误 | 强制唯一索引 |
| `EnsureTTLIndex(ctx, collection, field, duration, options...)` | 非负整秒 TTL | 索引名、错误 | 单字段 TTL 索引 |
| `EnsureIndexes(ctx, collection, models...)` | 有序 IndexModel 列表 | 已成功名称、错误 | 兼容优先的顺序批量确保 |
| `WithSession(ctx, fn, options...)` | Session 回调 | `error` | 自动创建和释放 Session |
| `WithTransaction(ctx, fn, options...)` | 可重试事务回调 | `error` | 自动管理事务和 Session |

完整实现与全部场景以 [01-game-store 源码](../../../../examples/15-mongodb/01-game-store/main.go) 为准。
