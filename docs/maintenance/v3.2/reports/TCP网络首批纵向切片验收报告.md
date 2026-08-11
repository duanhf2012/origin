# TCP 网络首批纵向切片验收报告

> 日期：2026-08-10
> Windows：Go 1.26.5，windows/amd64
> Ubuntu：Go 1.26.5，linux/amd64，Ubuntu 7.0.0-28-generic
> 结论：本切片验收通过

## 已验证能力

- Raw/PB/JSON、自定义 Codec、PB Big/Little Endian 和 TCP Big/Little Endian；
- 同一 Service 同时托管 Server/Client 并通过 TCP 回环调用自己，无启动死锁；
- Server 查询/主动关闭、Client 初次失败后重连、Dialer 单次连接；
- OnOpen/Message/Close 顺序、OnClose 恰好一次、停止期最终回调；
- Buffer 所有权、每 Session/Module 字节预算、队列消息数、水位迟滞和关闭释放；
- RPC、Discovery 与原有 tcpnet 使用者全量回归。

## 执行结果

Windows 已通过：

```text
go test ./...
go test -race ./internal/bufferpool ./internal/bytebudget ./internal/tcpnet ./sysmodule/network/...
go vet ./internal/bufferpool ./internal/bytebudget ./internal/tcpnet ./sysmodule/network/...
TCP 回环与重连测试连续执行 20 次
BenchmarkFrameLength: 0 B/op, 0 allocs/op
BenchmarkSendQueue: 约 50 ns/op, 0 B/op, 0 allocs/op
```

Ubuntu 已通过：

```text
go test ./...
go test -race ./...
go vet ./...
go build ./...
JSON Fuzz 5s：1,001,664 次执行
PB Fuzz 6s：376,095 次执行
TCP Frame Fuzz 5s：760,319 次执行
BenchmarkFrameLength：约 0.25 ns/op，0 B/op，0 allocs/op
BenchmarkSendQueue：约 44～45 ns/op，0 B/op，0 allocs/op
实际启动 TCP Raw 自调用示例并发送 SIGINT：Server/Client 均收到预期消息，进程正常退出
```

## 覆盖率说明

公共 `sysmodule/network` 单元测试达到 100%；Buffer Pool 与 bytebudget 超过 90%，既有 tcpnet 加入
新队列后仍超过 80%。协议和 TCP Module 的成功路径、边界与真实生命周期由跨包 Node 集成测试
覆盖；包级数字会把仅能由真实传输触发的内部 core 分支分摊到测试二进制，因此不把单一百分比
当作验收替代。尚未人为伪造操作系统极低概率错误分支来追求表面 100%。

WebSocket/KCP 将复用本切片契约，但必须分别完成握手、消息边界、停止、Race、Fuzz 和 Ubuntu
验收后才能进入下一阶段。

## 2026-08-11 Config 增量验收

Windows 通过全仓 `go test ./...`、全仓 `go vet ./...`、`service` 与全部网络包 Race；TCP Config
转换函数覆盖率为 85.7%～100%，未覆盖分支主要是 64 位环境无法制造的 `int` 溢出保护。配置驱动
TCP 自调用真实启动成功，Client 收到 `hello from the same service`。

同一代码快照上传到独立 Ubuntu 目录 `/home/boyce/origin_v3_tcp_ws_config_20260811`，相关包普通测试
与 Race 全部通过；配置驱动 TCP Example 使用真实回环 socket 收发成功。测试没有修改远端既有仓库。

覆盖率运行同时暴露 `TestCompletedContextDoesNotCreateExecutionFrame` 的测试时序不完整：WaitGroup 在
回调返回前完成，不能证明 Scheduler 已清除最后一个执行帧。测试现在额外等待 `CompletedTotal`，连续
100 轮通过；生产调度代码和语义没有因此修改。
