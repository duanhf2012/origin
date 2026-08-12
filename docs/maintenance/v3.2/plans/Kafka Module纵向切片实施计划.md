# Kafka Module 纵向切片实施计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 在 Origin v3.2 交付生产可用的 IBM Sarama Producer/Consumer Module、Raw/JSON/PB 外观、Origin Service 协程集成、自由 Sarama 配置层、完整教程与双平台真实 Kafka 验收。

**Architecture:** Producer 与 Consumer 是两个独立 Module，一个 Module 对应一个逻辑 Kafka 集群。Managed Producer 只拥有一个 AsyncProducer 内核和一层消息数/字节双有界提交队列；Managed Consumer 由 Sarama Claim 背压到所属 Service FIFO，业务 Handler 只在 Service 串行工作协程运行。长尾、Admin 和事务场景通过 Config Builder 创建由业务自行拥有的 Sarama 对象。

**Tech Stack:** Go 1.26.5、`github.com/IBM/sarama v1.60.1`、`github.com/bytedance/sonic v1.15.2`、`github.com/xdg-go/scram v1.2.0`、`google.golang.org/protobuf`、Docker Compose KRaft Kafka。

## Global Constraints

- 目标版本固定为 Origin v3.2，不保留 Origin v2 Kafka API、命名或行为兼容层。
- 外观接口以本计划和已确认核心设计为准；Sarama 类型只在 Config Hook、Builder 和自由模式边界出现。
- 所有等待 API 拒绝 nil Context；异步生产不接收 Context，并在调用者 goroutine 完成 JSON/PB 编码。
- Managed Producer 只有一个 Sarama Client、一个 AsyncProducer、一个 submit goroutine 和一个 completion goroutine。
- Producer 提交容量同时受消息数和 Payload 字节数硬限制，容量持有到 Delivery 完成。
- Consumer 不增加第二层消息队列；同 Partition 串行等待 Handler，成功后 Mark，失败不 Mark。
- 所有导出标识符必须有完整中文 GoDoc；复杂外观提供可编译 Example。
- 不实现自动建 Topic、死信、无限重试、Schema Registry、Outbox、Managed Kafka 事务/EOS、对象池或全局 Registry。
- Windows 与 Ubuntu 都执行单测、构建和真实收发；Ubuntu Kafka Container、Network、Volume 验收后保留。
- 每个生产行为严格执行 RED→GREEN→REFACTOR；没有先看到预期失败，不写对应实现。

---

### Task 1: 依赖、配置与 Sarama Builder

**Files:**
- Modify: `go.mod`
- Create: `sysmodule/kafkamodule/doc.go`
- Create: `sysmodule/kafkamodule/errors.go`
- Create: `sysmodule/kafkamodule/config.go`
- Create: `sysmodule/kafkamodule/option.go`
- Create: `sysmodule/kafkamodule/sarama_config.go`
- Test: `sysmodule/kafkamodule/config_test.go`

**Interfaces:**
- Produces: `ClusterConfig`、`TLSConfig`、`SASLConfig`、`ProducerConfig`、`ConsumerConfig`、`BatchConfig`。
- Produces: `BuildProducerSaramaConfig(ProducerConfig, ...ProducerOption)`、`BuildConsumerSaramaConfig(ConsumerConfig, ...ConsumerOption)`、`BuildAdminSaramaConfig(ClusterConfig, ...SaramaConfigOption)`。
- Produces: `WithProducerSaramaConfig`、`WithConsumerSaramaConfig`、`WithSaramaConfig`。

- [x] **Step 1: 固定依赖和许可证事实**

Run:

```powershell
go list -m -json github.com/IBM/sarama@latest
go list -m -json github.com/bytedance/sonic@latest
go list -m -json github.com/xdg-go/scram@latest
go mod download github.com/IBM/sarama@v1.60.1 github.com/bytedance/sonic@v1.15.2 github.com/xdg-go/scram@v1.2.0
```

Expected: 版本分别为 `v1.60.1`、`v1.15.2`、`v1.2.0`，许可证文件可读，Go 版本不高于 Origin 当前工具链。

- [x] **Step 2: 先写默认值、非法组合、TLS/SASL、Hook 不变量失败测试**

