# Origin 第三版 M17 公共服务发现 Provider 与 Origin 内置发现设计

> 文档类型：里程碑设计
> 创建日期：2026-07-30
> 最后更新：2026-07-30
> 当前状态：已实现并通过验收
>
> 后续覆盖：Origin Discovery 已改为复用 Application RPC Transport；`server.node` 是唯一
> 引导信息，TCP 地址从该 Node 的 `rpc.tcp.advertise` 推导，NATS 使用保留系统 Subject。
> 本文有关独立 Discovery `listen/address` 或独立 Listener 的旧描述不再适用。

## 1. 里程碑目标

M17 在 M14 本地发现目录与 M16 完整生命周期之上建立第一套正式远端服务发现：

1. 提供一个小而稳定的公开 `discovery/provider` SPI；
2. 允许 Application 注册 Consul 等项目自定义 Provider，而不修改 Node、Directory、RPC
   或业务发现 API；
3. 每个 Node 独占 Provider 实例、连接、私有镜像和恢复资源；
4. 实现 Origin 自带的单实例 `DiscoveryService` 和客户端 Provider；
5. 在业务 `OnStart` 前完成发现同步和本地 RPC 基础设施准备；
6. 在全部 `OnStart` 成功后发布完整本地 Node，在停止时先撤销再排空业务；
7. 断线立即反映 Provider 健康，但只在一个 TTL 后仍未恢复时清空旧远端事实；
8. 提供公共 Provider 一致性测试以及 Origin Wire、容量、故障和资源验收。

公共契约、Origin 协议、状态机、逐字段 Wire 和完整验收矩阵以
[服务发现提供者设计](../details/2026-07-26-服务发现提供者设计.md)为唯一详细定义。本文只
冻结 M17 实施范围、前置依赖和开工门禁，不复制第二套协议正文。

## 2. 已确认的里程碑边界

### 2.1 M17 实现

- 一个公开 Provider 包、一个 Application 私有注册入口和一个公共契约测试包；
- Factory、Provider、Context、Config、Host、Snapshot、Node、Service、Report 和状态枚举；
- 框架统一的严格配置选择、快照校验、深复制、差异提交、旧快照 TTL 和状态发布；
- `Node.DiscoveryStatus()` 原子只读快照；
- Origin Provider 客户端的 Hello、同步、Publish、Withdraw、Heartbeat、Resync 和持续恢复；
- 保留名称 `DiscoveryService` 的控制 Listener、单 Actor 注册表、TTL、全量和 Delta；
- 共置 DiscoveryService Node 的基础设施 Prepare、自连接和严格回滚；
- M14 Directory、TCP/NATS 路由、Await 和 M16 HealthStatus 的正式 Provider 接入；
- M17 新增的稳定错误码、日志、指标和测试夹具。

### 2.2 M17 不实现

- etcd Client、Lease、Watch、认证和 TLS；这些属于 M18；
- Consul Provider；M17 只用映射矩阵和公共测试证明能够替换；
- Origin TLS、mTLS、Token 或 HMAC；
- 多 DiscoveryService、Leader 选举、复制、持久化或跨发现源合并；
- Go `.so`、运行期热切换、sidecar 协议或远程下载 Provider；
- 独立 `static` Provider；
- Service 级 Delta、无限历史、压缩、Checksum 或复杂 Wire 版本协商；
- 业务 RPC Transport Bridge。

## 3. 前置里程碑与复用边界

M17 必须复用而不能重写：

- M3 的严格 JSON/YAML 配置解码、来源定位、时间和容量规则；
- M5 的四字节长度分帧、TCP 连接双循环、写超时、发送队列和 Buffer 唯一所有权；
- M8 的 Node 系统计时能力和 DeadlineQueue；
- M13/M15 的 TCP/NATS RPC Runtime、SessionID、Transport 状态和路由；
- M14 的完整原始快照入口、不可变 Directory、稳定 Diff、关注筛选、监听和 Await；
- M16 的启动回滚、运行期持续恢复、HealthStatus、总体 Stop Context、Service finalizer 和
  资源严格反序释放；
- M0/M1/M2 的稳定错误、日志和 BufferPool。

M14 的进程内 `internal/discovery.Source` 是正式 Provider 出现前的过渡数据源。M17 接入后，
生产 Node 的远端事实必须只来自当前配置选择的 Provider；同进程 Node 也经过相同
Publish/权威快照路径，不能保留一条绕过 Provider、TTL 和 Session 规则的并行数据源。
M14 Directory 和业务只读 API 保持不变。

## 4. 公共扩展面

公开 SPI 固定为：

```go
type Factory func(Context) (Provider, error)

type Provider interface {
    Start(context.Context) error
    Publish(context.Context, Node) error
    Withdraw(context.Context) error
    Close(context.Context) error
}
```

Application 只增加：

