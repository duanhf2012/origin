# 13 - 网络模块

根据接入端和网络条件选择传输；三者使用相同的 `network.Session`、`Handler`、容量和错误语义。

| 示例 | 适用场景 |
| --- | --- |
| [`01-tcp-raw-self-call`](./01-tcp-raw-self-call/README.md) | 游戏客户端、自定义长度帧、直接 TCP 长连接 |
| [`02-websocket-raw-self-call`](./02-websocket-raw-self-call/README.md) | 浏览器、HTTP Upgrade、WS/WSS 长连接 |
| [`03-kcp-raw-self-call`](./03-kcp-raw-self-call/README.md) | UDP 弱网、低时延游戏长连接和 KCP 专属参数 |

三个示例都采用“薄 Service + 业务网络 Module + Server/Client 子 Module”的结构，并调用自己的
网络入口，用最小程序验证父子生命周期、串行回调和回环通信。PB、JSON 与自定义 Codec 可在三种
传输上复用。
