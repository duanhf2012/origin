# TCP 网络首批纵向切片实施计划

> 状态：已完成实现、本地验收与 Ubuntu 验收
> 基线：v3.1.0 发布候选
> 目标：v3.2.0
> 兼容性：新增 v3.2 API；不改变 v3.1 已冻结公开外观
> 设计依据：[`Origin 网络模块核心设计`](../design/Origin网络模块核心设计.md)

## 1. 本切片交付范围

本计划只交付公共网络契约、Raw/PB/JSON/自定义 Codec 和 TCP Server、Client、Dialer。WebSocket、
KCP、Gin、公开 Drain、Broadcast、Token Bucket、优先级、动态 Framer/Codec 和公共 Buffer API
不进入本切片。

完成标准不是“代码可以连接”，而是公共契约、所有权、数量/字节容量、停止、重连、协议、测试、
Benchmark、Ubuntu 复验和教程同时完成。

## 2. 实施顺序

### 2.1 内部基础能力

1. [x] 为 `internal/bufferpool.Buffer` 增加只读容量查询、容量内 Resize 和独占 Slice 接管；
2. [x] 新建无队列语义的 `internal/bytebudget`，提供有界原子预留、释放和峰值快照；
3. [x] 把 `internal/tcpnet` 帧端序扩展为 Big/Little Endian；
4. [x] 把 TCP 固定槽位发送队列改为惰性增长 Ring，同时增加每连接数量/容量与共享总容量；
5. [x] 增加高低水位和慢连接裁决，但不改变 RPC Wire Format；
6. [x] 运行 bufferpool、bytebudget、ringqueue、tcpnet 与全部 RPC 测试，确认基础改动稳定。

### 2.2 公共网络契约

1. [x] 建立 `sysmodule/network` 的 SessionID、Transport、ByteOrder、Session、Handler/HandlerFuncs；
2. [x] 实现 EndpointOptions 默认值、严格校验和固定统计快照；
3. [x] 在 `sysmodule/network/internal` 实现 Session Runtime、入站额度、Service 投递、内部 Encoder 桥接、
   状态顺序和最终 Close；
4. [x] 所有外部 Raw `Send` 保持一次安全复制，内部协议编码路径转移唯一所有权。

### 2.3 标准协议与自定义 Codec

1. [x] 建立 `protocol` 的 MessageID、Codec、Resolver、Encoder、Router 和泛型 Register；
2. [x] Router 在 Module OnInit 冻结，运行期注册失败；
3. [x] 实现 Protobuf `uint16 ID + payload`，支持独立 Big/Little Endian；
4. [x] 实现 JSON `id/data` Envelope，使用稳定 `encoding/json`；
5. [x] 覆盖未知 ID、重复/零 ID、错误类型、panic 和借用 Buffer 生命周期。

### 2.4 TCP 纵向能力

1. [x] 实现 TCP Server Module：监听、Session 登记、查找、关闭、统计和完整停止；
2. [x] 实现 Dialer：一次 Context 约束的连接尝试；
3. [x] 实现 Client Module：当前 Session、状态回调、有界指数退避和停止取消；
4. [x] Server、Client、Dialer 使用同一 Session/Handler/协议/队列语义；
5. [x] 验证同一 Service 的 TCP 回环以及服务自己调用自己的网络入口不会死锁。

### 2.5 测试、文档与验收

1. [x] 单元测试覆盖稳定错误、边界、所有权、失败回滚、重复关闭和 panic；
2. [x] 集成测试覆盖真实 TCP、Raw/PB/JSON、Client 重连、Dialer 和回环；
3. [x] Fuzz 覆盖 1/2/4 字节 Big/Little Endian 帧与 PB/JSON Envelope；
4. [x] Race 覆盖重点并发、连接、队列和停止路径；
5. [x] Benchmark 保存帧与发送队列分配、吞吐结果；
6. [x] 在 Ubuntu `192.168.8.3` 执行真实协议、Race、Fuzz、稳定性和资源回收复验；凭证未进入仓库；
7. [x] 回写默认值、变更摘要、验收报告、教程和带完整中文注释的可运行 Example；
8. [x] 完成最终工作树复核后，按里程碑提交到 `v3` 分支。

## 3. 每一步的回归门禁

每次修改先执行相关包测试；完成内部 TCP 基础后执行 `go test ./internal/tcpnet ./rpc/...`，完成公共
协议后执行对应包测试，完成 TCP Module 后执行 `go test ./...`。提交前执行：

```text
gofmt（全部新增/修改 Go 文件）
go test ./...
go test -race ./...
go vet ./...
go build ./...
适用 Fuzz、Benchmark、覆盖率和 Ubuntu 验收命令
```

任何失败、竞态、泄漏、未解释低覆盖或文档与代码不一致都必须先解决，不能通过跳过测试或放宽
断言进入下一传输切片。
