# 使用 NATS 承载跨节点 RPC

NATS 是可选的第三方 RPC Transport。业务 RPC 契约、生成客户端、路由和调用方式与 TCP 相同；只有
Application 顶层传输配置和运行依赖不同。

## 什么时候选择

- 已有受运维的 NATS 集群；
- 希望由 Broker 管理 Node 连接、断线恢复和集群入口；
- 可以接受 Core NATS 的至多一次投递语义。

只需要简单内网直连时，优先从[内置 TCP 跨节点 RPC](../baseline/v3.0/guides/07.remote-rpc.md)开始。

## 本地运行

进入 [`examples/07-remote-rpc/02-nats-two-nodes`](../../examples/07-remote-rpc/02-nats-two-nodes/README.md)，
按当前系统执行：

```text
deps-up.bat       # Linux/macOS 使用 ./deps-up.sh
check-deps.bat    # Linux/macOS 使用 ./check-deps.sh
run.bat           # Linux/macOS 使用 ./run.sh
deps-down.bat     # Linux/macOS 使用 ./deps-down.sh
```

示例会启动发现 Node、业务 Node 和调用 Node；它们复用同一套 RPC 契约，但每个 Node 各自持有一条
NATS Connection。

## 最小配置

```yaml
rpc:
  transport: nats
  nats:
    namespace: origin-tutorial
    urls:
      - nats://127.0.0.1:4222

discovery:
  type: origin
  origin:
    ttl: 5s
    server:
      node: discovery-1

nodes:
  - id: discovery-1
    services: [DiscoveryService]
  - id: player-1
    services: [PlayerService]
  - id: gateway-1
    services: [GatewayService]
```

`namespace` 隔离同一集群内的 Origin 环境，同一 RPC 环境中的 Node 必须使用相同值。NATS 模式不再
配置 Node 级 `rpc.tcp.listen` 或 `advertise`；业务代码也不因 Transport 改变。

## 生产连接

生产环境至少配置受信任 TLS 和一种认证方式：

```yaml
rpc:
  transport: nats
  nats:
    namespace: game-prod
    urls:
      - tls://nats-1.example.com:4222
      - tls://nats-2.example.com:4222
    auth:
      credentials_file: certs/game.creds
    tls:
      enabled: true
      ca_file: certs/nats-ca.pem
      server_name: nats.example.com
      insecure_skip_verify: false
```

认证可以选择用户名/密码、Token、Credentials 文件或 NKey Seed 文件，但不能混用。证书和凭据不应
提交到业务默认配置。`insecure_skip_verify` 应保持 `false`。

## 使用边界

- Origin 使用 Core NATS，不提供 JetStream 持久化或消费确认；
- RPC 超时或断线不会自动重放业务请求；
- 提交成功不等于远端业务已经处理；
- 需要同时监控 NATS 连接、慢消费者、服务发现状态和 Origin RPC 错误；
- 本地依赖脚本与 Compose 不是生产部署模板。

路由、广播、`IncludeRetired` 和调用错误处理继续阅读[跨节点 RPC](../baseline/v3.0/guides/07.remote-rpc.md)。
