# Origin Kafka Module 核心设计

> 状态：已确认，允许在 MongoDB、Redis 完成后实施
> 基线：Origin v3.0，目标版本：v3.2
> 兼容性：项目尚未对外发布，不保留 Origin v2 Kafka API、命名或行为兼容层
> Kafka Client 基线：实施前再次复核；截至 2026-08-11 固定为 `github.com/IBM/sarama v1.60.1`
> JSON 基线：实施前再次复核；截至 2026-08-11 固定为 `github.com/bytedance/sonic v1.15.2`

## 1. 文档定位

本文是 `sysmodule/kafkamodule` 实现、测试、Example 和使用教程的单一核心设计。后续实施不得从
Origin v2、Sarama 示例或讨论记录中任意恢复另一套公共外观；需要改变本文已经确认的公共结论时，
必须先更新本文并重新 Review。

本模块面向游戏服务中两条最常见的数据流：

1. Service 收到 RPC，处理业务数据后同步或异步发送到 Kafka；
2. Kafka Consumer Group 收到消息，把业务回调派发到所属 Service 串行工作协程，成功后再标记 Offset。

首批目标是同时提供三个使用层级：

1. **开箱即用层**：Raw、JSON、Protobuf 的单条与批量生产，单条与批量消费；
2. **Origin 集成层**：同步 I/O 使用 `Await`，异步 Delivery 可安全回到 Service，消费回调默认运行在
   Service 串行工作协程；
3. **自由组合层**：提供可复用的 Sarama Config 构建能力，特殊业务可以直接使用 IBM Sarama 并自行拥有
   Client、Producer、Consumer Group 或 Admin 生命周期。

范围控制同样是设计目标。本模块不实现消息总线框架、Schema Registry、Outbox、Exactly Once 业务语义、
自动死信队列、自动业务重试、自动建 Topic、全局 Client Registry 或消息对象池。只有真实项目形成稳定、
重复且跨业务一致的需求后，才增加便利层。

## 2. Origin v2 复核与迁移结论

Origin v2 的 `sysmodule/kafkamodule` 已覆盖同步/异步生产、Consumer Group、简单批处理、Admin 和 SASL
配置样本，但不能直接迁移：

| v2 能力 | v3.2 结论 | 原因 |
| --- | --- | --- |
| 一个 `Producer` 同时嵌入 Sync/Async Producer | 删除 | 两套连接与两套生命周期增加资源和顺序理解成本 |
| `SendMessage/SendMessages` | 规范为 `ProduceSync/ProduceBatchSync` | 名称直接表达同步等待语义 |
| `AsyncSendMessage().WaitOk()` | 重建为 `Delivery.Wait(ctx)` | v2 goroutine 无取消与退出闭环，Metadata 类型断言可能 panic |
| `AsyncPushMessage` | 重建为有界 `ProduceAsync` | v2 直接阻塞写 Sarama Input，过载时可能拖住 Service |
| Consumer 回调返回 `bool` 并无限重试 | 删除 | 毒消息会形成忙循环，无法取消，也没有退避和上限 |
| 按 Topic 聚合批次 | 重建为同 Topic、同 Partition 批次 | 混合 Partition 后提交最大 Offset 容易破坏顺序与重投边界 |
| 手工 `session.Commit()` | 默认删除 | 高频同步 Commit 放大延迟；成功后 Mark、定期提交即可提供 At-least-once |
| 固定后台 Context | 删除 | 必须继承 Service、Consumer Session 和调用方取消语义 |
| `KafkaAdmin` Topic 缓存 | 不包装 | Admin 属于低频运维能力，缓存会过期；自由层直接使用 Sarama Admin |
| v2 类型与拼写兼容 | 不保留 | 项目未发布，应直接形成规范外观 |

v2 中“回调成功才提交 Offset”“支持批量消费”“生产成功/失败必须持续排空”等能力目标继续保留，但全部
按 v3 生命周期、Service 调度、有界过载和测试原则重建。

## 3. 依赖结论

### 3.1 IBM Sarama

采用 IBM 维护的 `github.com/IBM/sarama`。截至设计日最新稳定版为 v1.60.1，支持同步/异步 Producer、
Consumer Group、TLS、SASL、压缩、幂等生产者、事务、Admin 和完整 Kafka 协议能力。

选择它的原因：

- Origin v2 和团队生产项目已经使用 Sarama，迁移与排障经验可以复用；
- 项目活跃、功能完整，适合需要精细控制 Producer/Consumer 参数的游戏服务；
- 原生暴露 Kafka 的 Topic、Partition、Offset、Header 和 Consumer Group 语义，不强加另一套消息模型；
- Producer、Consumer Group 和 Client 都提供显式关闭能力。

约束：

- Sarama 内部具有多个 goroutine 和 Channel，所有返回 Channel 必须持续排空；
- 配置中若打开 Producer Success/Error 返回却无人读取会死锁；
- Consumer Group Handler 会按 Partition 并发调用，不能直接访问非并发安全的 Service 状态；
- Sarama 的网络调用不是全部 Context-aware，关闭和超时必须同时依赖生命周期 Context 与显式网络超时；
- Driver 升级必须重新执行配置差异、关闭、重平衡、幂等生产与双平台集成测试。

### 3.2 JSON

Kafka 便利层使用 `github.com/bytedance/sonic v1.15.2`，只替换 Kafka Module 内部 JSON 编解码，不全局
替换 Origin 其他模块的 `encoding/json`。

内部冻结配置满足以下语义：

- 不使用会关闭 JSON 校验的 `ConfigFastest`；
- 解码到 `map[string]any` 时，整数使用 `int64`，不默认变成 `float64`；
- 解码字符串复制到结果，消费回调结束后不引用 Kafka 输入 Buffer；
- Kafka Payload 不开启 HTML 转义；
- 不为 Map Key 排序，JSON 对象字段顺序不属于协议契约；
- 支持标准 `json.RawMessage` 和 `json.Marshaler`。

Sonic 与 `encoding/json` 都产生合法 JSON，但不承诺字节完全一致。业务不得依赖 Map 字段顺序、HTML
转义形式或原始 JSON 字节做签名和相等判断；需要确定性字节时，应由业务自行编码后发送 Raw，或选择
经过明确约束的 Protobuf 格式。

实施时必须用玩家事件、登录日志、充值事件、邮件附件和嵌套道具等真实结构，对 Sonic、
`encoding/json` 与 `goccy/go-json` 进行 Windows/Ubuntu Benchmark 和差异测试。若 Sonic 在当前 Go 版本、
支持平台或真实数据上出现稳定性/兼容问题，则回到设计阶段评估 `goccy/go-json`，不得静默降级。

### 3.3 Protobuf 与 SCRAM

Protobuf 使用 Origin 已经固定的 `google.golang.org/protobuf/proto`。默认编码不承诺含 Map 消息的字节
确定性；需要确定性编码的特殊业务自行编码 Raw Payload。

普通配置层支持 SASL/PLAIN、SCRAM-SHA-256 和 SCRAM-SHA-512。Sarama 没有内置 SCRAM Client，实施前
复核并固定维护活跃、依赖较小的 SCRAM 实现。OAuth、Kerberos、AWS MSK IAM 等依赖外部 Token Provider
或云厂商插件的方案不进入普通配置，通过 Sarama Config Hook 或自由组合层实现。

## 4. 总体架构

### 4.1 Producer 与 Consumer 分离

采用两个独立 Origin Module：

```go
type Producer struct {
    service.Module
}

type Consumer struct {
    service.Module
}
```

Producer 与 Consumer 不合成一个大 Module，原因是：

- 很多 Service 只生产或只消费；
- 两者配置、连接、goroutine、停止顺序和故障状态不同；
- Consumer Group 的 Group ID、Topics 和重平衡语义不应污染 Producer；
- 独立 Module 可以分别压测、扩容、重启和观察状态。

一个 Module 对应一个逻辑 Kafka 集群和一个 Sarama Client。多个 Kafka 集群通过多个命名 Module 组合，
不在 Module 内维护按字符串查找的 Client Map。

典型业务组合：

