# RPC 单 Payload 固定上限 32 MiB 设计

> 状态：已实现并完成 Windows 验收
>
> 基线：v3.2.0 Origin RPC
>
> 目标：v3.2.1
>
> 兼容性：线协议和生成代码 ABI 不变；NATS Broker 需要覆盖 32 MiB 加 RPC 包络

## 目标

Origin RPC 单个业务 payload 的默认值和编解码固定硬上限由 4 MiB 统一调整为 32 MiB。
项目仍可通过 `rpc.max_payload_size` 配置更小的值，但不能配置为零、负数或超过 32 MiB。

## 统一边界

- `rpc.DefaultMaxPayloadSize` 固定为 `32 * 1024 * 1024` 字节。
- `rpc.Config.Validate` 在创建连接、监听器和 goroutine 前拒绝超过固定上限的配置。
- 生成 Codec 继续复用 `DefaultMaxPayloadSize`，因此无需修改生成代码、生成器版本或
  `GeneratedABIVersion`。
- TCP 和 NATS Adapter 分别在业务 payload 外预留协议包络，并继续执行已有的入站、
  出站和连接状态校验。
- `MaxSystemMessageSize`、`DefaultMaxBroadcastSize` 及通用 `tcpnet`/`natsnet` 的独立
  默认值不随本次 RPC 业务边界变化。

## NATS 部署约束

NATS `max_payload` 限制的是完整消息。Origin RPC 最坏 NATS 包络为 549 B，因此 Broker
必须大于 `rpc.max_payload_size`。仓库 Compose 配置使用 33 MiB，为默认 32 MiB 业务
payload 留出明确余量；运行时仍在启动阶段读取 Broker INFO 并拒绝能力不足的部署。

## 资源影响

32 MiB 是单消息上限，不是预分配大小，普通小消息不会因此直接申请 32 MiB。接近上限
的请求会放大单次 Buffer 占用、编码时间、网络传输时间和队头阻塞，因此高并发大消息
仍应由项目按场景调小配置，或改用分片、对象存储等传输方式。TCP 的默认 64 MiB 单连接
发送字节预算保持不变，可容纳至少一个最大 RPC 帧，并继续形成有界背压。

## 兼容性

线协议和生成客户端 ABI 不变。唯一需要部署方处理的兼容事项是：使用 NATS 且希望沿用
32 MiB 默认值时，Broker `max_payload` 必须同步提高；否则 Node 会按既有规则启动失败，
不会静默降级或在调用热路径才失败。
