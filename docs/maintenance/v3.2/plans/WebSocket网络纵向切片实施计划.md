# WebSocket 网络纵向切片实施计划

> 目标版本：Origin v3.2
> 实施顺序：公共基础与 TCP 验收之后、KCP 之前
> 设计依据：[`Origin 网络模块核心设计`](../design/Origin网络模块核心设计.md)
> 外观基线：当前已经实现并人工确认的 `sysmodule/network` 与 `sysmodule/network/tcp`

## 1. 实施原则

1. 当前代码中的公共外观是实现基线。设计文档与代码不一致时，公共 `Session`、`Handler`、
   `Server`、`Client`、`Dialer` 形状以当前代码为准；发现必须改变公共外观的问题时先修订设计，
   不在 WebSocket 包中建立临时兼容层。
2. 复用已经通过 TCP、Race 和 Ubuntu 验证的 Runtime、Buffer Pool、端点容量、生命周期、错误和
   统计语义。WebSocket 适配器不得另建业务消息队列或绕过 Service 串行回调。
3. 只增加 WebSocket 的真实专属能力：HTTP Upgrade、Path、Origin、Text/Binary、请求 Header、
   子协议、TLS、Ping/Pong 和标准 Close。WebSocket 使用原生消息边界，不增加长度帧或端序。
4. 设计优化只处理当前重复、已知安全风险或实现本切片所必需的边界；不增加公共握手元数据、动态
   Middleware、压缩、代理抽象或未来协议预留层。
5. 性能优化控制范围。发送队列、容量预算和单 Writer 属于正确性设计；其他优化必须由本切片
   Benchmark/Profile 证明收益后再做。

## 2. 最小公共外观

包路径为 `sysmodule/network/websocket`，提供与 TCP 一致的：

- `NewServer(address, ServerOptions)`；
- `NewClient(url, ClientOptions)`；
- `NewDialer(url, DialOptions)`；
- Server 的 `Addr`、`Session`、`SessionCount`、`CloseSession`、`Stats`；
- Client 的 `Session`、`State`、`Stats`；
- Dialer 的一次拨号语义。

`ServerOptions` 只增加 Path、数据消息类型、握手超时、Ping/Pong、Origin 检查、子协议、响应
Header 和服务端 TLS。`DialOptions` 对应 URL、数据消息类型、握手超时、Ping/Pong、请求 Header、
子协议和客户端 TLS。默认发送及接收 Binary Message，默认 Path 为 `/ws`，未配置 `CheckOrigin`
时沿用 Gorilla WebSocket 的安全同源策略，默认不开启实验性压缩。

## 3. 实现顺序

1. [x] 固化 Options 默认值、复制规则和启动前校验；
2. [x] 抽取 TCP/WebSocket/KCP 都需要的内部有界发送 Ring，保持 TCP 行为和测试不变；
3. [x] 实现 WebSocket Conn 的单 Reader、单 Writer、Ping/Pong、Close 和消息大小限制；
4. [x] 实现 HTTP Upgrade Listener、Origin、Path、TLS、连接准入和优雅停止；
5. [x] 接入公共 Runtime，完成 Server、Client、Dialer 与有界重连；
6. [x] 补齐 Options、Conn、Listener、共同契约、服务自调用、Client 重连和 Dialer 集成测试；
7. [x] 执行 Race、Fuzz、Benchmark、泄漏检查和本地全仓门禁；
8. [x] 完成中文使用指南、完整注释 Example、变更与验收记录；
9. [x] 在 Ubuntu `192.168.8.3` 执行真实 WS/WSS、服务自调用、Race、Fuzz 和全仓门禁；
10. [x] 复核工作树后提交 `v3` 分支。

## 4. 必测边界

- 默认同源允许、跨 Origin 拒绝和自定义 Origin 策略；
- Path 错误、非 Upgrade 请求、握手取消/超时、首次拨号失败和停止中拨号；
- Binary/Text 正常双向传输和消息类型不匹配；
- `MaxMessageSize` 边界、分片消息、恶意超大消息及 Buffer 归还；
- Ping/Pong 存活、Pong 超时、读空闲、写超时、正常/异常 Close Code；
- 消息数、Session 字节和端点总字节过载，80%/50% 水位与慢连接关闭；
- TLS 成功、证书校验失败、子协议与 Header；
- Open/Message/Writable/Close 顺序、恰好一次、Handler error/panic 和重复 Close；
- Server、Client、Dialer、重连以及服务自己调用自己的 WebSocket 回环；
- 停止和启动失败后的端口、Session、goroutine、队列、预算及 Buffer Pool 配平。

重点核心路径以可达语句和分支接近 100% 为目标；平台上不能稳定制造的底层错误使用故障注入、
Race、Fuzz 和 Ubuntu 真实集成补证，不用无意义断言换覆盖率。

## 5. 验收门禁

- `go test ./... -count=1`、`go test -race ./... -count=1`、`go vet ./...` 全部通过；
- WebSocket Options/Conn/Listener/公共适配层测试通过，重点包覆盖率报告已记录；
- WebSocket 边界 Fuzz 和必要 Benchmark 完成，无未解释回退；
- WS 与 WSS 的 Server、Client、Dialer 和同 Service 自调用在 Ubuntu 通过；
- 指南从使用者视角说明最短接入、浏览器 Text/Origin、TLS、PB/JSON 和错误处理；
- Example 可独立构建运行，关键代码具有简洁完整中文注释；
- 文档、实现和测试一致，工作树只包含本切片改动。