```go
type PlayerEventProducer struct {
    kafkamodule.Producer
}

type PlayerEventConsumer struct {
    kafkamodule.Consumer
}
```

业务 Topic、消息模型、幂等键和回调方法放在业务 Module 中，Service 只负责 RPC、生命周期和业务流程，
不散落 Kafka 细节。

### 4.2 单一异步 Producer 内核与提交队列

Managed Producer 只创建一个 `sarama.Client` 和一个 `sarama.AsyncProducer`，并建立一个由 Producer 唯一
拥有的有界提交队列：

- `ProduceAsync` 只把消息提交给有界队列，不等待 Sarama 或 Broker；
- `ProduceSync` 复用同一内核并等待对应 Delivery；
- 单条、批量、Raw、JSON 和 PB 全部复用相同的发送、统计、过载和关闭路径；
- 不再同时创建 SyncProducer 与 AsyncProducer，避免同一业务出现两套连接和不清楚的跨 Producer 顺序。

Sarama 对外的 `AsyncProducer.Input()` 是无缓冲 Channel，不能用一次 `select default` 判断真实容量：内部
Dispatcher 短暂未接收时会把健康 Producer 误判为过载。因此 Managed Producer 必须由一个 submit goroutine
从 Origin 有界队列取消息，再阻塞转交给 Sarama Input。该队列同时限制消息数和 Key/Value/Header 字节数，
是实现“异步立即接受或立即拒绝”的必要适配层，不是通用消息队列或离线缓存。

提交队列是 Producer 唯一新增的队列层；不再建立序列化 Worker Queue、重放 Queue 或每 Topic Queue。
队列项的容量预算一直持有到 Delivery 完成，不是在转交给 Sarama 后提前释放，保证慢 Broker 时 InFlight
Payload 也受总字节上限控制。

模块始终打开并排空 Sarama Successes 与 Errors Channel。使用者不能读取这两个内部 Channel，也不能关闭
Managed Producer。

### 4.3 Consumer 的 Service 派发

Sarama 每个 Partition 的 Claim goroutine 只负责取消息、形成有界批次和等待 Service 处理结果。业务
Handler 通过所属 `Service.DispatchAsync` 进入 Service 有界 FIFO，在 Service 串行工作协程执行：

```text
Kafka broker
    -> Sarama claim goroutine
    -> Service.DispatchAsync
    -> 业务 Handler（Service 串行工作协程）
    -> Handler 成功
    -> claim goroutine MarkMessage
    -> Sarama 定期提交 Offset
```

每个 Claim 在上一个消息或批次完成前不派发同 Partition 的下一个批次，保证 Partition 内业务顺序。不同
Partition 虽然由 Sarama 并发接收，但进入同一 Service 后按 Service FIFO 串行处理。Service 队列满时不
静默丢弃、不 Mark Offset，而是结束当前消费 Session 并报告过载。

### 4.4 Managed 与自由模式

Managed 模式覆盖大多数游戏业务，负责 Origin 生命周期、Service 派发、Delivery、统计和安全默认值。

自由模式提供：

```go
func BuildProducerSaramaConfig(config ProducerConfig, options ...ProducerOption) (*sarama.Config, error)
func BuildConsumerSaramaConfig(config ConsumerConfig, options ...ConsumerOption) (*sarama.Config, error)
func BuildSaramaConfig(config ClusterConfig, options ...SaramaConfigOption) (*sarama.Config, error)
func BuildAdminSaramaConfig(config ClusterConfig, options ...SaramaConfigOption) (*sarama.Config, error)
```

Producer/Consumer Builder 保留 Managed 不变量；需要事务、手工 Offset 等不同所有权时使用
`BuildSaramaConfig`。特殊业务可以用返回的 Config 直接创建并拥有 Sarama Client、事务 Producer、Consumer Group、Partition
Consumer 或 ClusterAdmin。自由模式不自动接入 Origin 生命周期；教程必须明确要求业务 Module 在
`OnStart/OnStop` 中创建、取消、关闭和等待全部资源。

Managed Module 不暴露正在使用的 AsyncProducer、Success/Error Channel 或 ConsumerGroup，避免调用方
误关闭、重复 Consume 或截走完成事件。自由能力通过“新建自己拥有的 Sarama 实例”提供，而不是借用
Managed 内部对象。

## 5. 包与公共类型

首批目录建议：

```text
sysmodule/kafkamodule/
    config.go
    option.go
    message.go
    codec.go
    delivery.go
    producer.go
    consumer.go
    consumer_handler.go
    stats.go
    error.go
    sarama_config.go
```

文件按职责拆分，不建立 `core/factory/manager/adapter` 多层目录。只在单元测试确实需要隔离 Sarama 时定义
最小内部接口，公共 API 不暴露测试工厂。

### 5.1 消息类型

```go
type Header struct {
    Key   string
    Value []byte
}

type ProducerMessage struct {
    Topic     string
    Key       []byte
    Value     []byte
    Headers   []Header
    Timestamp time.Time
}

type JSONMessage struct {
    Topic     string
    Key       []byte
    Value     any
    Headers   []Header
    Timestamp time.Time
}

type PBMessage struct {
    Topic     string
    Key       []byte
    Value     proto.Message
    Headers   []Header
    Timestamp time.Time
}
```

每一条消息都显式包含 Topic。批量接口接收 `[]ProducerMessage`、`[]JSONMessage` 或 `[]PBMessage`，不接收
缺少 Topic 的 `[]PlayerEvent`；同一批可以包含不同 Topic，Sarama 按消息 Topic 和 Key 分区。

`JSONMessage.Value` 是尚未编码的 Go 值，不是 JSON 字符串：

- 传入结构体会编码为 JSON Object；
- 传入 nil 会编码为 JSON `null`；
- 传入普通 string 会编码为带引号的 JSON String；
- 传入 `[]byte` 会按 JSON 规则编码为 Base64 String；
- 已经编码好的 JSON 使用 `json.RawMessage`，或者直接使用 `ProducerMessage.Value`。

`PBMessage.Value` 必须是生成的非 nil Protobuf Message。Kafka Broker 最终只保存字节，不保存“JSON/PB
类型”；跨语言消费者依靠约定的 Topic、Header、Schema 和消息版本解码。

Raw `ProducerMessage.Value == nil` 是合法 Kafka Tombstone，用于 compacted Topic 删除对应 Key；它与长度为
零的非 nil `[]byte{}` 不同。Tombstone 必须同时提供非空 Key，普通非压缩 Topic 不应把 nil 当成业务删除。

### 5.2 消费消息

```go
type Message struct {
    Topic      string
    Partition  int32
    Offset     int64
    Key        []byte
    Value      []byte
    Headers    []Header
    Timestamp  time.Time
    HighWatermark int64
}

func (message *Message) DecodeJSON(destination any) error
func (message *Message) DecodePB(destination proto.Message) error
```

`Message` 的 Key、Value 和 Header Value 在 Handler 返回前有效。Managed Consumer 不复用或归还这些 Buffer，
但使用者若要跨回调长期保存，必须显式复制，避免未来 Driver 行为或业务并发产生所有权歧义。

### 5.3 Delivery 与结果

```go
type Metadata struct {
    Topic     string
    Partition int32
    Offset    int64
    Timestamp time.Time
}

type DeliveryResult struct {
    Metadata Metadata
    Err      error
}

type Delivery struct{}

func (delivery *Delivery) Wait(ctx context.Context) (Metadata, error)
func (delivery *Delivery) Done() <-chan struct{}
func (delivery *Delivery) Result() (DeliveryResult, bool)
```

Delivery 只能完成一次，完成结果可以重复读取；`Wait` 的 Context 取消只停止调用方等待，不代表 Kafka
消息一定未发送。即使调用方不 Wait，Module 仍会排空 Sarama 完成事件并释放内部引用。

`Result()` 只做非阻塞快照；未完成返回 `false`。`Done()` 仅用于 `select`，调用方不得关闭返回 Channel。

## 6. Producer 外观

### 6.1 Setup 与生命周期

```go
func (producer *Producer) Setup(config ProducerConfig, options ...ProducerOption) error
func (producer *Producer) OnInit() error
func (producer *Producer) OnStart(ctx context.Context) error
func (producer *Producer) OnStop(ctx context.Context) error
```