```go
app.RegisterDiscoveryProvider(name, factory)
```

注册表属于单个 Application，必须在配置加载或启动前写入；内置 `origin`、`etcd` 名称不能
覆盖。Factory 通过 `Context.Config.Decode` 严格解码选中块；Provider 通过框架构造的
`Context.Host` 提交完整 Snapshot 和 Report。Provider 不取得 Application、Node、内部
Directory、RPC Runtime 或业务监听器。

第三方只实现一个 Factory 和一个 Provider。Host、Config、目录差异、TTL 旧快照、Readiness
和事件都由框架实现，不能把 Origin 的 Actor、Frame、Revision 或 TCP 连接模型泄漏到公共
接口。

## 5. Node 生命周期接入

M17 的启动顺序固定为：

1. 构造 Node、Service 绑定和静态配置；
2. 按配置顺序执行全部纯 `OnInit`；
3. 准备 TimerEngine、业务 TCP/NATS、RPC Client Runtime 和发现目录；
4. 共置 DiscoveryService 时先 Prepare 独立控制 Listener；
5. 创建当前 Node 独占的 Provider，完成 `Start` 和首个权威完整快照；
6. 安装 TCP 候选并开始拨号，但不等待全部远端连接；
7. 按原配置顺序执行全部业务 `OnStart`；
8. 有公开业务 Service 时 Publish 完整本地 Node 并等待 Ack；
9. 没有发布需求时把 Publication 标记为 `NotRequired`；
10. 满足 Transport、Provider、Service 和发布屏障后一次性进入 Ready。

任一步失败都按已经成功建立资源的严格逆序回滚，不重新执行任何 `OnInit` 或 `OnStart`。
同一 Application 启动多个 Node 时，`server.node` 必须在显式顺序第一位，框架不静默重排；
因此 Stop 时它自然最后停止。

正常停止先在 Stop Context 内 Withdraw 并等待 Ack，再停止新业务入站和执行 Service/Module
finalizer，最后关闭 Provider。DiscoveryService 的 Listener 和 Actor 保持到同一
Application 其他 Node 已停止；超时不跳过可安全完成的本地清理。

## 6. 公共状态和故障语义

`DiscoveryStatus()` 固定包含：

- Kind；
- `Starting/Ready/Recovering/Stopped`；
- Synchronized；
- `NotRequired/Pending/Published`；
- Reconnects；
- ConsecutiveFailures；
- ErrorCode。

查询使用不可变原子快照，无锁、无分配，不暴露 endpoint、凭据、动态 error、后端 revision
或远端节点。

Provider 控制连接或 Watch 中断时立即进入 Recovering，并令 Node Readiness 为 false。框架
从最后有效 Ack 起最多保留旧完整快照一个 TTL；TTL 后仍未恢复才提交空快照。主动 Withdraw、
Origin TTL 到期、权威快照删除、Session 替换等已确认事实立即产生 Lost，不增加业务防抖。
恢复后立即用完整权威快照对账，未变化实例不产生伪事件。

## 7. Origin 发现端

一个发现域只允许一个 `DiscoveryService`，但该 Service 可以与任意业务 Service 配置在同一
Node。Origin 控制通道使用独立 TCP，不复用业务 RPC 连接、Runtime 或 Wire；NATS 业务 Node
也只额外建立一条轻量控制连接。

DiscoveryService 使用：

- 一个系统 Actor 独占注册表、Session、Epoch、Revision、客户端和到期状态；
- 32768 条有界 Actor 命令；
- 一份 generation 最小堆处理 TTL，不创建每 Node Timer/goroutine；
- 直接复用 M5 每客户端 64 帧 FIFO 队列和独占 Writer；
- Actor 生成不可变消息，发送前复制到连接独占 M5 Buffer；
- 队列满时关闭慢连接，客户端重连后完整 Resync。

首次连接取得 FullSnapshot，后续只发送完整 Node Upsert 或 Node Delete。Epoch 改变、Revision
缺口或镜像非法时重新取得 FullSnapshot。新 ServerEpoch 只使用一次
`min(TTL/3, 5s)`、最低 `1s` 的 Warming 窗口；正常变化不防抖。

## 8. Origin Wire 与配置门禁

Wire 复用 M5 四字节长度帧，Payload 为 `FrameType uint8 + Body`。M17 冻结
`0x01～0x05` 客户端帧、`0x81～0x87` 服务端帧和 `0xFF Error`；Body 使用手写网络大端
编码、长度前缀字符串、稳定排序和解码前容量校验。协议不包含独立 WireVersion、Magic、
Checksum、Compression、Reserved、RequestID 或动态错误文本。

Origin 配置只允许：

```yaml
discovery:
  type: origin
  origin:
    ttl: 15s
    server:
      node: discovery-1
```