```go
func TestProducerConfigRejectsBrokenIdempotence(t *testing.T) {
    current := validProducerConfig()
    current.RequiredAcks = "one"
    _, err := BuildProducerSaramaConfig(current)
    if !errors.Is(err, ErrInvalidConfig) { t.Fatal(err) }
}

func TestConsumerConfigRejectsHeartbeatAtSessionBoundary(t *testing.T) {
    current := validConsumerConfig()
    current.HeartbeatInterval = current.SessionTimeout
    _, err := BuildConsumerSaramaConfig(current)
    if !errors.Is(err, ErrInvalidConfig) { t.Fatal(err) }
}
```

- [x] **Step 3: 运行 RED**

Run: `go test ./sysmodule/kafkamodule -run 'TestProducerConfig|TestConsumerConfig' -count=1`

Expected: FAIL，因为配置类型和 Builder 尚不存在。

- [x] **Step 4: 实现严格配置与 Builder**

```go
func BuildProducerSaramaConfig(input ProducerConfig, options ...ProducerOption) (*sarama.Config, error) {
    current, err := normalizeProducerConfig(input)
    if err != nil { return nil, err }
    result, err := buildClusterSaramaConfig(current.Cluster)
    if err != nil { return nil, err }
    applyProducerConfig(result, current)
    if err = applyProducerOptions(result, options); err != nil { return nil, err }
    if err = validateManagedProducerConfig(result, current); err != nil { return nil, err }
    return result, result.Validate()
}
```

TLS 追加系统 CA，mTLS Cert/Key 成对加载；SCRAM 使用 xdg-go 客户端生成器；Hook 执行后再次验证 Success/Error Channel、幂等、事务和安全不变量。

- [x] **Step 5: 验证 GREEN 和配置覆盖率**

Run: `go test -race -cover ./sysmodule/kafkamodule -run 'Test.*Config|Test.*TLS|Test.*SASL|Test.*Hook' -count=1`

Expected: PASS；配置标准化、错误组合和 Hook 顺序全部可重复。

- [x] **Step 6: Commit**

```powershell
git add go.mod go.sum sysmodule/kafkamodule
git commit -m "feat(v3.2): 建立 Kafka 配置与 Sarama Builder"
```

### Task 2: 消息、Codec 与 Delivery

**Files:**
- Create: `sysmodule/kafkamodule/message.go`
- Create: `sysmodule/kafkamodule/codec.go`
- Create: `sysmodule/kafkamodule/delivery.go`
- Create: `sysmodule/kafkamodule/stats.go`
- Test: `sysmodule/kafkamodule/message_test.go`
- Test: `sysmodule/kafkamodule/codec_test.go`
- Test: `sysmodule/kafkamodule/delivery_test.go`
- Test: `sysmodule/kafkamodule/example_test.go`
- Test: `sysmodule/kafkamodule/benchmark_test.go`

**Interfaces:**
- Produces: `Header`、`ProducerMessage`、`JSONMessage`、`PBMessage`、`Message`、`Metadata`、`DeliveryResult`、`Delivery`。
- Produces: `Message.DecodeJSON`、`Message.DecodePB`、`Delivery.Wait/Done/Result`。
- Produces internal: `encodeRaw`、`encodeJSON`、`encodePB`、`messageBytes`、`newDelivery`、`complete`。

- [x] **Step 1: 写消息语义和 Delivery 一次完成测试**

```go
func TestRawTombstoneRequiresKey(t *testing.T) {
    _, err := encodeRaw(ProducerMessage{Topic: "player-state", Value: nil})
    if !errors.Is(err, ErrInvalidArgument) { t.Fatal(err) }
}

func TestDeliveryCompletesExactlyOnce(t *testing.T) {
    delivery := newDelivery()
    delivery.complete(DeliveryResult{Metadata: Metadata{Offset: 7}})
    delivery.complete(DeliveryResult{Err: errors.New("late")})
    result, ok := delivery.Result()
    if !ok || result.Metadata.Offset != 7 || result.Err != nil { t.Fatalf("%+v", result) }
}
```

- [x] **Step 2: 运行 RED**

Run: `go test ./sysmodule/kafkamodule -run 'TestRaw|TestJSON|TestPB|TestDelivery|TestDecode' -count=1`

Expected: FAIL，因为消息与 Delivery 尚不存在。

- [x] **Step 3: 实现明确所有权和 Sonic/PB Codec**

```go
func (message *Message) DecodeJSON(destination any) error {
    if message == nil || destination == nil { return ErrInvalidArgument }
    return jsonAPI.Unmarshal(message.Value, destination)
}

func (delivery *Delivery) Wait(ctx context.Context) (Metadata, error) {
    if ctx == nil || delivery == nil { return Metadata{}, ErrInvalidArgument }
    select {
    case <-delivery.done:
        result, _ := delivery.Result()
        return result.Metadata, result.Err
    case <-ctx.Done():
        return Metadata{}, ctx.Err()
    }
}
```

