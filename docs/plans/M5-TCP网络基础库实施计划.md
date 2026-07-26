# Origin 第三版 M5 TCP 网络基础库实施计划

> 计划状态：已完成
> 创建日期：2026-07-26
> 对应里程碑：[M5 TCP 网络基础库设计](../design/milestones/M5-TCP网络基础库设计.md)

## 1. 实施目标

实现内部 `tcpnet` 包，为后续 M10 TCP RPC 和 TcpModule Adapter 提供共同的长度帧、
连接、低拷贝 Buffer 收发、有界发送、读写超时以及可完整等待的资源生命周期。

M5 只提供单次 Dial 和连接关闭通知，不实现 NodeID、握手、RPC 协议、服务发现、自动重连、
业务 TcpModule、消息压缩或 Transport Drain。M10 连接管理器后续组合 M5 单次 Dial 实现
逻辑目标有效期间的重连。

## 2. 实施步骤

1. 在 `errs` 中登记五个 Transport 错误码及公共哨兵；
2. 实现 `ConnectionOptions`、`ListenOptions`、长度字段和全部配置校验；
3. 实现一、二、四字节长度字段以及大端、小端编码和解析；
4. 实现按帧数、payload 字节数双重限制的预分配环形发送队列；
5. 实现 `Conn` 的 ReadLoop、WriteLoop、BufferPool 所有权和 scatter/gather 完整写入；
6. 实现空 payload、短读、短写、读空闲超时、写超时和协议错误关闭；
7. 实现 Handler 顺序回调、panic 隔离、关闭原因提交、幂等 Close 和 Wait；
8. 实现单次 Context Dial、TCP_NODELAY、KeepAlive 和 Listener Accept 生命周期；
9. 补齐单元测试、真实回环集成测试、Fuzz、Benchmark、竞态和覆盖率验证；
10. 执行 Windows、Linux 原生测试以及 Windows、Linux、macOS 构建；
11. 回写设计、计划、索引和复核清单中的实际实现及验收结果；
12. 完成 M5 独立提交。

## 3. 实现边界

- 生产代码固定放在 `internal/tcpnet`，不建立面向业务的公共 TCP 包；
- 只依赖标准库、M0 `errs`、M1 `log` 和 M2 `internal/bufferpool`；
- 不引入 event-loop、网络轮询、队列或重连第三方依赖；
- 每条连接固定一个 ReadLoop 和一个 WriteLoop，不为每帧创建 goroutine；
- payload 允许为空，nil Buffer 非法，默认单帧最大 `4M`；
- M5 默认发送队列为 `4096` 帧和 `8M`，上层可以显式覆盖；
- 队列满非阻塞返回 `CodeTransportOverloaded`，M5 不理解 RPC 消息分类；
- ReadTimeout 默认关闭，WriteTimeout 默认 `15s`；
- `Close` 立即停止传输并释放未发送 Buffer，不实现 Drain 或半关闭；
- 已关闭 `Conn` 不复活，不保存可供自动重发的 payload；
- 不提前实现 M6、M7～M12 或后续 TcpModule 能力。

## 4. 预计代码结构

```text
errs/
├── code.go
└── errors.go

internal/tcpnet/
├── options.go
├── error.go
├── frame.go
├── queue.go
├── conn.go
├── listener.go
├── dial.go
├── options_test.go
├── frame_test.go
├── queue_test.go
├── conn_test.go
├── listener_test.go
├── listener_internal_test.go
├── frame_fuzz_test.go
└── benchmark_test.go

tests/integration/tcpnet/
└── tcpnet_test.go
```

实际文件可以按职责合并，但不为目录外观建立空文件或无实际用途的抽象。

## 5. 关键实现不变量

1. Send 成功才把 Buffer 所有权转移给 Connection，失败时仍由调用方释放；
2. ReadLoop、WriteLoop 和关闭清理中的每个 Buffer 恰好释放一次；
3. 连接关闭状态、队列准入和 Buffer 转移在同一同步边界完成，不出现关闭后成功入队；
4. 第一个有效关闭原因保留，后续并发错误不能覆盖；
5. OnOpen、OnMessage、OnClose 对同一连接严格串行，OnClose 恰好一次；
6. Handler 回调不在持有连接、队列或 Listener 锁时执行；
7. 写超时或部分写失败后不复用当前 TCP 字节流；
8. Listener 停止 Accept 后拒绝正在竞态注册的新连接，并等待全部所属连接清理；
9. 空 payload 仍消费一个帧槽位，不能绕过队列容量；
10. 正常逐帧收发不写日志，过载日志按连接限频。