`Setup` 只允许一次并且只能在所属业务 Module/Service 的初始化期调用；它复制配置与 Option，不连接 Kafka。
`OnStart` 构建 Sarama Config、创建 Client/Producer、启动唯一完成事件排空 goroutine，并完成 Metadata
初始化。任一步失败都按逆序清理。

`OnStop` 先拒绝新消息，再关闭 Producer 输入并排空已接收消息，等待 Success/Error Channel 关闭，最后
关闭 Client。关闭过程受网络超时和 Stop Context 共同约束；若 Stop Context 到达，必须主动关闭 Client
中断 I/O，并继续回收内部 goroutine，不能留下后台 Producer。

### 6.2 Raw 单条与批量

```go
func (producer *Producer) ProduceSync(
    ctx context.Context,
    message ProducerMessage,
) (Metadata, error)

func (producer *Producer) ProduceAsync(message ProducerMessage) (*Delivery, error)

func (producer *Producer) ProduceBatchSync(
    ctx context.Context,
    messages []ProducerMessage,
) ([]DeliveryResult, error)

func (producer *Producer) ProduceBatchAsync(
    messages []ProducerMessage,
) ([]*Delivery, error)
```

同步接口等待 Broker Delivery，必须从 Service 工作协程放入 `Await`。异步接口不等待 Broker；正常路径只
完成校验、建立 Delivery 和向 Origin 有界提交队列准入。提交队列达到消息或字节上限时立即返回
`errs.ErrTransportOverloaded`，不阻塞 Service。同步接口使用相同的非阻塞准入规则，成功准入后才用
Context 等待 Delivery；它不会为了等待队列空位隐藏额外延迟。

批量接口规则：

- 空批量返回 `errs.ErrInvalidArgument`；
- 同步批量返回与输入等长的结果，单条失败写入对应 `DeliveryResult.Err`，总错误用于快速判断是否存在失败；
- 异步批量逐条非阻塞提交，发生过载时返回已接受 Delivery 和包含已接受数量的错误；Kafka 不支持撤回
  已提交部分，调用方必须按返回值处理部分接受；
- 批量不是 Kafka 事务，不提供全成全败和跨 Partition 原子性。

### 6.3 JSON 与 PB

JSON 与 PB 分别提供与 Raw 完全对称的四个方法：

```go
ProduceJSONSync(ctx, message)
ProduceJSONAsync(message)
ProduceJSONBatchSync(ctx, messages)
ProduceJSONBatchAsync(messages)

ProducePBSync(ctx, message)
ProducePBAsync(message)
ProducePBBatchSync(ctx, messages)
ProducePBBatchAsync(messages)
```

具体签名返回值与 Raw 对应方法一致。类型名称让使用者在 IDE 中直接发现能力，不引入 `any + encoding`
枚举或反射式统一入口。

JSON/PB 异步方法在调用方 goroutine 完成序列化后再提交，保证函数返回时消息已经形成稳定字节快照；
因此“Async”表示不等待 Kafka Broker，不表示序列化也被后台化。把可变结构体指针交给后台延迟序列化会
引入数据竞争和快照不确定性，首批不采用。

### 6.4 返回 Service 的异步完成

```go
type DeliveryHandler func(context.Context, DeliveryResult)

func (producer *Producer) DispatchDelivery(
    ctx context.Context,
    delivery *Delivery,
    handler DeliveryHandler,
) error

func (producer *Producer) DispatchDeliveries(
    ctx context.Context,
    deliveries []*Delivery,
    handler func(context.Context, []DeliveryResult),
) error
```

该外观复用 `service.DispatchAsyncCompletion`：先在 Service 有界 FIFO 预留一个普通根任务，任务运行后通过
`Await` 等待 Delivery，完成回调严格一次运行在 Service 串行工作协程。它不额外创建每消息 goroutine。

如果预留任务失败，方法返回明确错误，Delivery 本身仍然有效，调用方可以 `Wait`、检查 `Result` 或交给
其他有所有者的流程处理。批量版本只占一个 Service 根任务并一次返回全部结果。

典型 RPC 工作流：

```go
func (module *PlayerModule) SaveEvent(ctx context.Context, event PlayerEvent) error {
    delivery, err := module.kafka.ProduceJSONAsync(kafkamodule.JSONMessage{
        Topic: "player-events",
        Key:   []byte(event.PlayerID),
        Value: event,
    })
    if err != nil {
        return err
    }

    return module.kafka.DispatchDelivery(ctx, delivery,
        func(_ context.Context, result kafkamodule.DeliveryResult) {
            module.onEventDelivered(result)
        })
}
```

如果 RPC 必须在返回前确认 Kafka 已持久化，则直接在当前 Service Task 中使用 `Await` 包住
`ProduceSync`，不能先异步后忙等。

## 7. Consumer 外观

### 7.1 Handler

```go
type Handler func(context.Context, *Message) error

type Batch struct {
    Topic     string
    Partition int32
    Messages  []*Message
}

type BatchHandler func(context.Context, Batch) error

func (consumer *Consumer) Setup(config ConsumerConfig, handler Handler, options ...ConsumerOption) error
func (consumer *Consumer) SetupBatch(config ConsumerConfig, handler BatchHandler, options ...ConsumerOption) error
```

同一个 Consumer 实例只能选择单条或批量模式之一。Handler 参数 Context 同时携带 Origin 当前 Task 私有
执行身份和 Consumer Session 取消语义，因此回调中可以安全调用所属 Module 的 `Await`。Handler 不得在
返回后保存 Context，也不得把 Service 非并发安全状态交给其他 goroutine。

Handler 返回 nil 后才 Mark Message。返回错误时不 Mark；Consumer 按显式 Handler 重试配置执行有界重试，
耗尽后停止当前 Managed Consumer、保存 `LastError` 并输出脱敏结构化日志。首批不自动 Skip、提交毒消息
或发送死信 Topic，避免框架静默丢数据。

### 7.2 批量规则

批量只聚合同一 Topic、同一 Partition 的连续消息，按以下任一条件触发：

- 达到 `batch.max_messages`；
- 达到 `batch.max_size`；
- 第一条消息进入后达到 `batch.max_wait`；
- Claim 正常结束且 Session 尚未取消。

批次 Handler 成功后按消息顺序 Mark 全部 Offset；失败则整个批次不 Mark，之后可能整体重投。批次不是
Kafka 事务，业务部分成功后返回错误会造成已完成部分再次执行，因此消费逻辑必须使用事件 ID、业务唯一键、
数据库约束或幂等记录防重。

Session 已取消或 Partition 已被撤销时，不再派发尚未处理的半批次，留给下一任 Consumer 重投。不能为了
“尽量清空”在 Rebalance Deadline 后继续访问已经失效的 Session。

### 7.3 暂停、恢复和状态

```go
func (consumer *Consumer) PauseAll() error
func (consumer *Consumer) ResumeAll() error
func (consumer *Consumer) Pause(partitions map[string][]int32) error
func (consumer *Consumer) Resume(partitions map[string][]int32) error
func (consumer *Consumer) Stats() ConsumerStats
func (consumer *Consumer) LastError() error
```

Pause/Resume 只控制继续 Fetch，不取消已经进入 Service 队列的任务，也不提交未处理 Offset。未启动、停止中
或参数非法返回稳定错误。`LastError` 返回保留错误链的只读快照，日志与错误文本不得包含 Payload、凭证或
完整 TLS 内容。

### 7.4 Rebalance 与恢复

OnStart 创建 Client 和 Consumer Group 后启动一个由 Consumer 唯一拥有的 Consume 循环，并等待第一个
Session `Setup` 成功或启动 Context 失败，不能在尚未完成认证、Metadata 和 Group Join 时提前报告 Ready。

运行期 Broker 断连、Leader 变化和 Rebalance 属于基础设施恢复，允许在生命周期内持续恢复，但必须使用
有上限且带抖动的退避，单一 goroutine 串行重试，不积累新的 Consume goroutine、Session 或消息。确定性
认证、授权、配置和不支持协议错误立即停止；Handler 错误按业务失败处理，不混入基础设施无限恢复。