Raw 接受后不复制；JSON/PB 在调用方编码为稳定快照；消费解码拒绝 typed nil；`map[string]any` 整数保持 int64。

- [x] **Step 4: 验证 GREEN、差异测试与基准**

Run:

```powershell
go test -race ./sysmodule/kafkamodule -run 'TestRaw|TestJSON|TestPB|TestDelivery|TestDecode' -count=1
go test ./sysmodule/kafkamodule -run none -bench 'Benchmark(JSON|PB|Delivery)' -benchmem -count=3
```

Expected: PASS；保存 Sonic/标准库/goccy 的真实游戏事件差异和性能结果，不基于单一微型结构下结论。

- [x] **Step 5: Commit**

```powershell
git add sysmodule/kafkamodule
git commit -m "feat(v3.2): 完成 Kafka 消息编解码与 Delivery"
```

### Task 3: Managed Producer 内核与外观

**Files:**
- Create: `sysmodule/kafkamodule/producer_queue.go`
- Create: `sysmodule/kafkamodule/producer_runtime.go`
- Create: `sysmodule/kafkamodule/producer.go`
- Test: `sysmodule/kafkamodule/producer_queue_test.go`
- Test: `sysmodule/kafkamodule/producer_test.go`

**Interfaces:**
- Produces: `Producer.Setup/OnInit/OnStart/OnStop/Stats`。
- Produces: Raw/JSON/PB 的 `ProduceSync`、`ProduceAsync`、`ProduceBatchSync`、`ProduceBatchAsync` 对称外观。
- Consumes: Task 1 Sarama Config、Task 2 编码和 Delivery。

- [x] **Step 1: 写双有界队列、部分接受和关闭竞争失败测试**

```go
func TestProducerQueueHoldsBudgetUntilDelivery(t *testing.T) {
    queue := newSubmitQueue(1, 128)
    first := testEnvelope(96)
    if err := queue.trySubmit(first); err != nil { t.Fatal(err) }
    if err := queue.trySubmit(testEnvelope(1)); !errors.Is(err, errs.ErrTransportOverloaded) { t.Fatal(err) }
    queue.complete(first)
    if err := queue.trySubmit(testEnvelope(1)); err != nil { t.Fatal(err) }
}
```

生命周期替身必须覆盖创建 Client 成功但 Producer 失败、启动中 Stop、Drain 超时、Success/Error 同时到达、重复 Stop 和并发 Stop。

- [x] **Step 2: 运行 RED**

Run: `go test ./sysmodule/kafkamodule -run 'TestProducer|TestSubmitQueue' -count=1`

Expected: FAIL，因为 Producer 内核尚不存在。

- [x] **Step 3: 实现单异步内核**

```go
func (producer *Producer) ProduceAsync(message ProducerMessage) (*Delivery, error) {
    envelope, err := producer.prepareRaw(message)
    if err != nil { return nil, err }
    if err = producer.queue.trySubmit(envelope); err != nil { return nil, err }
    producer.stats.accepted.Add(1)
    return envelope.delivery, nil
}
```

submit goroutine 独占写 `AsyncProducer.Input()`；completion goroutine 持续排空 Success/Error，按 Metadata 找到 Envelope，完成 Delivery 后释放计数和字节预算。Stop 先封口，再 Drain；Context 到达时关闭 Client 中断 I/O，清理继续且结果可复用。

- [x] **Step 4: 实现同步/批量/JSON/PB 对称层**

同步方法只复用异步准入和 `Delivery.Wait`；同步批量始终返回与输入等长结果；异步批量错误包含 `Accepted` 数量且不尝试撤回已接受消息。

- [x] **Step 5: 验证 GREEN、竞态和无 goroutine 泄漏**

Run:

```powershell
go test -race ./sysmodule/kafkamodule -run 'TestProducer|TestSubmitQueue' -count=100
go test ./sysmodule/kafkamodule -run TestProducer -coverprofile=producer.cover -count=1
```

Expected: PASS；队列热路径、生命周期和停止分支无竞态、无重复完成、无容量泄漏。

- [x] **Step 6: Commit**

```powershell
git add sysmodule/kafkamodule
git commit -m "feat(v3.2): 完成 Kafka Managed Producer"
```

