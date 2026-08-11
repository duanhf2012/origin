# WebSocket 网络纵向切片验收报告

> 日期：2026-08-11
> Windows：Go 1.26.5，windows/amd64
> Ubuntu：Go 1.26.5，linux/amd64，Ubuntu 7.0.0-28-generic
> 结论：本切片验收通过

## 已验证能力

- 统一 `Session`/`Handler`、Server、Client、Dialer、Client 有界重连与状态统计；
- Binary/Text 双向消息、严格 UTF-8、原生消息边界和消息大小限制；
- Path、默认同源、跨 Origin 拒绝、自定义 Origin、请求/响应 Header 保留字段校验；
- WS/WSS、TLS 证书校验路径、Header、子协议、Ping/Pong、业务读空闲与标准 Close；
- 公共 WSS Server、Client、Dialer 的真实 TLS 握手、双向收发与生命周期；
- 连接数、消息数、Session/端点字节预算、80%/50% 水位、慢连接和 Buffer 配平；
- Handler panic/error、重复关闭、首次拨号失败、重连、服务自己调用自己的回环；
- TCP 改用共享发送 Ring 后的全量功能、Race、RPC Broadcast 和原有模块回归。

## Windows 结果

以下门禁全部通过：

```text
go test ./... -count=1
go test -race ./... -count=1
go vet ./...
go build ./...
WebSocket/TCP 重点 Race 连续 20 轮
WebSocket 核心测试连续 50 轮
WebSocket Fuzz 5s：207,057 次执行
```

重点包覆盖率：

```text
internal/messagequeue：87.2%
internal/wsnet：85.7%
sysmodule/network/websocket：82.1%
```

未覆盖部分主要是操作系统极低概率关闭失败、内部不变量 panic 和故障日志分支；消息读取增长、
容量、水位、所有权、生命周期和公开外观重点路径由单元、集成、Race 与 Fuzz 共同覆盖。

Windows 回环 Benchmark（100 次/规格）：

```text
32 B：   177,053 ns/op，303 B/op， 4 allocs/op
256 B：  192,898 ns/op，292 B/op， 6 allocs/op
4 KiB：  249,559 ns/op，1,066 B/op，15 allocs/op
64 KiB：1,259,518 ns/op，9,762 B/op，26 allocs/op
```

该数据用于建立首轮基线；没有仅凭单机短 Benchmark 扩大性能优化范围。

## Ubuntu 结果

代码解压到独立目录 `/home/boyce/origin_v3_ws_validation_20260811_0928`，未修改远端原仓库。
以下门禁全部通过：

```text
go test ./... -count=1
go test -race ./... -count=1
go vet ./...
go build ./...
WebSocket/TCP Race 连续 50 轮
公共 WSS 与 TCP/WebSocket 最终增量 Race 连续 20 轮
TCP Broadcast Race 连续 100 轮
WebSocket 核心测试连续 50 轮
WebSocket Fuzz 5s：35,621 次执行
```

Ubuntu 回环 Benchmark（100 次/规格）：

```text
32 B：    29,968 ns/op，263 B/op， 4 allocs/op
256 B：   35,778 ns/op，246 B/op， 6 allocs/op
4 KiB：   39,383 ns/op，989 B/op，15 allocs/op
64 KiB： 214,592 ns/op，8,393 B/op，25 allocs/op
```

真实启动 `02-websocket-raw-self-call` 后，Server 与 Client 均收到
`hello through websocket`；SIGINT 后 Application 与 Node 正常停止。WS、WSS、TLS、Origin、心跳和
子协议由 Ubuntu 单元/集成测试执行；公共 WSS Server、Client、Dialer 也完成真实 TLS 端到端验证，
服务自调用由公共 Module 与真实 socket 完成。

## Race 复验说明

Ubuntu 首轮 Race 发现测试在读取发送统计和服务端注销状态时使用了瞬时假设。实现契约中 Writer
统计在完整写出后递增，客户端与服务端也各自完成最终关闭，因此“远端已处理”不保证另一个
goroutine 的统计/注销已在同一时刻发布。测试改为有界等待公开终态后：

- Windows TCP/WebSocket Race 连续 20 轮通过；
- Ubuntu TCP/WebSocket Race 连续 50 轮通过；
- Windows TCP Broadcast Race 连续 50 轮通过；
- Ubuntu TCP Broadcast Race 连续 100 轮通过；
- Ubuntu 全仓 Race 再次通过。

没有忽略 Race 失败，也没有通过延长无界 Sleep 掩盖问题。

## 2026-08-11 Config 增量验收

Windows 通过全仓 `go test ./...`、全仓 `go vet ./...`、`service` 与全部网络包 Race；WebSocket
Config 转换函数覆盖率为 85.7%～100%，配置字符串、容量、心跳、URL、重连和 Slice 所有权均有
专门断言。配置驱动 WebSocket 自调用真实启动成功，Client 收到 `hello through websocket`。

同一代码快照上传到独立 Ubuntu 目录 `/home/boyce/origin_v3_tcp_ws_config_20260811`，相关包普通测试
与 Race 全部通过；配置驱动 WebSocket Example 使用真实回环 socket 收发成功。测试没有修改远端
既有仓库，也没有把连接 Header、证书、Origin 策略或其他运行期安全对象写入 YAML。