## 8. 配置设计

### 8.1 公共配置类型

```go
type ClusterConfig struct {
    Brokers                   []string
    Version                   string
    ClientID                  string
    DialTimeout               config.Duration
    ReadTimeout               config.Duration
    WriteTimeout              config.Duration
    KeepAlive                 config.Duration
    MetadataTimeout           config.Duration
    MetadataRefreshInterval   config.Duration
    MetadataRetryMax          int
    MetadataRetryBackoff      config.Duration
    AllowAutoTopicCreation    bool
    TLS                       TLSConfig
    SASL                      SASLConfig
}

type TLSConfig struct {
    Enable     bool
    CAFile     string
    CertFile   string
    KeyFile    string
    ServerName string
}

type SASLConfig struct {
    Enable    bool
    Mechanism string
    Username  string
    Password  string
}
```

ProducerConfig 与 ConsumerConfig 分别组合 ClusterConfig，避免把不相关字段放进同一大结构。所有 Duration
和 ByteSize 遵守 Origin 带单位字符串规则；配置结构体普通字段不机械添加 Tag。

TLS 使用系统 Root CA，并可追加 `ca_file`；同时提供 Cert/Key 时启用双向 TLS。禁止配置
`InsecureSkipVerify`。密码建议使用 `${KAFKA_PASSWORD}` 环境变量，不写入 YAML、日志或错误。

### 8.2 ProducerConfig

```go
type ProducerConfig struct {
    Cluster                 ClusterConfig
    RequiredAcks            string
    Idempotent              *bool
    Compression             string
    MaxMessageSize          config.ByteSize
    DeliveryTimeout         config.Duration
    RetryMax                int
    RetryBackoff            config.Duration
    RetryBufferMessages     int
    RetryBufferSize         config.ByteSize
    FlushMessages           int
    FlushSize               config.ByteSize
    FlushInterval           config.Duration
    FlushMaxMessages        int
    SubmitQueueMessages     int
    SubmitQueueSize         config.ByteSize
    ChannelBufferMessages   int
}
```

默认值与生产建议起点：

| 字段 | 必填 | 默认值 | 生产说明 |
| --- | --- | --- | --- |
| `cluster.brokers` | 是 | 无 | 至少两个不同 Broker Seed，地址必须去重 |
| `cluster.version` | 是 | 无 | 填实际 Kafka Broker 最低版本，不能高于集群 |
| `cluster.client_id` | 否 | `origin-<service>` | 用于 Broker 审计与指标，需稳定且可识别 |
| `dial_timeout` | 否 | `10s` | 建连上限；跨地域需依据 P99 调整 |
| `read_timeout` | 否 | `30s` | Broker 响应读取上限 |
| `write_timeout` | 否 | `30s` | 请求写入上限 |
| `keep_alive` | 否 | `30s` | TCP Keepalive 探测周期；不是 Kafka 心跳 |
| `metadata_timeout` | 否 | `10s` | 限制全部 Seed 不可达时的总等待 |
| `metadata_refresh_interval` | 否 | `10m` | 大规模 Topic 可适当延长，0s 表示关闭主动刷新 |
| `metadata_retry_max` | 否 | `3` | 单次 Metadata 刷新中的重试上限 |
| `metadata_retry_backoff` | 否 | `250ms` | Leader 选举期间的重试间隔 |
| `allow_auto_topic_creation` | 否 | `false` | 生产必须由运维显式创建 Topic |
| `tls.enable` | 否 | `false` | 启用 TLS；生产跨主机建议开启 |
| `tls.ca_file` | 否 | 空 | 追加私有 CA；空值使用系统 CA |
| `tls.cert_file/key_file` | 否 | 空 | mTLS 必须同时配置，不能只填一个 |
| `tls.server_name` | 否 | Broker 主机名 | 证书名称与连接地址不一致时显式配置 |
| `sasl.enable` | 否 | `false` | 启用 Kafka 身份认证 |
| `sasl.mechanism` | 否 | `plain` | 普通层支持 plain、scram_sha_256、scram_sha_512 |
| `sasl.username/password` | 启用 SASL 时 | 无 | 密码使用环境变量，PLAIN 应配合 TLS |
| `required_acks` | 否 | `all` | 游戏关键事件优先可靠性；`none` 无可靠 Delivery |
| `idempotent` | 否 | `true` | 可显式设 `false`；开启可减少 Producer 重试重复，仍不等于业务 Exactly Once |
| `compression` | 否 | `snappy` | 小消息吞吐与 CPU 的稳健起点；实测后可选 lz4/zstd |
| `max_message_size` | 否 | `1M` | 必须不大于 Broker/Topic 上限，并与 Consumer Fetch 对齐 |
| `delivery_timeout` | 否 | `10s` | Broker 等待 Ack 的上限，不是完整业务 Deadline |
| `retry_max` | 否 | `3` | 只处理 Sarama 可重试错误；业务仍需幂等 |
| `retry_backoff` | 否 | `100ms` | 防止 Leader 切换时忙循环 |
| `retry_buffer_messages` | 否 | `4096` | 显式限制 Sarama Retry Bridge，防止无界内存 |
| `retry_buffer_size` | 否 | `32M` | 与消息数量上限同时生效 |
| `flush_messages/size/interval` | 否 | `0` | 0 表示尽快发送；吞吐优先场景压测后再配置批聚合 |
| `flush_max_messages` | 否 | `0` | 配置 Flush 时建议设硬上限，不能形成无限批次 |
| `submit_queue_messages` | 否 | `1024` | Origin 提交队列消息硬上限，满时异步立即过载 |
| `submit_queue_size` | 否 | `64M` | 覆盖排队与已转交未完成 Payload 的总字节预算 |
| `channel_buffer_messages` | 否 | `256` | Sarama 内部 Channel 容量，不代替 Origin 提交队列 |

幂等 Producer 强制 `acks=all`、`Net.MaxOpenRequests=1` 和兼容的重试配置。普通 Config 或 Hook 若破坏这些
不变量，Setup/OnStart 返回配置错误，不静默改成非幂等。

`required_acks=none` 无法提供可靠 Offset 和 Broker 错误，只允许明确可丢的遥测场景；Delivery 成功只表示
进入发送路径，教程必须突出其风险。

### 8.3 ConsumerConfig

```go
type ConsumerConfig struct {
    Cluster                    ClusterConfig
    GroupID                    string
    Topics                     []string
    InitialOffset              string
    BalanceStrategy            string
    InstanceID                 string
    SessionTimeout             config.Duration
    HeartbeatInterval          config.Duration
    RebalanceTimeout           config.Duration
    AutoCommitInterval         config.Duration
    IsolationLevel             string
    ResetInvalidOffsets        bool
    FetchMinSize               config.ByteSize
    FetchDefaultPartitionSize  config.ByteSize
    FetchMaxPartitionSize      config.ByteSize
    FetchMaxTotalSize          config.ByteSize
    FetchMaxWait               config.Duration
    MaxProcessingTime          config.Duration
    ChannelBufferMessages      int
    RecoveryInitialBackoff     config.Duration
    RecoveryMaxBackoff         config.Duration
    HandlerRetryMax            int
    HandlerRetryBackoff        config.Duration
    Batch                      BatchConfig
}

type BatchConfig struct {
    MaxMessages int
    MaxSize     config.ByteSize
    MaxWait     config.Duration
}
```

