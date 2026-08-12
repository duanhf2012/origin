# Kafka Module 使用指南

> 状态：已实现
>
> 目标版本：Origin v3.2
> Driver：`github.com/IBM/sarama v1.60.1`，JSON：`github.com/bytedance/sonic v1.15.2`

Kafka Module 提供受管 Producer、受管 Consumer 和 Native Sarama 配置三层能力。普通游戏业务使用前两层；事务、手工 Offset、OAuth、特殊 Partition Consumer 和 Admin 使用自由层，并自行负责完整生命周期。

## 1. 十分钟接入

1. 按 [`deploy/kafka`](../../../../deploy/kafka/README.md) 启动开发 Kafka，并显式创建 Topic。
2. 先运行 [`01-producer-workflows`](../../../../examples/17-kafka/01-producer-workflows/README.md)。
3. 再启动 [`02-consumer-service-handler`](../../../../examples/17-kafka/02-consumer-service-handler/README.md)，重新运行 Producer。

推荐把 Kafka Module 组合到业务 Module，把 Topic、Key、Schema 版本和幂等规则集中在业务边界：

```go
type PlayerKafkaModule struct{ kafkamodule.Producer }

func (module *PlayerKafkaModule) OnInit() error {
    var current kafkamodule.ProducerConfig
    if err := module.GetServiceConfigStrict("kafka.producer", &current); err != nil {
        return err
    }
    return module.Setup(current)
}
```

`Setup` 只校验并冻结配置，不连接 Kafka；`OnStart` 才创建 Client。Consumer 用同样方式组合 `kafkamodule.Consumer`，在 `Setup` 中传入 Handler。

## 2. 三个使用层级

| 层级 | 适用场景 | 入口 | 谁拥有资源 |
| --- | --- | --- | --- |
| Managed Producer | RPC 后发事件、关键事件确认、Raw/JSON/PB、批量 | `Producer` | Module |
| Managed Consumer | Consumer Group、Service 串行业务、成功后 Mark、批量 | `Consumer` | Module |
| Native Sarama | Admin、事务、手工 Offset、OAuth、特殊 Consumer | `BuildSaramaConfig` / `BuildAdminSaramaConfig` | 业务 Module |

`BuildProducerSaramaConfig` 和 `BuildConsumerSaramaConfig` 保留 Managed 安全不变量，适合需要自行创建 Sarama 对象但仍采用同一可靠语义的场景。需要改变事务或 Offset 所有权时使用 `BuildSaramaConfig`。

## 3. 配置

表中值是常见在线游戏服务的起点，不是压测结论。先保证可靠性和容量有界，再按消息大小、Partition 数、QPS、P95/P99 与 Broker 指标调整。

### 3.1 Cluster

| 字段 | 必填 | 默认 | 说明与建议 |
| --- | --- | --- | --- |
| `brokers` | 是 | 无 | 去重的 `host:port`；生产至少两个 Seed |
| `version` | 是 | 无 | 集群最低 Broker 版本，不能高于实际集群 |
| `client_id` | 否 | `origin-kafka` | 建议服务名+环境，保持稳定可识别 |
| `dial_timeout` | 否 | `10s` | 建连上限；跨地域按 P99 调整 |
| `read_timeout` / `write_timeout` | 否 | `30s` | Broker I/O 兜底，不代替业务 Context |
| `keep_alive` | 否 | `30s` | TCP Keepalive，不是 Consumer 心跳 |
| `metadata_timeout` | 否 | `10s` | Seed 全不可达时的 Metadata 总等待 |
| `metadata_refresh_interval` | 否 | `10m` | `0s` 关闭主动刷新；大量 Topic 时再调大 |
| `metadata_retry_max` | 否 | `3` | 单次 Metadata 刷新重试次数 |
| `metadata_retry_backoff` | 否 | `250ms` | Leader 选举时的重试间隔 |
| `allow_auto_topic_creation` | 否 | `false` | 生产保持关闭，由运维显式创建 Topic |
| `tls.enable` | 否 | `false` | 跨主机生产建议开启 |
| `tls.ca_file` | 否 | 系统 CA | 追加私有 CA PEM |
| `tls.cert_file` / `key_file` | 否 | 空 | mTLS 必须成对配置 |
| `tls.server_name` | 否 | 地址主机名 | 证书名与地址不一致时配置 |
| `sasl.enable` | 否 | `false` | 开启身份认证 |
| `sasl.mechanism` | 否 | `plain` | `plain`、`scram_sha_256`、`scram_sha_512` |
| `sasl.username` / `password` | SASL 时 | 无 | 密码用 Secret/环境变量；PLAIN 配合 TLS |