### Task 4: Producer 与 Origin Service 完成回调

**Files:**
- Modify: `sysmodule/kafkamodule/producer.go`
- Test: `sysmodule/kafkamodule/producer_dispatch_test.go`

**Interfaces:**
- Produces: `DeliveryHandler`、`Producer.DispatchDelivery`、`Producer.DispatchDeliveries`。
- Consumes: `service.DispatchAsyncCompletion`，不创建每 Delivery goroutine。

- [x] **Step 1: 写 Service 队列预留、取消和串行协程失败测试**

```go
func TestDispatchDeliveryRunsHandlerInOwnerService(t *testing.T) {
    fixture := startServiceFixture(t)
    delivery := completedDelivery(Metadata{Offset: 9}, nil)
    called := make(chan bool, 1)
    err := fixture.producer.DispatchDelivery(context.Background(), delivery, func(ctx context.Context, result DeliveryResult) {
        called <- fixture.service.IsCurrentTask(ctx) && result.Metadata.Offset == 9
    })
    if err != nil || !<-called { t.Fatalf("%v", err) }
}
```

- [x] **Step 2: 运行 RED**

Run: `go test ./sysmodule/kafkamodule -run TestDispatchDelivery -count=1`

Expected: FAIL，因为 Dispatch 外观尚不存在。

- [x] **Step 3: 复用 Service completion 原语实现**

```go
return service.DispatchAsyncCompletion(producer.Service(), ctx,
    func(waitCtx context.Context) error {
        metadata, err := delivery.Wait(waitCtx)
        result = DeliveryResult{Metadata: metadata, Err: err}
        return nil
    },
    func(taskCtx context.Context, _ error) { handler(taskCtx, result) },
)
```

批量只预留一个根任务、顺序等待全部 Delivery，并保证 Handler 严格一次。

- [x] **Step 4: 验证 GREEN**

Run: `go test -race ./sysmodule/kafkamodule -run TestDispatchDeliver -count=100`

Expected: PASS；Service 队列满时立即返回原始错误，Delivery 仍可由调用方处理。

- [x] **Step 5: Commit**

```powershell
git add sysmodule/kafkamodule
git commit -m "feat(v3.2): 接入 Kafka Delivery Service 回调"
```

### Task 5: Managed Consumer、批量与 Origin Service Handler

**Files:**
- Create: `sysmodule/kafkamodule/consumer_runtime.go`
- Create: `sysmodule/kafkamodule/consumer_handler.go`
- Create: `sysmodule/kafkamodule/consumer.go`
- Test: `sysmodule/kafkamodule/consumer_handler_test.go`
- Test: `sysmodule/kafkamodule/consumer_test.go`

**Interfaces:**
- Produces: `Handler`、`Batch`、`BatchHandler`、`Consumer.Setup/SetupBatch/OnInit/OnStart/OnStop`。
- Produces: `PauseAll/ResumeAll/Pause/Resume/Stats/LastError`。
- Consumes: Task 1 Consumer Config、Task 2 Message、Origin `Service.DispatchAsync` 和 Handler 内 `Await`。

- [x] **Step 1: 写成功 Mark、失败不 Mark、同 Partition 顺序和批次边界失败测试**

```go
func TestConsumerMarksOnlyAfterServiceHandlerSuccess(t *testing.T) {
    session, claim := fakeClaimWithOffsets(10, 11)
    handler := newManagedGroupHandler(owner, func(context.Context, *Message) error { return nil })
    if err := handler.ConsumeClaim(session, claim); err != nil { t.Fatal(err) }
    if !reflect.DeepEqual(session.markedOffsets(), []int64{10, 11}) { t.Fatal(session.markedOffsets()) }
}
```

另外覆盖 Service 队列满、Handler panic、Handler 有界重试、Session 撤销丢弃半批、`max_messages/max_size/max_wait` 和 Claim 收尾。

- [x] **Step 2: 运行 RED**

Run: `go test ./sysmodule/kafkamodule -run 'TestConsumer|TestBatch|TestClaim' -count=1`

Expected: FAIL，因为 Consumer 尚不存在。

- [x] **Step 3: 实现 Claim 到 Service 的一次派发**

Claim goroutine 提交一个 Service 根任务并等待结果 Channel；Handler 在 Service task Context 中执行；成功后 Claim goroutine 调用 `MarkMessage`。失败保存错误、取消当前 Managed Consumer，不 Mark、不 Skip。

- [x] **Step 4: 实现 Consumer Group 恢复和生命周期**