| 字段 | 必填 | 默认值 | 生产说明 |
| --- | --- | --- | --- |
| `group_id` | 是 | 无 | 同一业务消费组必须稳定，环境和用途应进入名称 |
| `topics` | 是 | 无 | 去重后的非空 Topic；首批不支持正则订阅 |
| `initial_offset` | 否 | `newest` | 只在没有已提交 Offset 时生效；补历史数据显式用 oldest |
| `balance_strategy` | 否 | `cooperative_sticky` | 新组推荐；迁移旧组时全部实例必须兼容同一策略 |
| `instance_id` | 否 | 空 | 稳定唯一实例可启用静态成员；容器随机副本不要误用固定值 |
| `session_timeout` | 否 | `10s` | 必须落在 Broker 允许范围 |
| `heartbeat_interval` | 否 | `3s` | 必须小于 session_timeout，通常不高于其 1/3 |
| `rebalance_timeout` | 否 | `60s` | 覆盖在途 Handler 收尾，不能无限放大 |
| `auto_commit_interval` | 否 | `1s` | 只提交已经成功 Mark 的 Offset |
| `isolation_level` | 否 | `read_committed` | 隐藏外部事务中已 Abort 的记录 |
| `reset_invalid_offsets` | 否 | `false` | Offset 越界默认停止并告警，避免静默跳到 newest/oldest |
| `fetch_min_size` | 否 | `1B` | 低延迟起点；提高可换吞吐但增加等待 |
| `fetch_default_partition_size` | 否 | `1M` | 应覆盖大多数单条消息 |
| `fetch_max_partition_size` | 否 | `4M` | 必须覆盖最大消息并小于总 Fetch 上限 |
| `fetch_max_total_size` | 否 | `50M` | 限制一次 Fetch 总内存，仍需考虑 Broker 单批例外 |
| `fetch_max_wait` | 否 | `500ms` | 过低会增加 CPU 和网络请求 |
| `max_processing_time` | 否 | `100ms` | Sarama Channel 投递预算，不是业务 Handler 超时 |
| `channel_buffer_messages` | 否 | `256` | 与 Service 队列共同构成有界积压 |
| `recovery_initial_backoff` | 否 | `250ms` | 运行期恢复退避起点 |
| `recovery_max_backoff` | 否 | `30s` | 持续恢复的单次等待上限，实际加入抖动 |
| `handler_retry_max` | 否 | `0` | 默认不自动重复业务；需要时显式配置并保证幂等 |
| `handler_retry_backoff` | 否 | `1s` | Handler 有界重试间隔 |
| `batch.max_messages` | 批量模式 | `100` | 数量硬上限 |
| `batch.max_size` | 批量模式 | `1M` | 聚合目标上限；合法的更大单条消息会作为单元素批次交付 |
| `batch.max_wait` | 批量模式 | `50ms` | 从第一条进入开始计时，延迟与吞吐的起点 |

`AutoCommit` 固定启用，内部只有 Handler 成功才 Mark。普通配置不提供“收到即提交”或“自动提交未处理
消息”的模式。需要完全手工 Offset、事务性 Offset 或外部存储 Offset 的特殊消费者使用自由模式。

### 8.4 Option 与 Sarama Hook

```go
type SaramaConfigHook func(*sarama.Config) error

func WithProducerSaramaConfig(hook SaramaConfigHook) ProducerOption
func WithConsumerSaramaConfig(hook SaramaConfigHook) ConsumerOption
func WithSaramaConfig(hook SaramaConfigHook) SaramaConfigOption
```

Hook 在 Origin 配置完成映射、TLS/SASL 建立后执行，在最终 `Validate` 前执行。它运行在 Module 启动 goroutine，
不是 Service 业务工作协程。Hook 可以配置 Interceptor、OAuth Token Provider、Rack ID 和低频 Sarama 能力，
但不能破坏 Managed 不变量：

- Producer Successes/Errors 必须开启并由 Module 排空；
- Managed Producer 首批禁止 Transactional ID；
- Retry Buffer 必须有数量和字节上限；
- Consumer Errors 必须开启并由 Module排空；
- Managed Consumer 必须使用成功后 Mark 的 Offset 模式；
- TLS 不允许跳过服务端证书校验。

同一 Option 实例为只读配置，不能保存运行 Client 或跨 Application 可变状态。

## 9. Context、goroutine 与所有权

### 9.1 执行位置

| 操作/回调 | 执行位置 |
| --- | --- |
| `Setup`、配置校验 | 调用方初始化 goroutine，不做网络 I/O |
| Sarama Config Hook | Module `OnStart` goroutine |
| Raw `ProduceAsync` 校验与入队 | 调用方 goroutine |
| JSON/PB 序列化 | 调用方 goroutine |
| `Delivery.Wait` | 调用该方法的 goroutine |
| Service 中 `Await(...ProduceSync...)` | Kafka 等待发生在 Await Worker，完成后回到 Service 工作协程 |
| `DispatchDelivery` 的 Handler | Service 串行工作协程 |
| 有界队列转交 Sarama Input | Producer 拥有的单一 submit goroutine |
| Sarama Producer Success/Error 排空 | Producer 拥有的单一 completion goroutine |
| Sarama Consumer Claim | Sarama 管理的 Claim goroutine |
| 单条/批量业务 Handler | Service 串行工作协程 |
| Consumer Handler 中的 `Await` 回调 | Await Worker |
| Sarama Interceptor | Sarama 内部 goroutine；不得访问 Service 非并发安全状态 |
| 自由模式 Sarama 回调 | 由使用者负责，Origin 不保证 Service 协程语义 |

教程必须逐个说明这些边界，不能只写“线程安全”。

### 9.2 发送所有权

Raw 消息以尽量零拷贝为目标：Produce 接受后，Key、Value 和 Header Value 的只读所有权转移到 Producer，
调用方在 Delivery 完成前不得修改或复用底层数组。模块不会为了防御未知误用默认复制全部 Payload。
容量计费包含 Key、Value 和全部 Header Key/Value；消息数上限约束每消息结构开销。单条超过
`submit_queue_size` 或 `max_message_size` 在接管所有权前直接拒绝。

JSON/PB 产生的新编码 Buffer、Key 和 Header Value 副本由 Module 持有到 Delivery 完成，调用方仍拥有
全部原始输入。序列化完成后修改原始对象或切片不会改变已提交消息。

发送失败、过载拒绝或编码失败时，未接受消息的所有权仍属于调用方。批量部分接受时按返回的 Delivery
数量区分所有权，错误必须包含已接受数量但不能包含 Payload。

### 9.3 消费所有权

Consumer Message 只在 Handler 调用期间借用。业务要保存 Key/Value/Header，必须复制；业务反序列化到
自己的结构体后，目标对象由业务拥有。Module 不建立跨回调消息缓存，也不把消费消息放入无界重试队列。

### 9.4 Context

- nil Context 对所有需要等待的方法均非法，不偷偷使用 `context.Background()`；
- `ProduceSync` Context 取消只结束等待，消息仍可能最终成功；
- `ProduceAsync` 不接收 Context，保证调用语义是立即接受或立即过载；
- Consumer Handler Context 在 Service 停止、Session 撤销或当前任务取消时结束；
- Handler 内阻塞数据库、Redis、HTTP 或其他 Kafka 同步 I/O 必须使用 `Await`；
- Context 不保存到回调外，也不作为 Module 后台生命周期的替代品。

## 10. 可靠性、顺序和原子性边界

### 10.1 Producer

| 能力 | 保证与限制 |
| --- | --- |
| `acks=all` | Broker 按 ISR 配置确认，不代表所有副本永久不丢 |
| 幂等 Producer | 减少同一 Producer Session 重试重复；不覆盖应用重启后的业务重复 |
| 相同 Key | 在 Topic Partition 数量稳定且分区器一致时进入同一 Partition |
| Partition 顺序 | Kafka 只保证 Partition 内顺序；跨 Partition/Topic 无全局顺序 |
| Batch | 发送便利与协议批聚合，不是事务，不保证全成全败 |
| Context 超时 | 结果未知，不能据此断言消息未写入 |
| Delivery 成功 | 当前 Producer 收到 Broker 确认；业务消费者是否处理属于另一阶段 |

金币、道具、充值和结算事件必须带稳定 Event ID，并在消费端使用数据库唯一约束或幂等记录。Kafka
Producer 幂等不能替代业务幂等。

### 10.2 Consumer

Managed Consumer 提供 At-least-once：Handler 成功后 Mark，Offset 周期提交。进程在 Handler 成功与 Offset
提交之间退出时，消息会再次投递。Handler 必须可重入和幂等。

Handler 失败默认停止 Consumer，不自动跳过。业务若确认某类格式错误可以进入死信，应在 Handler 中显式
发送到独立 Topic并等待成功，再返回 nil；这仍是业务流程，不由框架自动隐藏。