TLS 总是验证服务端证书，不支持 `InsecureSkipVerify`。错误和日志不会输出密码、证书内容或消息 Value。

### 3.2 Producer

| 字段 | 默认 | 生产说明 |
| --- | --- | --- |
| `required_acks` | `all` | `none` 只适合可丢遥测，无法得到可靠 Broker 错误/Offset |
| `idempotent` | `true` | 可显式设 `false`；开启时强制 `acks=all`、`MaxOpenRequests=1`，但不等于业务 Exactly Once |
| `compression` | `snappy` | 可选 none/gzip/snappy/lz4/zstd；实测 CPU 与网络后调整 |
| `max_message_size` | `1M` | 不大于 Broker/Topic 上限，并与 Consumer Fetch 对齐 |
| `delivery_timeout` | `10s` | Broker Ack 上限；Context 取消后结果可能未知 |
| `retry_max` / `retry_backoff` | `3` / `100ms` | 只重试 Sarama 可重试错误；业务仍需幂等 |
| `retry_buffer_messages` / `size` | `4096` / `32M` | Sarama Retry Bridge 双硬上限 |
| `flush_messages` / `size` / `interval` | `0` | 0 表示尽快发送；吞吐优先时压测后配置 |
| `flush_max_messages` | `0` | Flush 批次硬上限；不能小于 `flush_messages` |
| `submit_queue_messages` / `size` | `1024` / `64M` | Origin 队列+在途 Payload 双硬上限，持有到 Delivery |
| `channel_buffer_messages` | `256` | Sarama Channel 容量，不代替 Origin 容量 |

### 3.3 Consumer

| 字段 | 必填/默认 | 生产说明 |
| --- | --- | --- |
| `group_id` | 必填 | 稳定包含环境、业务和版本；不要随意改名 |
| `topics` | 必填 | 去重非空列表；首批不支持正则 |
| `initial_offset` | `newest` | 仅在没有已提交 Offset 时生效；补历史用 oldest |
| `balance_strategy` | `cooperative_sticky` | 旧组迁移时全部实例必须使用兼容策略 |
| `instance_id` | 空 | 只给身份稳定且唯一的实例；随机容器不要固定复用 |
| `session_timeout` / `heartbeat_interval` | `10s` / `3s` | 心跳必须小于且通常不高于 Session 的 1/3 |
| `rebalance_timeout` | `60s` | 覆盖在途 Handler 收尾，不要无限放大 |
| `auto_commit_interval` | `1s` | 只提交 Handler 成功后已 Mark 的 Offset |
| `isolation_level` | `read_committed` | 隐藏外部 Kafka 事务中已 Abort 的记录 |
| `reset_invalid_offsets` | `false` | 默认停止告警，不静默跳到 newest/oldest |
| `fetch_min_size` | `1B` | 低延迟起点；提高可换吞吐但增加等待 |
| `fetch_default_partition_size` | `1M` | 覆盖大多数单条消息 |
| `fetch_max_partition_size` | `4M` | 覆盖最大消息且不大于总 Fetch |
| `fetch_max_total_size` | `50M` | 限制一次 Fetch 的总内存 |
| `fetch_max_wait` | `500ms` | 过低增加空轮询和 CPU |
| `max_processing_time` | `100ms` | Sarama Channel 投递预算，不是 Handler 超时 |
| `channel_buffer_messages` | `256` | 与 Service 有界队列共同构成积压 |
| `recovery_initial_backoff` / `max_backoff` | `250ms` / `30s` | 基础设施恢复指数退避并带抖动，最大值是硬上限 |
| `handler_retry_max` / `backoff` | `0` / `1s` | 默认不重试业务；开启前保证 Handler 可重入且幂等 |
| `batch.max_messages` | 批量默认 `100` | 数量硬上限 |
| `batch.max_size` | 批量默认 `1M` | 聚合目标上限；更大的合法单条消息作为单元素批次交付 |
| `batch.max_wait` | 批量默认 `50ms` | 从第一条进入开始计时 |