单一 Consume 循环等待首个 `Setup` Ready；认证/授权/配置错误停止，基础设施错误按带抖动的有界退避恢复；每轮 Consume 结束后确认旧 Session/Claim 全部退出再进入下一轮。

- [x] **Step 5: 实现 Pause/Resume、Stats、LastError**

只借用内部 ConsumerGroup 完成即时操作，不向使用者暴露；状态快照使用 atomic，LastError 用不可变错误引用和锁保护。

- [x] **Step 6: 验证 GREEN 和竞态**

Run:

```powershell
go test -race ./sysmodule/kafkamodule -run 'TestConsumer|TestBatch|TestClaim' -count=100
go test ./sysmodule/kafkamodule -run TestConsumer -coverprofile=consumer.cover -count=1
```

Expected: PASS；没有跨 goroutine 访问 Service 串行状态、无已撤销 Session Mark、无重试 goroutine 累积。

- [x] **Step 7: Commit**

```powershell
git add sysmodule/kafkamodule
git commit -m "feat(v3.2): 完成 Kafka Managed Consumer"
```

### Task 6: Ubuntu KRaft Kafka 与真实协议验收

**Files:**
- Create: `deploy/kafka/compose.yaml`
- Create: `deploy/kafka/.env.example`
- Create: `deploy/kafka/create-topics.sh`
- Create: `deploy/kafka/README.md`
- Modify: `sysmodule/kafkamodule/integration_test.go`

**Interfaces:**
- Produces persistent environment: 局域网 Listener、持久 Volume、健康检查、自动建 Topic 关闭。
- Produces integration env vars: `ORIGIN_KAFKA_BROKERS`、Topic/Group 前缀。

- [x] **Step 1: 只读审计 Ubuntu 现有 Kafka**

Run:

```powershell
ssh boyce@192.168.8.3 "docker ps -a; docker volume ls; ss -lnt"
```

Expected: 记录现有名称、端口和 Volume；不删除、不覆盖不属于本任务的资源。

- [x] **Step 2: 固定官方稳定 Kafka 镜像并提交 Compose**

Compose 使用单节点 KRaft、持久命名 Volume、容器内与局域网双 Listener、`restart: unless-stopped`、健康检查、`auto.create.topics.enable=false`，消息上限与测试配置一致。

- [x] **Step 3: 启动并保留环境**

Run: `docker compose -f deploy/kafka/compose.yaml up -d`

Expected: Broker 健康；创建 Raw/JSON/PB/compacted/consumer-test Topic；不执行 `down`。

- [x] **Step 4: 写真实协议失败测试并运行 RED**

先添加 Metadata、Raw/JSON/PB、批量、Header、Tombstone、消息过大、自动建 Topic 关闭、Consumer Offset、重投、Pause/Resume、Rebalance 和恢复测试；在实现缺口存在时观察对应失败。

- [x] **Step 5: 修复到 GREEN 并执行故障恢复**

Run:

```bash
go test -race ./sysmodule/kafkamodule -count=1
docker restart origin-kafka
go test -race ./sysmodule/kafkamodule -run TestIntegrationRecovery -count=1
```

Expected: Producer/Consumer 有界恢复，无未完成 Delivery、Claim 或 goroutine 泄漏。

- [x] **Step 6: Windows 连接 Ubuntu Smoke**

Run: `go test ./sysmodule/kafkamodule -run TestIntegration -count=1`

Expected: 使用 Ubuntu Advertised Listener 完成 Raw/JSON/PB 与 Consumer Group；防火墙和地址记录到报告。

- [x] **Step 7: Commit**

```powershell
git add deploy/kafka sysmodule/kafkamodule
git commit -m "test(v3.2): 完成 Kafka Docker 集成验收"
```

### Task 7: 三组 Example 与使用指南

**Files:**
- Create: `examples/17-kafka/README.md`
- Create: `examples/17-kafka/01-producer-workflows/**`
- Create: `examples/17-kafka/02-consumer-service-handler/**`
- Create: `examples/17-kafka/03-managed-and-native/**`
- Create: `docs/maintenance/v3.2/guides/Kafka Module使用指南.md`
- Create: `docs/maintenance/v3.2/changes/Kafka Module纵向切片变更记录.md`
- Create: `docs/maintenance/v3.2/reports/Kafka Module纵向切片验收报告.md`
- Modify: `README.md`
- Modify: `examples/README.md`
- Modify: `docs/maintenance/v3.2/README.md`