单个 Service 串行处理多个 Partition 可保护 Service 状态，但吞吐上限受该 Service 处理能力约束。需要更高
吞吐时应增加 Topic Partition 和 Service/Consumer 实例，通过 Key 保持玩家级顺序，而不是在一个 Service
内部并发修改玩家状态。

### 10.3 事务与 EOS

首批 Managed 外观不包装 Sarama Transaction、`AddOffsetsToTxn` 或 Consume-Transform-Produce Exactly Once。
原因是 Kafka EOS 需要 Transactional ID 稳定分配、Producer Fencing、Consumer Isolation、Rebalance、
Offset 与事务共同提交，以及业务外部数据库一致性边界；简单增加几个转发方法会给出错误安全感。

确实只操作 Kafka 且需要 EOS 的特殊业务，可以使用自由模式直接构建 Sarama Transaction Producer，并在
业务 Module 中明确拥有生命周期。形成两个以上真实业务后再单独建立事务设计，不在首批顺手实现。

## 11. 过载、重试与故障处理

### 11.1 Producer 过载

Producer 使用一层必要的 Origin 有界提交队列，再复用 Sarama Retry Buffer 和协议批处理：

- 提交队列达到消息或字节上限时 `ProduceAsync` 立即返回 `errs.ErrTransportOverloaded`；
- 同步调用也对满队列快速失败；成功准入后在 Await Worker 等待 Delivery，并受 Context 控制；
- Retry Bridge 同时限制消息数量和字节总量；
- 不丢旧消息、不随机丢消息、不覆盖未完成消息；
- 业务自行决定拒绝 RPC、降级非关键日志或写入其他持久化补偿。

submit goroutine 是队列唯一消费者；它可以阻塞写 Sarama Input，不占用 Service 工作协程。completion
goroutine 在 Broker 完成后释放 Delivery 和队列字节预算。停止时先封闭新准入，再按 FIFO 把已经接受的
队列项转交并 Drain；Stop Context 到达后仍未发送的项以明确关闭错误完成，不能静默遗失。

这符合大多数游戏服务“关键事件快速失败并由业务决定”的策略，避免框架在内存中隐藏无限堆积。

### 11.2 Consumer 过载

Consumer 不增加第二层消息队列：Claim goroutine 等待 Service 任务完成，自然把背压传回 Sarama。Service
队列满时结束 Session且不 Mark 当前消息，之后重投。不得通过无界 goroutine 并发派发绕过 Service 容量。

批量模式只在同 Partition 的 `max_messages/max_size/max_wait` 范围内缓存。Timer 每 Claim 一个，由 Claim
创建、停止并等待；不使用 `time.After` 在循环中不断分配。

### 11.3 重试

- Producer 只使用 Sarama 可重试错误与显式上限；
- Handler 自动重试默认关闭，开启后次数与退避都有上限；
- Consumer 基础设施恢复可持续到生命周期结束，但每轮单一、退避有上限并带抖动；
- 不为发送或消费创建无界 pending、离线消息缓存和后台业务重放器；
- 超时和断链后结果可能未知，调用方重试必须依赖业务幂等键。

## 12. 错误、安全与可观测性

### 12.1 错误

- 空 Topic、PB nil Value、Tombstone 空 Key、空 Brokers、非法 Version、范围和组合错误使用 Origin
  `ErrInvalidArgument/ErrInvalidConfig`；Raw nil Value 与 JSON nil 按各自明确语义处理；
- 未启动、停止中和已停止使用稳定生命周期错误；
- Producer Input 或 Service 队列达到上限使用 `ErrTransportOverloaded` 或原始 `ErrServiceQueueFull`；
- Kafka Broker、认证、授权、编码、Context 和 Sarama 错误保留 `errors.Is/As` 所需错误链；
- Batch 聚合错误保留输入索引、Topic、Partition（若已知）与原因，不包含 Payload；
- `LastError` 和日志不得包含密码、Token、完整证书、消息 Value 或可能敏感的 Header Value。

### 12.2 安全

- TLS 默认验证服务端证书，CA 文件只追加到系统 CA；
- Cert/Key 必须同时配置，读取失败立即停止启动；
- SASL 密码通过环境变量注入；
- PLAIN 不应在无 TLS 的公网或不可信网络使用；
- Config Hook 和 Interceptor 可能看到消息与凭证，教程要求只使用受信任代码；
- 默认日志只记录 Client ID、Topic、Partition、Offset、错误类型和计数，不记录 Key/Value/Header Value。

### 12.3 Stats

```go
type ProducerStats struct {
    Accepted   uint64
    Succeeded  uint64
    Failed     uint64
    Overloaded uint64
    InFlight   int64
}

type ConsumerStats struct {
    Received        uint64
    Handled         uint64
    Failed          uint64
    Batches         uint64
    Rebalances      uint64
    DispatchRejected uint64
    Running         bool
}
```

`Stats()` 返回无锁原子快照，不遍历 Sarama Metrics Registry。高级 Broker、请求和延迟指标通过自由层或
Sarama Metric Registry Hook 接到业务监控；首批不构建 Origin 专用 Metrics 适配框架。

## 13. 教程设计

### 13.1 文档入口

实施时新增：

```text
docs/maintenance/v3.2/guides/Kafka Module使用指南.md
examples/17-kafka/01-producer-workflows
examples/17-kafka/02-consumer-service-handler
examples/17-kafka/03-managed-and-native
deploy/kafka/
```

根 README 的扩展组件教程表和 `docs/maintenance/v3.2/README.md` 增加 Kafka 行，但不重排基础框架教程。

### 13.2 三个使用层级

教程必须按使用者决策顺序讲清：

1. **最小开箱即用**：配置 Brokers、Version、Topic/Group，发送/消费一条 JSON；
2. **Origin Service 集成**：RPC 后 `ProduceSync + Await`、`ProduceAsync + DispatchDelivery`、Consumer Handler
   运行在 Service 工作协程以及 Handler 内再次 Await 数据库；
3. **自由 Sarama 模式**：Admin、事务、OAuth、特殊 Partition Consumer 如何用 Config Builder 自行组合，
   并明确这些对象不再由 Managed Module 管理。

每层先给“什么时候用”，再给最小代码、执行协程、错误处理和不能保证什么。不能从完整 Sarama Config
结构开始，也不能只展示 Happy Path。

### 13.3 配置教程

指南必须提供：

- Producer 与 Consumer 分开的最小 YAML；
- TLS、mTLS、SASL/PLAIN、SCRAM-SHA-256/512 配置；
- 每个字段的必填性、默认值、生产建议起点、调整依据和错误组合；
- `required_acks`、幂等、压缩、Batch、Fetch、Consumer Group、Offset 和 Rebalance 的简洁解释；
- Kafka/Broker/Topic 的 `message.max.bytes`、副本和 ISR 约束需要由运维同步配置；
- Windows 连接 Ubuntu Kafka 时 Advertised Listener 与防火墙注意事项；
- 凭证只通过环境变量传入，不出现用户提供的真实密码。

### 13.4 API 与协程表

指南必须逐组列出公开函数、参数、返回值、是否网络 I/O、在哪个 goroutine 执行、所有权何时转移、Context
取消后的不确定性和适合的使用场景。重点包括：

- Raw/JSON/PB 单条和批量同步/异步；
- Delivery 的 Wait/Done/Result；
- DispatchDelivery/DispatchDeliveries；
- 单条/批量 Consumer Handler；
- DecodeJSON/DecodePB；
- Pause/Resume、Stats、LastError；
- Config Builder 和 Hook。

### 13.5 可靠性教程

必须用游戏场景解释：

- 相同玩家 ID 作为 Key，只保证玩家所在 Partition 内顺序；
- RPC 成功前必须确认 Kafka 时使用 Await + ProduceSync；
- 非关键行为日志可以 ProduceAsync，但过载也必须有明确降级；
- Handler 成功后、Offset 提交前崩溃会重复投递；
- 充值、奖励和结算使用 Event ID 与数据库唯一约束；
- 批量部分成功、异步批量部分接受和 Context 超时后的处理；
- 毒消息不自动跳过，业务显式决定修复、停机或写死信；
- Kafka 幂等 Producer 不等于业务 Exactly Once。

