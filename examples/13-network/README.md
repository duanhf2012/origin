# 13 - 网络模块

根据接入端选择传输；两者使用相同的 `network.Session`、`Handler`、容量和错误语义。

| 示例 | 适用场景 |
| --- | --- |
| [`01-tcp-raw-self-call`](./01-tcp-raw-self-call/README.md) | 游戏客户端、自定义长度帧、直接 TCP 长连接 |
| [`02-websocket-raw-self-call`](./02-websocket-raw-self-call/README.md) | 浏览器、HTTP Upgrade、WS/WSS 长连接 |

两个示例都在一个 Service 内同时托管 Server 和 Client，并调用自己的网络入口，用最小程序验证
生命周期、串行回调和回环通信。PB、JSON 与自定义 Codec 可在两种传输上复用。
