# Origin 第三版 M6 NATS 基础库实施计划

> 计划状态：已完成
> 创建日期：2026-07-26
> 对应里程碑：[M6 NATS 基础库设计](../design/milestones/M6-NATS基础库设计.md)

## 1. 实施目标

实现内部 `natsnet` 包，为后续 M11 NATS RPC 和服务发现控制面提供 Core NATS 连接、
发布订阅、有限重连、认证、TLS、Pending 限制、状态事件以及可完整等待的资源生命周期。

M6 只处理 Subject 和原始字节消息，不实现 Origin RPC Subject、RequestID、ServiceName、
代码生成、序列化、服务路由、业务调度或 JetStream。

## 2. 实施步骤

1. 固定 `nats.go v1.52.0` 和集成测试使用的 `nats-server/v2 v2.14.3`；
2. 实现 Options 默认值、认证互斥、TLS 文件检查、URL 校验与脱敏；
3. 实现 NATS error 到 Origin Transport 错误码的统一映射；
4. 实现 Context-aware 初始连接、有限自动重连、连接状态和事件回调；
5. 实现允许空 payload 的 Publish、消息大小限制和 Context Flush；
6. 实现普通订阅、Queue Group、Pending 双重上限、慢消费者报告和统计；
7. 实现 Handler/EventHandler panic 隔离和敏感信息保护；
8. 实现 Connection/Subscription 的幂等 Close、Drain 和 Wait；
9. 补齐单元测试、真实 NATS Server 集成测试、Benchmark、竞态和覆盖率验证；
10. 使用本机 Docker 三节点集群和 Ubuntu 上的三节点集群执行真实连接与故障恢复测试；
11. 执行 Windows、Linux 原生测试以及 Windows、Linux、macOS 构建；
12. 回写设计、计划、索引、复核清单和迁移矩阵中的实施与验收结果；
13. 完成 M6 独立提交。

## 3. 实现边界

- 生产代码固定放在 `internal/natsnet`；
- 生产代码只依赖标准库、`nats.go`、`errs` 和 `log`；
- 不接入 `bufferpool`，不在 `nats.go` 已有分配之外再次复制入站 payload；
- 不包装 NATS 原生 Request/Reply；
- 不定义 Origin RPC Subject，不引入 NodeID 或 ServiceName；
- 不在官方客户端外增加发送队列或每消息 goroutine；
- 不建立 TCP/NATS 共同的大型 Transport 接口；
- 初始连接失败直接返回，成功后的自动重连次数有界；
- 正常消息热路径不写日志，不执行每次 Publish Flush。

## 4. 预计代码结构

```text
internal/natsnet/
├── options.go
├── error.go
├── event.go
├── message.go
├── conn.go
├── subscription.go
├── options_test.go
├── error_test.go
├── conn_test.go
├── subscription_test.go
└── benchmark_test.go

tests/integration/natsnet/
└── natsnet_test.go
```

实际文件可以按职责合并，但不为目录外观建立空文件或没有真实用途的抽象。

## 5. 关键实现不变量

1. Publish 返回后不再引用调用方 payload，包装层不额外复制；
2. MessageHandler 只在 NATS 回调期间拥有只读 `Message.Data` 视图；
3. Connection 是其全部 Subscription 的最终所有者；
4. Close、Drain 和 Wait 可以重复或并发调用，不 panic、不泄漏 goroutine；
5. Drain 一旦开始就拒绝新的 Publish 和 Subscribe，超时后强制 Close；
6. 第一个有效终止原因保留，后续回调不能覆盖；
7. EventClosed 对每个 Connection 至多发布一次；
8. Handler 和 EventHandler 调用期间不持有包装层状态锁；
9. Handler/EventHandler panic 只能终止当前回调，不能破坏连接或订阅调度；
10. 日志、Event URL 和错误不得包含密码、Token、NKey Seed 或 URL UserInfo；
11. Pending 数量或字节达到上限时必须映射为过载，不建立无界缓冲；
12. 所有初始连接取消观察协程在 Connect 返回前退出。

## 6. 验证命令

```text
gofmt
go vet ./...
go test ./...
go test -count=20 ./internal/natsnet/...
go test -count=10 ./tests/integration/natsnet/...
go test -race ./...
go test -coverprofile cover.out ./internal/natsnet/...
go tool cover -func cover.out
go test -run '^$' -bench . -benchmem ./internal/natsnet/...
scripts\buildwin.bat
scripts\buildlinux.bat
GOOS=darwin GOARCH=amd64 go build ./...
GOOS=darwin GOARCH=arm64 go build ./...
```

真实集群测试读取环境变量提供的 URL 和账号密码，凭据不得写入仓库或测试日志。

## 7. 完成条件

1. M6 设计中的 API、默认值、认证/TLS 组合和非法配置均有测试；
2. 普通订阅、Queue Group、空 payload、大小边界和 Flush 屏障通过；
3. 断线重连、自动重订阅、有限重连和 Reconnect Buffer 边界通过；
4. Pending 数量/字节、慢消费者、Dropped 统计和回调 panic 路径通过；
5. Connection/Subscription 的 Close、Drain、Wait 和超时强制关闭通过；
6. Windows 和 Linux 真实 NATS 测试、竞态检测及三平台构建通过；
7. 已安装的三节点 NATS 集群通过跨节点发布订阅和节点故障恢复测试；
8. 设计、计划、索引、复核清单和迁移矩阵已经回写实际验收结果；
9. 工作区改动范围清楚并形成 M6 独立提交。

## 8. 开工记录

M6 设计 Review 已于 2026-07-26 通过。开发者随后明确指示开始实现并使用刚安装的三节点
NATS 集群测试，本计划据此进入执行；实现不得改变已确认设计，发现新的方案取舍时仍需
暂停并重新确认。

## 9. 实施结果

M6 已按计划完成，实际生产代码位于 `internal/natsnet`，跨包真实协议测试位于
`tests/integration/natsnet`。

验收结果如下：

1. 固定依赖 `nats.go v1.52.0` 和 `nats-server/v2 v2.14.3`；
2. API、默认值、认证互斥、TLS 文件组合、URL 校验与脱敏均已实现并测试；
3. 发布订阅、Queue Group、空 payload、消息大小、Flush、Pending、慢消费者、统计、
   Handler panic、有限重连、自动重订阅和 Reconnect Buffer 均通过真实 Server 测试；
4. Connection/Subscription 的 Close、Drain、Wait、重复调用、并发调用和超时强制关闭
   已验证；
5. 用户名密码、Token、NKey、TLS 与双向 TLS 通过真实握手测试；Credentials File 使用
   官方 `nats.UserCredentials` 接入，并完成配置文件存在性和互斥校验；
6. `go test ./...`、`go test -race ./...`、重复测试、`go vet ./...` 和三平台构建通过；
7. Linux amd64 测试二进制在 Ubuntu 上运行通过；
8. 三节点外部集群完成跨节点发布订阅和首节点停止后的自动重连验证，测试后集群三个节点
   全部恢复运行；
9. 合并覆盖率为 `87.2%`；`Message` 包装基准为 `0.5545 ns/op`、`0 B/op`、
   `0 allocs/op`；
10. 外部集群凭据只通过环境变量传入，没有写入源码、文档或测试日志。