## 14. 完整 Example

### 14.1 `01-producer-workflows`

同一个可运行 Example 演示：

- 业务 `PlayerEventModule` 组合 `kafkamodule.Producer`；
- 模拟 RPC 在 Service 工作协程接收玩家事件；
- Raw、JSON、PB 单条发送；
- JSON/PB 批量消息中每条显式填写 Topic；
- `Await + ProduceSync` 的可靠 RPC 路径；
- `ProduceAsync + DispatchDelivery` 的不阻塞 Broker 路径；
- Header、玩家 ID Key、Partition/Offset 结果；
- compacted Topic 中 Raw Tombstone 与空字节 Value 的差异；
- 编码失败、提交队列过载、Context 超时和部分批量接受的处理。

README 说明同步确认与异步接受的区别，以及 Async JSON 仍在调用方完成序列化。

### 14.2 `02-consumer-service-handler`

演示：

- `PlayerEventConsumer` 组合 Consumer；
- JSON 与 PB Topic 的解码与消息版本 Header；
- Handler 确实运行在 Service 串行工作协程；
- Handler 内通过 `Await` 模拟数据库 I/O；
- Event ID 幂等表，重复消息不会重复发奖励；
- 单条模式与同 Partition 批量模式；
- Handler 返回错误不提交、重启后重投；
- Pause/Resume、Stats、LastError 和优雅停止。

### 14.3 `03-managed-and-native`

演示三个层次的边界：

- Managed Producer/Consumer 的推荐普通用法；
- Config Hook 添加可信 Interceptor 或特殊 Rack ID；
- `BuildAdminSaramaConfig` 创建 Topic、查询 Topic 后显式关闭 Admin；
- 用 Config Builder 创建一个完全由业务拥有的 Sarama Client，并在 `OnStop` 逆序关闭；
- 明确事务/EOS 只展示入口与生命周期骨架，不伪造完整业务 Exactly Once。

### 14.4 Example 交付标准

每个 Example 都必须：

- 包含 README、完整带注释 YAML、运行命令、环境变量、预期输出和清理说明；
- 可独立 `go build`，关键流程有自动化测试；
- 使用业务 Module 承载 Kafka 代码，不把业务回调散落在 Service；
- 同时包含成功与至少一个可观察失败分支；
- 说明每个回调和参数在哪个 goroutine 有效；
- 不包含真实服务器地址、用户名、密码或生产 Topic；
- Windows 与 Ubuntu 都能运行，Windows 可连接 Ubuntu 保留的测试 Kafka。

## 15. Ubuntu Docker Kafka 环境

Kafka Docker 只在 Kafka Module 实施阶段安装，设计阶段和 MongoDB/Redis 实施阶段不提前安装。

实施时在 Ubuntu 测试主机使用 Docker Compose 安装单节点 KRaft Kafka，并把可重复配置提交到
`deploy/kafka/`。镜像与 Kafka 版本在实施当天复核官方稳定版后固定；配置至少包含：

- 持久化 Volume；
- 容器内 Listener 与局域网可访问的 Advertised Listener；
- 健康检查；
- 自动建 Topic 关闭；
- 测试 Topic 创建脚本；
- Broker/Topic 消息上限与 Producer/Consumer Example 对齐；
- 仅测试网访问，不暴露公网；
- 日志和命令不记录凭证。

测试完成后**不执行 `docker compose down`，不删除 Container、Image、Network 或 Volume**。只停止由测试
进程创建的临时 Consumer/Producer，并把 Kafka 容器状态、端口、版本和后续使用方法写入验收报告。后续
若需要升级或删除环境，必须由使用者另行确认。

如果 Ubuntu 已有 Kafka 容器，先只读检查名称、端口、Volume 和版本；不得覆盖或删除不属于本任务的环境。

## 16. 测试设计

### 16.1 单元测试

必须覆盖：

- Producer/Consumer Setup、默认化、严格配置、重复 Setup、未启动、停止中和重复 Stop；
- Brokers、Version、ClientID、Duration、ByteSize、TLS、mTLS、SASL 和凭证脱敏；
- RequiredAcks、幂等约束、压缩、消息上限、Flush、Retry Buffer 和 Channel 容量；
- Consumer Group、Topics、Offset、策略、静态成员、超时、Fetch、Batch 和组合校验；
- Hook nil、顺序、错误、panic 边界和 Managed 不变量保护；
- Raw/JSON/PB 的空值、Header、Key、Topic、编码错误和整数解码；
- Raw Tombstone、空 Key 拒绝、nil 与空字节差异，以及 JSON nil 编码为 `null`；
- Sonic 与 `encoding/json` 的结构体 Tag、omitempty、RawMessage、自定义 Marshaler、非法 JSON、整数、空值、
  NaN/Inf 和循环引用差异；
- Delivery 完成一次、重复读取、Context 取消、调用方放弃 Wait 和结果释放；
- Producer Success/Error Channel 排空、过载、部分批量接受和停止 Drain；
- 提交队列消息/字节上限、FIFO、容量预算持有到 Delivery、并发准入与停止封口竞争；
- Consumer 单条/批量同 Partition 顺序、成功后 Mark、失败不 Mark、重试上限、Session 取消和 Rebalance；
- Service Dispatch 队列满、Handler panic 隔离、Await 可用和 Handler 执行协程；
- 启动部分失败逆序清理、Client/Producer/Consumer goroutine 回收和重复关闭。

内部 Sarama 边界可使用最小测试替身，不为测试扩大公共 Factory API。能够稳定触发的公开行为分支尽量达到
100% 覆盖；无法在普通环境稳定触发的 Broker/OS 故障分支记录集成证据和剩余风险。

### 16.2 Ubuntu 真实 Kafka 集成测试

Docker Kafka 必须覆盖：

- Metadata、Admin 创建/查询 Topic、自动建 Topic 确实关闭；
- Raw、JSON、PB 单条与批量同步/异步生产；
- Success、权限/Topic 错误、消息过大、Context 超时和部分异步接受；
- Key 分区稳定性、同 Partition 顺序、Header、Offset 和压缩；
- 单条/批量 Consumer、Group Offset、重启重投、Handler 失败不提交；
- 两个 Consumer 实例触发 Rebalance、Cooperative Sticky 和停止时 Claim 回收；
- Broker 短暂重启后的有界退避恢复，不积累 goroutine 或未界定内存；
- Service 队列过载、Pause/Resume、Stats、LastError；
- TLS/SASL 通过独立受控配置验证；若单节点基础环境不启用安全协议，必须另建明确的安全 Profile，不能
  把未验证写成支持结论；
- `go test -race`、goroutine 泄漏、重复 Start/Stop 和 Example 完整运行。

### 16.3 Windows 验证

Windows 执行：

- 全部不依赖 Kafka 的单元测试、GoDoc Example、`go vet`、全仓构建；
- 连接 Ubuntu Kafka 的 Raw/JSON/PB 生产与 Consumer Group Smoke Test；
- Advertised Listener、局域网地址、Context、关闭和配置路径测试；
- Example 编译并至少完整运行 Producer 与 Consumer 主流程。

### 16.4 服务自工作流测试

必须新增端到端场景：

1. RPC/模拟 RPC 进入 Service；
2. Service 更新自身串行业务状态；
3. 通过 Await + ProduceSync 发送 Kafka 并返回；
4. 另一 Consumer 收到消息并派发回其所属 Service；
5. Handler 内访问 Service 状态并使用 Await 模拟持久化；
6. 断言没有跨 goroutine 直接访问非并发安全 Service 数据。

这项测试验证 Origin 机制与 Kafka 的完整闭环，不只验证 Sarama 能收发字节。

### 16.5 统一门禁

实施完成前至少执行：

```text
gofmt
go vet ./...
go test ./... -count=1
go test -race ./... -count=1        # Ubuntu
go test ./... -coverprofile=...
go build ./...
go build ./examples/17-kafka/...
```

发现竞态、goroutine 泄漏、不稳定测试、未解释低覆盖、消息静默丢失或 Example 与教程不一致时，不得标记
Kafka Module 完成。