TTL 默认 `15s`、范围 `3s～5m`；心跳、Dial/Hello/写超时、重连退避和收敛窗口全部派生，
不增加配置字段。TCP 拨号地址由 `server.node` 指向的 `nodes[].rpc.tcp.advertise` 推导，
NATS 使用顶层 `rpc.nats` 的保留 Subject；不再允许 `server.listen/address`。M17 限定可信
内网，必须由防火墙、安全组或 NetworkPolicy 限制 RPC 端口；需要认证、TLS 或高可用时使用
M18 etcd Provider。

## 9. 固定容量和错误码

固定容量为：

| 资源 | 上限 |
|---|---:|
| 已发布 Node | 8192 |
| 控制连接 | 16384 |
| 单 Node 公开 Service | 256 |
| 全域公开 Service | 65536 |
| 单 Node Label | 32 |
| 编码后 Node | 256 KiB |
| FullSnapshot/控制 Body | 16 MiB |
| 每客户端待发送消息 | 64 |

M17 使用 `CodeDiscoveryUnavailable=5001`、`CodeDiscoveryDuplicateNode=5002`、
`CodeDiscoveryCapacity=5003`、`CodeDiscoverySnapshotInvalid=5004`。配置错误使用
`CodeInvalidConfig`；Wire 协议、消息过大和 M5 队列过载复用对应 Transport Code。Warming
不是错误。

## 10. 实施交付物

实施计划必须把以下交付物映射到明确包和测试：

1. 公开 `discovery/provider` SPI 和中文 GoDoc；
2. Application 私有 Provider 注册表与严格配置联合；
3. 每 Node Provider Runtime、Host、状态快照和 M14 Directory Adapter；
4. Origin 客户端状态机、私有镜像、重连、心跳和发布期望；
5. DiscoveryService 保留类型、Prepare Listener、Actor、TTL 堆和广播；
6. Origin Wire 编解码器及状态机；
7. `discovery/providertest` 公共一致性测试；
8. Origin 单元、Fuzz、真实 TCP、集成、容量、Race、Benchmark 和跨平台测试；
9. 配置示例、错误码表、日志/指标和设计回写。

不得为了先跑通 Origin 而建立临时公共接口、第二套目录写入口、包级 Provider 注册表或无界
重试/队列。实施发现公共契约无法覆盖 etcd/Consul 映射时必须先停止并重新 Review。

## 11. 验收门禁

M17 完成必须同时满足：

1. Origin、假 Provider 和至少一个最小第三方测试 Provider 通过 `providertest`；
2. Consul 配置、注册/注销、blocking query/index、健康和恢复可完整映射到冻结 SPI；
3. 首次 Full、Delta、Resync、Warming、Session 冲突/接管和 Ack 丢失行为通过真实 TCP；
4. 控制断线立即 Recovering，TTL 内恢复无伪 Lost，TTL 后清空，权威删除立即 Lost；
5. 私有或零公开 Service Node 同步但不发布空 Node；
6. 慢客户端、Actor 队列和 M5 队列过载均有界并能恢复；
7. 8192 Node、16384 连接、65536 Service、256 KiB Node 和 16 MiB Body 边界通过；
8. 启动失败、正常 Stop、Stop 超时和重复 Close 不泄漏 goroutine、连接或 Buffer；
9. Windows/Linux 单测、集成、Race、Fuzz、构建和 Benchmark 通过；
10. M18 可以只新增 etcd Provider，不修改 Node、Directory、RPC、业务 API 或本公共 SPI。

性能验收保存吞吐、`allocs/op`、`B/op` 和 p50/p95/p99 真实基线；设计不在测量前设定缺乏
依据的硬延迟 SLA。

## 12. 开工 Review 结论

2026-07-30 已确认：

1. M17 与 M18 共用一套小型 Provider 契约，但实现继续拆为两个里程碑；
2. 第三方替换只需一个 Factory、一个 Provider、一个注册入口和一个同名配置块；
3. Origin 私有 TCP/Actor/Revision/Frame 不进入公共 SPI；
4. Provider、Node、Directory、RPC、状态、错误和停止边界已经逐项冻结；
5. FrameType、二进制布局、幂等、背压、TTL、Buffer 所有权和容量已经逐项冻结；
6. Consul 映射没有要求修改框架上层 API；
7. 公共及 Origin 专属验收矩阵已经完整；
8. M17 当前没有遗留待确认项。

开工 Review 已通过；实现与质量门禁已按
[M17 实施计划](../../plans/M17-公共服务发现Provider与Origin内置发现实施计划.md)完成。
公共 SPI 已由 Origin 与最小 Consul 风格 Provider 共同验证，M18 可以只新增 etcd Provider
与同名配置，不修改 Node、Directory、RPC 或业务发现 API。

M18 的实际实现与全量回归已于 2026-07-30 进一步证明该边界成立；无需回改 M17 公共
Factory、Provider、Context、Host、DTO、状态或错误码。