## 6. 验证命令

```text
gofmt
go vet ./...
go test ./...
go test -count=20 ./internal/tcpnet/...
go test -count=10 ./tests/integration/tcpnet/...
go test -race ./...
go test -coverprofile cover.out ./internal/tcpnet/...
go tool cover -func cover.out
go test -run '^$' -bench '.' -benchmem ./internal/tcpnet/...
go build ./...
scripts\buildwin.bat
scripts\buildlinux.bat
GOOS=darwin GOARCH=amd64 go build ./...
GOOS=darwin GOARCH=arm64 go build ./...
```

Linux 测试机安装了兼容 Go 工具链时原生执行普通测试、竞态检测和 Benchmark；如果没有
Go 工具链，则在 Windows 交叉编译 Linux 测试二进制并上传执行真实 socket、Deadline、
并发关闭和资源回收测试。连接凭据、主机地址和临时目录不得写入仓库。

## 7. 完成条件

1. M5 设计中的全部 API、默认值和非法配置都有测试；
2. 长度帧覆盖空值、边界、越界、大小端、短读和粘包/拆包；
3. 发送覆盖短写、并发 Send、双重队列上限、关闭竞态和全部 Buffer 所有权；
4. Listener、Dial、KeepAlive、读写 Deadline 和真实回环双向通信通过集成测试；
5. Handler、ReadLoop、WriteLoop panic 和错误路径不泄漏 Buffer 或 goroutine；
6. Windows 全量测试与竞态检测通过，Linux 原生网络测试通过；
7. 三平台构建、静态检查、覆盖率审计和 Benchmark 完成；
8. 设计、计划、索引、复核清单和迁移矩阵已经回写实际验收结果；
9. 工作区只包含 M5 及必要的前置错误码修改并形成独立提交。

## 8. 开工记录

M5 设计 Review 已于 2026-07-26 通过。开发者随后明确指示“实现”，本计划据此进入执行；
实现不得改变已确认设计，发现新取舍时仍需暂停并重新确认。

## 9. 实际验收结果

M5 于 2026-07-26 完成。实际代码位于 `internal/tcpnet`，跨包真实网络测试位于
`tests/integration/tcpnet`，没有新增手工测试 `main` 或第三方依赖。

完成的验证如下：

```text
go vet ./...
go test -count=1 ./...
go test -race -count=1 ./...
go test -count=50 ./internal/tcpnet ./tests/integration/tcpnet
go test -coverprofile .tmp_tcpnet_coverage.out ./internal/tcpnet
go test -run '^$' -fuzz FuzzFrameLengthRoundTrip -fuzztime 5s ./internal/tcpnet
go test -run '^$' -bench . -benchmem -benchtime=300ms ./internal/tcpnet
scripts\buildwin.bat
scripts\buildlinux.bat
GOOS=darwin GOARCH=amd64 go build ./...
GOOS=darwin GOARCH=arm64 go build ./...
```

验收数据：

- `internal/tcpnet` 单元覆盖率 `93.3%`；
- Windows 全仓普通测试和竞态检测通过；
- TCP 单元与集成测试连续重复 50 轮通过；
- Fuzz 五秒执行约 `699266` 次，无失败；
- Windows/amd64 `BenchmarkWriteItem` 为 `45.42 ns/op`、`0 B/op`、`0 allocs/op`；
- Windows/amd64 环形队列为 `36.56 ns/op`、`0 B/op`、`0 allocs/op`；
- Linux/amd64 交叉编译测试二进制已在 Linux 真实机器执行，单元、真实回环集成和
  Benchmark 均通过；
- Windows/amd64、Linux/amd64、macOS/amd64、macOS/arm64 构建通过。

实施期间按性能规则复核逃逸：读帧头、写帧头和 `net.Buffers` 描述符改为连接级复用，
避免逐帧临时描述符逃逸；payload 仍不拼接、不额外复制。