## 4. Producer API、参数与执行位置

| API | 参数要点 | 编码/准入 | 等待/回调 |
| --- | --- | --- | --- |
| `ProduceAsync` | Raw；nil Value 是 Tombstone，必须有 Key | 调用方 goroutine；零拷贝借用 Buffer | 不等待 Broker |
| `ProduceJSONAsync` / `ProducePBAsync` | JSON Go 值；PB 非 nil Message | 调用方 goroutine 编码并复制 Key/Header，形成稳定快照 | 不等待 Broker |
| `Produce*Sync` | `ctx` 不能为空 | 调用方 goroutine | 当前 goroutine 等待 Delivery；Service 中用 `Await` |
| `Produce*BatchAsync` | 每条必须有 Topic；可跨 Topic | 逐条非阻塞；失败返回已接受前缀 | 非事务、可部分接受 |
| `Produce*BatchSync` | `ctx` + 非空批量 | 逐条准入 | 返回与输入等长结果和 `BatchError` |
| `Delivery.Wait` | `ctx` 不能为空 | 无编码 | 当前 goroutine；取消只停止等待 |
| `DispatchDelivery` | `ctx`、Delivery、Handler | 当前 goroutine预留一个 Service 任务 | wait 在 Await worker；Handler 在 Service 工作协程 |
| `DispatchDeliveries` | 非空 Delivery 列表 | 只预留一个任务 | 顺序等待，一次回调到 Service |
| `Stats` | 无 | 任意 goroutine，无网络 I/O | 原子快照 |

Raw 的 Key、Value、Header Value 在 Delivery 前不能修改或复用。JSON nil 编码为 `null`；普通 string 编码为带引号 JSON；预编码 JSON 使用 `json.RawMessage` 或 Raw。PB 使用现代 `google.golang.org/protobuf`。

## 5. Consumer API、参数与执行位置

| API/回调 | 执行位置 | 规则 |
| --- | --- | --- |
| `Setup` / `SetupBatch` | 业务 Module `OnInit` | 冻结配置，不连接 Broker；只能一次 |
| `Handler` / `BatchHandler` | 所属 Service 串行工作协程 | 可以安全访问业务状态；I/O 用 `Await` |
| Sarama Claim 接收与 Mark | Sarama Claim goroutine | 同 Partition 等上一个 Handler 完成；成功才 Mark |
| `Message.DecodeJSON/PB` | Handler 当前协程 | JSON interface 整数为 int64；拒绝 nil/typed nil 目标 |
| `Pause/PauseAll` | 调用方 goroutine | 只影响后续 Fetch；Module 在新 Claim/Rebalance 重放意图 |
| `Resume/ResumeAll` | 调用方 goroutine | 不触发 Rebalance，不撤销已排队任务 |
| `LastError` / `Stats` | 任意 goroutine | 只读快照，无网络 I/O |

Handler Context 同时携带当前 Service task 身份和 Consumer Session 取消。不要在 Handler 返回后保存 Context，也不要把 Service 非并发安全状态交给其他 goroutine。

## 6. 可靠性与错误

