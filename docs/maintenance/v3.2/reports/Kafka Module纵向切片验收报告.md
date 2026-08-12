# Kafka Module 纵向切片验收报告

> 验收日期：2026-08-12；分支：`v3`；实现驱动：IBM Sarama v1.60.1。

## 已验证范围

- 配置默认值、非法组合、TLS/mTLS、PLAIN/SCRAM 和 Hook 不变量；
- Raw/JSON/PB 编解码、int64 JSON、Tombstone、typed nil、Delivery 并发完成；
- Producer 双有界容量、部分批量接受、Drain、重复/并发停止；
- Consumer Service 协程、成功 Mark、失败不 Mark、panic、重试和批次三重边界；
- Kafka 4.3.1 真实协议、自动建 Topic 关闭、Pause/Resume、Offset 重投和 Broker 重启恢复；
- 双 Consumer Cooperative Sticky 加入/退出重平衡，以及 Service `Await -> Kafka -> Consumer Handler -> Await` 自工作流；
- Windows 与 Ubuntu 双平台 race、build、vet 和 Integration。

## 最终门禁

| 环境 | 命令 | 结果 |
| --- | --- | --- |
| Windows | `go test -race ./sysmodule/kafkamodule -count=1` | 通过，含真实 Kafka 集成 |
| Windows | `go test ./... -count=1`、`go vet ./...`、`go build ./...` | 全部通过 |
| Windows | Kafka Example test/build 与 Markdown 相对链接检查 | 全部通过 |
| Ubuntu | `CGO_ENABLED=1 go test -race ./... -count=1` | 全仓通过，含真实 Kafka 集成 |
| Ubuntu | `go test ./... -coverprofile=...`、`go vet ./...`、`go build ./...` | 全部通过 |
| Ubuntu | 三组 Kafka Example 实跑 | Delivery、Service Handler、Native Admin 均通过并优雅停止 |

Kafka Package 在 Ubuntu 最终覆盖执行中为 **81.2% statements**。配置标准化、消息校验/解码、Delivery、
受管队列、Handler 成功后 Mark、失败不 Mark、批量边界、Pause/Resume、重平衡和真实收发属于重点覆盖；
TLS 客户端证书文件故障、Broker/OS 极端中断排列等环境分支没有用无意义 Mock 伪造 100%，由严格校验、
race 集成、Broker 重启和显式剩余风险共同约束。

## 性能证据

Windows AMD Ryzen 7 7840HS，`-benchtime=1s -count=3` 的稳定区间：

| 路径 | ns/op | B/op | allocs/op |
| --- | ---: | ---: | ---: |
| Raw 准备 | 108～125 | 128 | 1 |
| JSON 准备（Sonic） | 539～572 | 396 | 5 |
| PB 准备 | 210～216 | 160 | 3 |
| Raw 10 / 100 / 1000 条准备 | 1.15～1.27µs / 11.25～11.45µs / 110～131µs | 每条约 128B | 每条 1 |
| 提交队列预算准入/释放 | 55～57 | 0 | 0 |
| Delivery 创建/完成/读取 | 229～231 | 240 | 2 |

Sonic 对同一游戏事件 Marshal 为 446～527ns、4 allocs，标准库为 659～744ns、7 allocs。结果支持保留
Sonic 与当前单队列设计；没有证据支持增加对象池、第二层 Consumer 队列或序列化 Worker Pool。真实 Broker
吞吐与 P95/P99 必须在生产相似的多 Broker 专用性能环境另测，本报告不以单节点局域网数据代替容量规划。

## Review 结论

- 按核心设计第 19、20 节复核了外观对称性、容量、所有权、Mark 时机、停止 Drain、Hook 不变量和自由层边界；
- Review 中补齐了 `BuildSaramaConfig` 自由入口、Pause 意图跨 Claim 重放、零时间戳处理、Hook panic 脱敏、
  导出字段 GoDoc、公共批量路径及重平衡/Service 自工作流测试；
- 独立只读 Review 未发现 Critical；3 个 Important 已全部修复：JSON/PB Key/Header 完整快照、超聚合阈值
  单条消息作为单元素批次交付、`idempotent` 可区分缺省与显式 `false`；1 个设计计费表述不一致也已同步。

## 保留环境

- Ubuntu：`192.168.8.3`
- Container：`origin-kafka`，镜像 `apache/kafka:4.3.1`
- 外部 Broker：`192.168.8.3:9092`
- Volume：`origin-kafka-data`
- Network：`origin-kafka_default`

验收流程不执行 `docker compose down`，以上资源保留给后续使用。

当前 Compose 为开发单节点明文环境。因为 Windows 与 Ubuntu 主机时间相差约 26 小时，开发配置把未来
CreateTime 容差临时放宽到 48 小时；生产必须使用 NTP 和 Kafka 默认边界，不能复制该容差。
