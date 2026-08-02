# 跨节点 RPC 示例

先从不依赖外部中间件的 TCP 示例开始，再尝试 NATS，最后学习多实例路由与广播。TCP 与 NATS 的业务客户端外观相同，差异集中在 Node 配置和运行依赖。

- [01-tcp-two-nodes](./01-tcp-two-nodes/README.md)：Origin Discovery + TCP。
- [02-nats-two-nodes](./02-nats-two-nodes/README.md)：Origin Discovery + NATS，需要先启动依赖。
- [03-route-and-broadcast](./03-route-and-broadcast/README.md)：业务 Key 路由与广播错误处理。

对应教程：[跨节点 RPC](../../docs/baseline/v3.0/guides/06-remote-rpc.md)。