- Handler 返回 nil 后才 Mark；返回错误或 panic 不 Mark，当前受管 Consumer 停止并保留 `LastError`。
- AutoCommit 只提交已经 Mark 的 Offset，因此交付语义是至少一次；崩溃、Rebalance 和提交间隔都会造成重复。
- Service 队列满时结束 Session，不创建无界 goroutine、不 Mark、不静默丢消息。
- Producer 过载立即返回 `errs.ErrTransportOverloaded`。调用方决定降级、返回 RPC 错误或写可靠 Outbox。
- Batch 不是 Kafka 事务；异步批量可能部分接受，消费批次可能整体重投。
- Context 超时或断链后的发送结果可能未知。重试必须依赖 Event ID、业务唯一键或数据库约束。
- 首批不自动建 Topic、不发死信、不无限重试、不提供 Schema Registry、Outbox 或 Managed EOS。

## 7. 常见游戏场景

| 场景 | 推荐方式 | 必须考虑 |
| --- | --- | --- |
| RPC 处理后发行为事件 | `ProduceJSONAsync` + `DispatchDelivery` | 过载快速失败；回调处理 Delivery |
| 充值/关键奖励事件 | Service `Await` 包 `ProduceJSONSync` | 业务幂等；未知结果重查/重试 |
| 玩家状态压缩 Topic 删除 | Raw Tombstone | 非空 Key；Topic 必须 compacted |
| 日志/遥测 | 可明确使用 `acks=none` | 接受丢失且无可靠 Offset/错误 |
| 消费更新玩家数据 | 单条 Handler + 数据库唯一 Event ID | 数据库成功后返回 nil |
| 同 Partition 审计落盘 | BatchHandler | 批次整体重投，写入必须幂等 |
| Kafka 内事务/EOS | `BuildSaramaConfig` + Native Sarama | 自行设计事务恢复、Offset 与外部一致性 |

## 8. 排错

| 现象 | 检查 |
| --- | --- |
| `timestamp out of acceptable range` | Producer/Broker NTP；Kafka 4.3 默认只允许 CreateTime 最多领先 1h |
| 启动一直等待 | Broker/Advertised Listener、DNS、防火墙、认证、Group Join；OnStart 必须等首个 Session Setup |
| Handler 重复 | 正常至少一次语义；检查 Event ID、唯一约束和是否在副作用完成前返回 nil |
| 没有消费旧数据 | Group 已有 Offset 时 `initial_offset` 不生效；换测试 Group 或显式重置 Offset |
| Pause 后仍看到消息 | Pause 不撤回在途 Fetch/Service 任务；等待已接受任务收敛 |
| 异步发送过载 | 检查 Broker 延迟、错误率、`InFlight`、队列上限；不要直接无限加大内存 |
| Windows 能连 Seed 但随后失败 | `advertised.listeners` 返回了容器名/localhost；必须发布客户端可达地址 |

## 9. 性能调优

先监控 Producer Accepted/Succeeded/Failed/Overloaded/InFlight、Consumer Lag、Handler P99、Rebalance、Broker Request P99、压缩比与 CPU。调优顺序通常是 Topic Partition 与 Key 分布、业务 Handler、消息大小/批次、压缩，再考虑 Flush 和队列容量。

首批不使用对象池：Raw 已零拷贝借用，JSON/PB 必须形成稳定快照；对象池会增加跨异步生命周期的归还风险。没有 profile 证据不要增加第二层 Consumer 队列或每消息 goroutine。

## 10. Windows 与 Ubuntu

```powershell
$env:ORIGIN_KAFKA_BROKERS='192.168.8.3:9092'
go test ./sysmodule/kafkamodule -run TestIntegration -count=1
```

```bash
ORIGIN_KAFKA_BROKERS=192.168.8.3:9092 \
  go test -race ./sysmodule/kafkamodule -run TestIntegration -count=1
```

完整代码见 [`examples/17-kafka`](../../../../examples/17-kafka/README.md)。开发 Compose 为局域网明文单节点，只用于教程与验收；生产需要多 Broker、TLS/SASL/ACL、监控、容量规划、NTP 和独立数据盘。
