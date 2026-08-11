# Redis Module 使用指南

> 状态：已实现
> 目标版本：Origin v3.2
> Driver：`github.com/redis/go-redis/v9 v9.22.0`，分布式锁：`github.com/bsm/redislock v0.10.0`

Redis Module 管理 Standalone、Sentinel 和 Cluster 的连接、探活与释放，并提供游戏服务常用的基础命令、
Pipeline、事务、Lua 和有界 Lease Lock。它不包装业务 Key、JSON/PB、缓存回源、可靠队列或复合排行；这些规则应留在业务 Module。

## 1. 快速接入

把 Redis 能力和业务方法组合在同一个 Module，避免 Service 散落 Redis Key 和序列化细节：

```go
type PlayerCacheModule struct {
    redismodule.Module
}

func (module *PlayerCacheModule) OnInit() error {
    var current redismodule.Config
    if err := module.GetServiceConfigStrict("redis", &current); err != nil {
        return err
    }
    return module.Setup(current)
}

func (module *PlayerCacheModule) LoadName(ctx context.Context, playerID string) (string, error) {
    return module.Get(ctx, "player:{"+playerID+"}:name")
}
```

在所属 Service 中注册一次：

```go
type PlayerService struct {
    service.Service
    cache *PlayerCacheModule
}

func (target *PlayerService) OnInit() error {
    target.cache = &PlayerCacheModule{}
    return target.AddModule(target.cache)
}
```

最小 Standalone 配置：

```yaml
services:
  PlayerService:
    redis:
      mode: standalone
      addresses:
        - "127.0.0.1:6379"
      client_name: "player-service"
      pool_size: 32
      max_active_connections: 32
```

凭证应由 Secret 或环境变量注入，不要提交到 YAML、日志或测试文件。

## 2. 配置字段

表中的建议值是常见游戏服务的起步值，不是固定答案。连接数应按“副本数 × 每个 Module × 每节点池容量”核算，
再结合 Redis 最大连接数、命令 P95/P99、池等待和压测结果调整。

| 字段 | 必填 | 默认值 | 说明与生产起步建议 |
| --- | --- | --- | --- |
| `mode` | 否 | `standalone` | `standalone`、`sentinel`、`cluster` |
| `addresses` | 是 | 无 | `host:port` 列表；Standalone 只能一个，Sentinel/Cluster 建议至少三个入口 |
| `username` | 否 | 空 | Redis 数据节点 ACL 用户 |
| `password` | 否 | 空 | Redis 数据节点密码；由 Secret 注入 |
| `database` | 否 | `0` | Standalone/Sentinel 可选；Cluster 必须为 `0` |
| `client_name` | 否 | 空 | 建议使用服务/部署名，便于 `CLIENT LIST` 定位连接 |
| `protocol` | 否 | `3` | RESP 2 或 3；旧代理不兼容 RESP3 时再改为 2 |
| `tls` | 否 | `false` | 跨不可信网络或托管 Redis 建议开启 |
| `tls_ca_file` | 否 | 系统 CA | 私有 CA 的 PEM 文件；仅在 `tls: true` 时配置 |
| `dial_timeout` | 否 | `5s` | 内网常从 `2s`～`5s` 起步，必须覆盖正常 DNS/TLS 建连 |
| `dial_attempts` | 否 | `5` | 一次取连接包含首次在内的建连次数；启动恢复可保留默认 |
| `dial_retry_interval` | 否 | `100ms` | 建连重试固定间隔 |
| `read_timeout` | 否 | `5s` | 单次网络读取兜底；长阻塞命令应自行使用更明确的 Context |
| `write_timeout` | 否 | `5s` | 单次网络写入兜底 |
| `pool_timeout` | 否 | `6s` | 池满等待上限；低延迟在线服常从 `500ms`～`2s` 起步并监控超时 |
| `pool_size` | 否 | Standalone/Sentinel=`10×GOMAXPROCS`；Cluster=`5×GOMAXPROCS`/节点 | 建议显式核算，在线服常从每节点 `16`～`64` 起步 |
| `max_concurrent_dials` | 否 | `pool_size` | 每节点并发建连上限，不能大于 `pool_size` |
| `max_active_connections` | 否 | `pool_size` | 每节点硬上限；必须不小于 `pool_size`，用于防止连接失控 |
| `min_idle_connections` | 否 | `0` | 对首请求敏感时可从 `2`～`8` 预热；大量副本需核算空闲连接 |
| `connection_max_idle_time` | 否 | `30m` | 云负载均衡回收空闲连接时应小于其空闲超时 |
| `max_retries` | 否 | `0` | 默认禁用命令重试；仅确认命令幂等且接受延迟放大后开启；Cluster 禁止配置 |
| `min_retry_backoff` | 否 | `10ms` | 命令重试最小退避 |
| `max_retry_backoff` | 否 | `1s` | 命令重试最大退避，不能小于最小退避 |
| `sentinel.master_name` | Sentinel 必填 | 无 | `SENTINEL MONITOR` 使用的 Master 名称 |
| `sentinel.username` | 否 | 空 | Sentinel 自身 ACL 用户，与数据节点用户分开 |
| `sentinel.password` | 否 | 空 | Sentinel 自身密码，不会回退使用数据节点密码 |
| `cluster.read_from_replicas` | 否 | `false` | 允许只读命令访问 Replica；玩家关键状态通常保持关闭 |
| `cluster.route_by_latency` | 否 | `false` | 需同时开启 Replica 读取；只适合可容忍复制延迟的读取 |
| `cluster.max_redirects` | 否 | `3` | MOVED/ASK 与网络拓扑恢复上限 |

