# RPC 单 Payload 固定上限 32 MiB 验收报告

> 状态：已完成 Windows 验收
>
> 基线：v3.2.0 Origin RPC
>
> 目标：v3.2.1
>
> 兼容性：线协议和生成代码 ABI 不变；NATS Broker 必须覆盖业务上限及包络

## 变更结果

- Origin RPC 默认及固定业务 payload 上限已统一为 32 MiB。
- 显式配置超过 32 MiB 时在启动资源创建前返回配置错误。
- TCP/NATS 继续在业务 payload 外预留各自包络，生成代码 ABI 无变化。
- Compose NATS `max_payload` 已调整为 33 MiB，集成测试 Broker 同步覆盖新边界。

## 验证记录

环境：Windows amd64，Go 1.27rc2，AMD Ryzen 7 7840HS。

```text
go test ./...
go test -race ./rpc ./application ./deploy/compose ./tests/integration/rpcfixture -count=1
go vet ./...
go build ./...
GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build ./...
go run ./cmd/origingen rpc --check ./...
go mod tidy -diff
git diff --check
```

上述门禁全部通过。Race 首轮并行回归中，一个与本次改动无关的 Application HTTP
生命周期测试触发一次三秒等待超时；该用例单独连续三轮、Application 全包及最终组合
Race 复跑均通过，没有放宽断言或跳过测试。

RPC 单包覆盖率为 59.8%；本次修改的 `rpc.Config.Validate` 为 100%。32 MiB 临界
Codec 往返和超一字节拒绝均由既有边界测试覆盖。

## 性能记录

命令：

```text
go test ./rpc -run '^$' \
  -bench '^(BenchmarkBytePayloadCodec|BenchmarkCustomPayloadCodec)$' \
  -benchmem -benchtime=100ms -count=1
```

| 样本 | ns/op | MB/s | B/op | allocs/op |
|---|---:|---:|---:|---:|
| Byte 16B | 57.38 | 278.87 | 16 | 1 |
| Byte 1KiB | 544.1 | 1881.84 | 1024 | 1 |
| Byte 32MiB-4B | 6,937,206 | 4836.88 | 67,108,935 | 3 |
| Custom 16B | 65.28 | 245.09 | 16 | 1 |
| Custom 1KiB | 455.7 | 2247.33 | 1024 | 1 |
| Custom 32MiB-4B | 6,690,844 | 5014.98 | 67,108,928 | 3 |

本次只改变常量边界，没有给小消息热路径增加判断、分配、复制或锁。最大样本的约 64 MiB
分配来自 Benchmark 同时创建完整编码和解码结果；框架不会因默认值提高而为普通连接预分配
32 MiB。高并发大消息仍需项目通过较小配置、分片或外部对象存储控制 GC 和尾延迟。