**Interfaces:**
- Documents all public APIs, parameters, goroutines, ownership, Context ambiguity, reliability and configuration.
- Demonstrates managed Producer/Consumer and native Sarama ownership without implying EOS.

- [x] **Step 1: 先写可编译 Example 测试**

GoDoc Example 覆盖 Raw/JSON/PB 同步与异步、批量 Topic、Delivery、Dispatch、单条/批量 Handler、Decode、Pause/Resume 和 Builder。

- [x] **Step 2: 写完整业务 Example**

`01` 覆盖 RPC 风格生产流程与失败分支；`02` 覆盖 Service 串行消费、Await、幂等和重投；`03` 覆盖 Managed、Hook、Admin 与业务自有 Client 生命周期。每个目录包含带注释 YAML、README、`run.bat`、`run.sh`。

- [x] **Step 3: 写使用者视角指南**

顺序固定为：十分钟接入 → 三个使用层级 → Producer/Consumer 配置全表 → API/参数/goroutine 表 → 所有权 → 可靠性 → 游戏场景 → 故障排查 → 性能调优 → Windows/Ubuntu。

- [x] **Step 4: 运行文档与 Example 门禁**

Run:

```powershell
go test ./examples/17-kafka/... ./sysmodule/kafkamodule -count=1
go build ./examples/17-kafka/...
go vet ./examples/17-kafka/... ./sysmodule/kafkamodule
```

Expected: PASS；相对链接存在，配置字段均有必填/默认/建议注释，无真实凭证。

- [x] **Step 5: Ubuntu 完整实跑三个 Example**

Expected: Producer 记录 Delivery；Consumer 在 Service 协程处理并展示幂等；Native 示例显式关闭 Admin/Client；进程优雅停止。

- [x] **Step 6: Commit**

```powershell
git add README.md examples docs/maintenance/v3.2
git commit -m "docs(v3.2): 完成 Kafka 教程与业务示例"
```

### Task 8: 性能、最终 Review 与统一门禁

**Files:**
- Modify: `sysmodule/kafkamodule/benchmark_test.go`
- Modify: `docs/maintenance/v3.2/design/Origin Kafka Module核心设计.md`
- Modify: `docs/maintenance/v3.2/plans/Kafka Module纵向切片实施计划.md`
- Modify: `docs/maintenance/v3.2/reports/Kafka Module纵向切片验收报告.md`

**Interfaces:**
- Produces final evidence: coverage、race、benchmark、recovery、container inventory、review findings。

- [x] **Step 1: 执行设计逐项回查**

逐条核对核心设计第 19、20 节，特别检查单异步内核、字节预算持有、Handler Mark 时机、停止 Drain、Hook 不变量、自由层所有权、事务/死信未承诺。

- [x] **Step 2: 运行 Benchmark/Profile**

Run:

```bash
go test ./sysmodule/kafkamodule -run none -bench Benchmark -benchmem -benchtime=1s -count=3
```

保存 Raw/JSON/PB、10/100/1000 批量、Delivery、Service 派发和积压/过载结果；没有证据不增加对象池或额外复制优化。

- [x] **Step 3: 执行 Windows 最终门禁**

Run:

```powershell
gofmt -w sysmodule/kafkamodule examples/17-kafka
go test -race ./sysmodule/kafkamodule -count=1
go test ./... -count=1
go vet ./...
go build ./...
git diff --check
```

Expected: 全部 exit 0。

- [x] **Step 4: 执行 Ubuntu 最终门禁**

Run:

```bash
go test -race ./... -count=1
go test ./... -coverprofile=/tmp/origin-kafka.cover -count=1
go vet ./...
go build ./...
```

Expected: 全部 exit 0；报告真实覆盖率与无法稳定触发的分支，不用无意义 Mock 伪造 100%。

- [x] **Step 5: 请求独立代码审查并处理发现**

审查范围包含代码、测试、Docker、教程和三个 Example；Critical/Important 必须修复并重跑相应门禁。

- [x] **Step 6: 核对 Kafka 环境保持运行**

Run: `docker ps --filter name=origin-kafka`

Expected: Kafka 健康，Container/Network/Volume 未删除，后续连接方式已写入报告。

- [x] **Step 7: 独立提交最终收口**

```powershell
git add sysmodule/kafkamodule deploy/kafka examples/17-kafka docs/maintenance/v3.2 README.md examples/README.md go.mod go.sum
git commit -m "feat(v3.2): 完成 Kafka Module 纵向切片"
```