Module 强制连接池有硬上限。配置省略时间字段表示采用默认值，不表示“无限等待”。每个业务调用仍应传入有 deadline 的 `ctx`。

## 3. 三种拓扑

Standalone：

```yaml
redis:
  mode: standalone
  addresses: ["redis-cache:6379"]
  username: "game-service"
  password: "${REDIS_PASSWORD}"
  database: 0
  client_name: "player-service"
  pool_size: 32
  max_active_connections: 32
  pool_timeout: 1s
```

Sentinel：`addresses` 填 Sentinel，不填数据节点。切换期间正在执行的命令仍可能失败，业务必须决定是否重试。

```yaml
redis:
  mode: sentinel
  addresses:
    - "sentinel-0:26379"
    - "sentinel-1:26379"
    - "sentinel-2:26379"
  username: "game-service"
  password: "${REDIS_PASSWORD}"
  sentinel:
    master_name: "game-master"
    username: "sentinel-client"
    password: "${REDIS_SENTINEL_PASSWORD}"
  pool_size: 32
  max_active_connections: 32
```

Cluster：多 Key 命令、Watch、事务和 Lua 的 Key 必须在同一 Slot。用 `{...}` 显式声明 Hash Tag：

```yaml
redis:
  mode: cluster
  addresses:
    - "redis-0:6379"
    - "redis-1:6379"
    - "redis-2:6379"
  username: "game-service"
  password: "${REDIS_PASSWORD}"
  pool_size: 16
  max_active_connections: 16
  cluster:
    read_from_replicas: false
    route_by_latency: false
    max_redirects: 3
```

同一玩家的 Key 可使用 `player:{1001}:profile`、`player:{1001}:inventory`。不要为“方便”让全服 Key 共用一个 Hash Tag，
否则会把整个 Cluster 压到一个分片。

## 4. TLS、ACL 与多部署

公共 CA 只需 `tls: true`；私有 CA 再配置 `tls_ca_file`。mTLS 或代码构造的证书使用 `WithTLSConfig`，
且不能与 `tls_ca_file` 混用。Module 拒绝 `InsecureSkipVerify`。

一个 `redismodule.Module` 只拥有一个逻辑部署。缓存、会话和排行榜位于不同 Redis 集群时，组合多个命名 Module；
不要在一个 Module 内维护动态 Client Map。每个 Module 独立启动、Ping、停止和暴露故障。

## 5. 三层使用外观

### 5.1 高频便利层

| 类别 | 常用方法 |
| --- | --- |
| Key | `Del`、`Unlink`、`Exists`、`Expire`、`TTL/PTTL`、`Persist`、`Rename`、`Scan` |
| String | `Get/GetBytes`、`Set`、`SetNX/XX`、`GetEx/GetDel`、`MGet/MSet/MSetNX`、`IncrBy/DecrBy` |
| Hash | `HGet/HGetBytes`、`HSet/HSetMany/HSetNX`、`HMGet`、`HIncrBy`、`HScan` |
| List | `LPush/RPush`、`LPop/RPop`、`LRange`、`LTrim`、`LRem`、`LMove` |
| Set | `SAdd/SRem`、`SIsMember/SMIsMember`、`SDiff/SInter/SUnion`、`SScan` |
| Sorted Set | `ZAdd/NX/XX`、`ZIncrBy`、`ZScore`、`ZRank`、整数 Score 范围/弹出/扫描 |
| Bitmap | `SetBit`、`GetBit`、`BitCount`、`BitOpAnd/Or/Xor/Not` |