## 17. 性能与低延迟原则

- 一个 Producer/Consumer Module 生命周期只创建一个 Sarama Client，不按请求创建连接；
- Managed Producer 只使用一个异步内核，不维护 Sync/Async 双连接；
- 只增加实现非阻塞准入所必需的一层 Producer 有界提交队列，不增加消费队列、序列化 Worker Pool 或
  每消息 goroutine；
- Raw 热路径在安全所有权下转移 Buffer，不默认复制 Payload；
- JSON/PB 为快照安全在调用方编码一次，不重复 Marshal；
- Consumer 只进行 Claim -> Service 一次必要调度，不再经过额外 Worker 队列；
- 批量按数量、字节和时间三重上限，不允许无限等待或无限增长；
- 首批不增加消息对象池、Delivery 池或 Buffer Pool；只有 Profile 证明 GC 是真实热点后再评估；
- 压缩算法、Flush 和 Fetch 参数必须用真实小消息/普通消息/最大消息压测，不能照搬通用“最佳配置”；
- 日志、Interceptor 和指标不得在热路径同步执行慢 I/O。

首批实现保存可重复的包装层微基准；需要真实 Broker 压力、完整 P50/P95/P99 或 Profile 时，另建独立性能
验收，不把局域网单节点延迟当作生产容量结论。当前微基准至少覆盖：

- Raw 单条异步入队；
- Raw/JSON/PB 单条编码与提交；
- 10、100、1000 条批量；
- Sonic 与标准库的游戏消息 Marshal；goccy 只作为选型调研候选，不因比较而引入运行时依赖；
- Delivery 创建/完成/等待；
- 提交队列消息/字节预算的准入与释放；
- `ns/op`、`B/op`、`allocs/op`。

Consumer Claim 到 Service Handler、不同消息尺寸、峰值积压、过载和 Broker 恢复的 P50/P95/P99 必须使用
专用性能环境、固定数据集与采样工具测试，不在功能集成测试中伪造结论。

性能优化必须由 Benchmark/Profile/Trace 支持。若对象池或更少复制会显著增加所有权和错误风险，先保留
简单安全实现并记录数据，再单独讨论。

## 18. 明确不实现

首批不实现：

- Origin v2 API、别名、拼写和行为兼容；
- 同一个 Managed Module 同时持有 Producer 与 Consumer；
- Managed 内部 Sarama Producer/ConsumerGroup/Channel 借用；
- Schema Registry、Avro、MessagePack 和自定义 Codec Registry；
- Topic 路由框架、自动 Topic 推断、缺少 Topic 的业务对象批量发送；
- 自动创建、修改或删除 Topic；
- Outbox、Inbox、Saga、全局事件总线和业务消息 Repository；
- 自动死信、自动 Skip、无限 Handler 重试和内存离线队列；
- Transaction/EOS Managed 包装、跨 Kafka 与数据库 Exactly Once 承诺；
- Consumer 正则 Topic、手工 Offset 存储和低级 Partition Consumer 包装；
- OAuth、Kerberos、AWS MSK IAM 普通配置外观；
- 运行期热更新 Brokers、Topic、Group 或安全凭证；
- Origin 自有连接池、消息池、Buffer Pool、序列化 Worker Pool；
- 进程全局 Producer/Consumer Registry。

特殊需求仍可通过 Config Builder 和业务自己拥有的 IBM Sarama 对象实现；形成稳定公共需求后再回到设计。

## 19. 设计 Review 清单

实施前和实现完成后都必须逐项复核：

1. Producer/Consumer 分离是否仍符合真实 Service 组合，没有为了共享连接重新引入全局所有权；
2. Raw/JSON/PB 单条与批量接口名称、参数和返回值是否完全对称且易发现；
3. 每条批量消息是否显式 Topic，是否清楚说明跨 Topic/Partition 不原子；
4. ProduceAsync 是否通过消息数和字节数双上限立即过载，Sarama 无缓冲 Input 是否只由 submit goroutine
   访问；
5. JSON/PB 是否先编码形成快照，Raw 所有权转移是否在 GoDoc 与教程可见；
6. Success/Error Channel 是否始终排空，停止是否能回收全部 goroutine；
7. Consumer Handler 是否只在 Service 工作协程访问业务状态，Claim goroutine 是否只负责传输和等待；
8. Handler 成功前是否绝不 Mark，失败/重平衡/过载是否会重投而不是静默丢弃；
9. Config 默认值是否有来源、范围和调优依据，Driver 默认变化是否被显式隔离；
10. TLS/SASL/错误/日志是否不泄漏凭证和 Payload；
11. Managed 不变量是否不能被 Hook 破坏，自由层所有权是否讲清；
12. 事务/EOS、死信、Schema Registry 等未实现范围是否没有被示例暗示为已保证；
13. Windows 与 Ubuntu Docker 的真实测试、Race、覆盖率、Benchmark 和故障恢复是否都有证据；
14. 教程是否按三个使用层级组织，并逐个说明 API/参数的执行 goroutine；
15. 三组 Example 是否完整可运行、包含失败路径并从业务 Module 角度组织；
16. 设计、代码、GoDoc、测试、Example、指南和 v3.2 索引是否保持一致。

## 20. 实施顺序与完成条件

Kafka 已按确认的 v3.2 顺序完成实施：

1. 本文完成并 Review；
2. 实现并验收 MongoDB Module；
3. 实现并验收 Redis Module；
4. 制定 Kafka 详细实施计划；
5. 在 Ubuntu 安装并保留 Docker Kafka；
6. 实现 Kafka Producer；
7. 实现 Kafka Consumer；
8. 完成教程、Example、双平台集成、性能和故障测试；
9. 对 Kafka 代码与文档做最终 Review并提交。

Kafka 切片只有同时满足以下条件才算完成：

1. IBM Sarama、Sonic 和 SCRAM 依赖版本、许可证、Go/OS/CPU 支持已复核固定；
2. 本文冻结的配置、Producer、Consumer、Delivery、Codec、状态和自由层全部实现，无兼容 API；
3. 所有导出标识符具有完整中文 GoDoc，复杂方法有可编译 Example；
4. 三个使用层级、配置表、协程表、可靠性与故障处理全部进入使用指南；
5. 三组完整 Example 可以独立构建和运行，覆盖成功与失败路径；
6. 重点公开行为分支尽量达到 100% 覆盖，例外逐项说明；
7. Windows 单元/构建与 Ubuntu Docker Kafka、`-race`、重平衡、恢复测试通过；
8. Producer 提交队列、Consumer 资源所有权、停止 Drain、部分启动失败和 goroutine 回收有测试；
9. Benchmark 没有发现包装层造成不合理分配、复制、调度或尾延迟；
10. Ubuntu Kafka Container、Network 和 Volume 保留，并在验收报告说明后续使用方法；
11. 根 README、v3.2 索引、设计、代码、测试、Example、指南和验收报告一致。

## 21. 参考资料

- [IBM Sarama GitHub](https://github.com/IBM/sarama)
- [IBM Sarama v1.60.1 Release](https://github.com/IBM/sarama/releases/tag/v1.60.1)
- [Sarama Configuration](https://github.com/IBM/sarama/blob/v1.60.1/config.go)
- [Sarama Async Producer](https://github.com/IBM/sarama/blob/v1.60.1/async_producer.go)
- [Sarama Consumer Group](https://github.com/IBM/sarama/blob/v1.60.1/consumer_group.go)
- [Apache Kafka Producer Configuration](https://kafka.apache.org/documentation/#producerconfigs)
- [Apache Kafka Consumer Configuration](https://kafka.apache.org/documentation/#consumerconfigs)
- [Apache Kafka Delivery Semantics](https://kafka.apache.org/documentation/#semantics)
- [Apache Kafka Design: Consumer Position](https://kafka.apache.org/documentation/#design_consumerposition)
- [ByteDance Sonic](https://github.com/bytedance/sonic)
- [ByteDance Sonic v1.15.2](https://github.com/bytedance/sonic/releases/tag/v1.15.2)
- [goccy/go-json](https://github.com/goccy/go-json)