`MGet`/`HMGet` 返回 `OptionalString`，用 `Exists` 区分 Miss 和空字符串。单值 Miss 保留 `ErrNil`，可用 `errors.Is(err, redismodule.ErrNil)` 判断。

TTL 返回值保持 Redis 语义：`-1` 表示 Key 存在但无过期时间，`-2` 表示 Key 不存在。批量和范围方法都应设置业务上限；
生产热路径不要使用 `KEYS`、无界 `HGetAll/SMembers/LRange 0 -1` 或一次取完整大排行。

Sorted Set 便利层只接受 `int64`，保证 `±(2^53-1)` 内可精确往返。时间优先等复合排行由业务自行编码 Member/Score，
或通过 `Client()` 使用官方浮点能力；不要误以为基础排行包装能表达所有排序规则。

### 5.2 官方 Client 与组合层

未包装的基础命令直接使用 `Client()` 或有界借用：

```go
err := module.WithClient(ctx, func(ctx context.Context, client redis.UniversalClient) error {
    return client.GeoAdd(ctx, key, locations...).Err()
})
```

调用方不能 `Close` Client，也不能把它交给超过 Module 生命周期的后台 goroutine。

`Pipelined` 减少网络往返但不保证原子性；`TxPipelined` 用 MULTI/EXEC 提交，但运行时命令错误不会像数据库事务一样自动回滚。
`Watch` 冲突返回 `redis.TxFailedErr`，业务应限制重试次数并退避，回调必须可重入且不能直接发奖励、邮件等不可重复副作用。

Lua 用于少量短命令的原子组合：

```go
script := redis.NewScript(`
if redis.call("SET", KEYS[1], "1", "NX", "EX", ARGV[1]) then
    return redis.call("INCRBY", KEYS[2], ARGV[2])
end
return 0
`)

result, err := module.RunScript(ctx, script,
    []string{"reward:{1001}:mail-9", "currency:{1001}:gold"}, 86400, 100)
```

`keys` 只能放 Redis Key，普通参数放 `args`；Cluster 下 Key 必须同 Slot。脚本会阻塞 Redis 执行线程，必须短小、有界、可观测。

### 5.3 Origin Service 协程

Redis I/O 是阻塞调用。在 Service 工作协程中应使用 `Await` 把等待移出工作协程，并在恢复后修改业务状态：

```go
value, err := service.Await(target, ctx, func(ctx context.Context) (string, error) {
    return target.cache.Get(ctx, key)
})
// 这里重新回到 Service 工作协程，可以安全修改 target 的串行状态。
```

不要在 Await 的 I/O 回调内读写 Service 串行字段；先捕获不可变参数，恢复后处理结果。普通并发 HTTP/RPC goroutine 若不依赖 Service 串行状态，
可直接调用并发安全的 Module。

## 6. 回调与 goroutine

| API/回调 | 执行位置 | 使用要求 |
| --- | --- | --- |
| 普通 Redis 方法 | 调用者 goroutine | 同步等待网络 I/O；传有界 Context |
| `WithClient` 回调 | 调用者 goroutine | 不 Close、不长期持有 Client |
| `Pipelined/TxPipelined` 回调 | 调用者 goroutine | 只收集命令；回调报错则不发送 |
| `Watch` 回调 | 调用者 goroutine | 冲突重试时可能再次执行，必须可重入 |
| `WithLock` 回调 | 调用者 goroutine | TTL 内完成；失败或 panic 仍尝试释放 |
| go-redis Hook | Driver 发起命令的 goroutine | 必须并发安全、快速、不得记录凭证/完整敏感值 |
| `Await` I/O 回调 | Origin 调度的非 Service 工作协程 | 不访问 Service 串行状态 |
| `Await` 恢复后的代码 | Service 工作协程 | 可安全处理业务状态 |

## 7. Lease Lock

`TryLock` 立即尝试一次；`Lock` 在 `ctx` 和 `waitTimeout` 的共同边界内用抖动退避等待；`WithLock` 自动释放。

```go
lease, acquired, err := module.TryLock(ctx, "lock:{guild-7}:cache", 3*time.Second)
if err != nil {
    return err
}
if !acquired {
    return serveStaleCache()
}
defer lease.Release(context.WithoutCancel(ctx))
```

长任务必须由业务在明确检查点调用 `Refresh`；Module 不启动自动续租 goroutine。刷新失败后应立即停止受保护操作或进入补偿流程。
TTL 应覆盖“正常 P99 + 可接受抖动”，同时保持短到进程崩溃后能及时恢复。

Redis Lease 不能单独保证奖励、货币、支付或跨系统事务恰好一次。关键写入还要有业务幂等键、数据库唯一约束、版本号或事务兜底。
完整的缓存重建、匹配结算、定时抢占和长任务刷新见[分布式锁示例](../../../../examples/16-redis/04-distributed-lock/README.md)。

## 8. 错误、重试与超时

- `ErrNotRunning`：Module 尚未成功启动或已经停止；启动只有在 Ping 成功后才发布 Client。
- `ErrInvalidConfig/ErrInvalidArgument`：配置或调用参数错误，应修复而不是重试。
- `ErrNil`：Redis Miss；不是服务故障。
- `ErrInvalidScore`：浮点、超出精确范围或整数递增溢出。
- `ErrLockNotObtained`：等待窗口内未获得锁；按业务走降级、稍后重试或返回冲突。
- `context.Canceled/DeadlineExceeded`：调用边界结束。
- `redis.ErrPoolTimeout`：池达到硬上限且等待超时，应先检查慢命令、无界结果和容量，再考虑扩池。

自动重试可能让非幂等命令在“服务端已执行但响应丢失”时重复生效。默认 `max_retries: 0` 是有意的；
需要重试时优先用业务幂等键或 Lua 去重，再设置有界次数和总 Context deadline。

## 9. 性能与容量

1. 先测命令延迟、池等待、超时、Redis CPU/内存和网络，再调整连接池。
2. Pipeline 适合一批相互独立的命令；Lua 只用于需要原子性的短组合。
3. 使用 `SCAN` 家族渐进遍历；`count` 是提示，不保证每页精确数量。
4. 大 Value、热 Key 和单 Hash Tag 会比 Go 包装开销更早成为瓶颈。
5. `Hook` 做指标时只记录命令名、耗时和错误类别，不记录密码、Token 或完整业务参数。
6. `Ping` 在 Cluster 会检查全部 Primary，不应放在业务热路径；交给低频健康检查。

## 10. 可运行示例

| 示例 | 解决的问题 |
| --- | --- |
| [缓存与会话](../../../../examples/16-redis/01-cache-and-session/README.md) | PB 缓存、Miss/损坏回源、批量摘要、滑动会话和一次性 Token |
| [集合与基础排行](../../../../examples/16-redis/02-collections-and-ranking/README.md) | Hash、Set、List、整数 ZSet、Bitmap 与有界 Scan |
| [Pipeline、Lua 与并发](../../../../examples/16-redis/03-pipeline-lua-and-concurrency/README.md) | 批量往返、Watch 有界重试、幂等奖励 Lua 和 Cluster Hash Tag |
| [分布式 Lease Lock](../../../../examples/16-redis/04-distributed-lock/README.md) | 缓存重建、结算幂等、定时抢占、刷新与过期 |

Windows 使用各目录的 `run.bat`，Ubuntu/Linux 使用 `run.sh`。默认连接 `127.0.0.1:6379`；运行前先启动 Redis。

## 11. 选择建议

- 只需一个命令：优先便利层。
- 便利层没有该基础命令：使用 `Client()`/`WithClient`，不要为一次业务需求扩大公共外观。
- 需要减少往返：`Pipelined`；需要同 Slot 命令一起提交：`TxPipelined`。
- 需要基于读取结果做乐观更新：`Watch` + 有界业务重试。
- 需要少量命令真正原子执行：短 Lua。
- 需要减少跨实例并发：Lease Lock，同时保留最终写入幂等。
- 需要可靠消息、消费确认和回放：使用 Kafka 等消息系统，不把 Redis List 包装成通用可靠队列。
