# Origin 第三版 M13 TCP 远程调用端到端闭环实施计划

> 文档状态：已完成
>
> 创建日期：2026-07-28
>
> 对应设计：[M13 TCP 远程调用端到端闭环设计](../design/milestones/M13-TCP远程调用端到端闭环设计.md)

## 1. 目标

在不改变 M11/M12 RPC 契约、生成客户端外观和业务数据编码格式的前提下，实现两个
Origin Node 通过真实 TCP 完成 Await、Async 和 Notify 调用：

1. 实现 `ORP1` 最小握手、Request、Notify、Response、Ping 和 Pong 线协议；
2. 使用 `NodeID + ServiceName` 精确选择目标，并在握手阶段校验完整契约指纹；
3. 实现每条出站会话独立的 RequestID、pendingCall、断线完成和有界容量；
4. 实现目标端 M8 Deadline、Service FIFO 调度和调用方断线后的已接收任务收尾；
5. 实现有界发送、重连、心跳、重复 NodeID 拒绝和显式地址迁移；
6. 使用 BufferPool headroom 原地前置协议头，不复制完整业务 payload；
7. 把 TCP 配置、Runtime 和资源释放接入 Node/Application 生命周期；
8. 通过同 Service 自调用、双 Node、双进程、Race、Fuzz、Benchmark 和跨平台测试验收。

## 2. 实施约束

- 严格遵守 [开发指导原则](../../../../AGENTS.md)和已确认 M13 设计；
- 不实现服务发现 Provider、NATS RPC、压缩、Cancel 包、流式 RPC、自动路由或自动重试；
- 跨 Node 调用不允许使用 Go 指针短路，同一 Application 内也必须经过真实 TCP；
- 网络 goroutine 只解析固定头和执行准入，不运行生成解码器或业务回调；
- 任一 Buffer、连接、goroutine、Deadline 和 pendingCall 都必须有唯一所有者和终止路径；
- 热路径不使用反射、JSON、方法字符串查找、每调用辅助 goroutine或重复 payload 拷贝；
- pendingCall 首先实现未池化基线，只有 Benchmark 证明收益后才决定是否启用专用池；
- 新增和修改代码必须具有完整中文 GoDoc、步骤注释和并发所有权说明。

## 3. 文件职责

### 3.1 `internal/bufferpool`

- 增加带 headroom 的取得方式；
- 增加原地 `Prepend` 和 `DiscardPrefix`；
- 保持 Release 归还原始完整容量及 TrackUsage 配平。

### 3.2 `rpc`

- `config.go`：Node 级 TCP RPC 冻结配置和校验；
- `wire.go`：`ORP1` 固定线协议及确定性解析；
- `remote_session.go`：出站会话、pendingCall、响应关联和断线完成；
- `remote_target.go`：目标 Add/Remove、单次 Dial、退避、重连和心跳；
- `tcp_handler.go`：Hello/HelloAck、重复 NodeID、入站准入和 Buffer 转移；
- `runtime.go`、`client.go`：本地/远端提交收敛、目标 Deadline 和响应恢复。

### 3.3 `node` 与 `application`

- 严格解析每个 Node 的 `rpc.transport=tcp` 配置；
- Node 在 TimerEngine 后启动 RPC Listener，在 Service 排空前停止网络准入；
- Application 内同时启动的多个 Node 通过内部目标输入建立真实 loopback TCP；
- 启动失败和停止按既有逆序规则清理 Listener、连接、pending 和 DeadlineQueue。

### 3.4 测试与文档

- 协议 golden、非法输入、Fuzz 和 Benchmark；
- Buffer headroom 所有权与统计；
- 重连、重复 NodeID、过载、断线和资源回落；
- Await、Async、Notify、自调用、错误、panic 和目标 Deadline；
- 单进程双 Node和独立进程 TCP RPC；
- 回写真实覆盖率、性能、池化结论和跨平台结果。

## 4. 执行任务

### Task 1：Buffer headroom 与协议基础

- [x] 实现 `AcquireWithHeadroom`、`Prepend` 和 `DiscardPrefix`。
- [x] 覆盖零 payload、容量档位、越界、重复视图变化和最终统计归零。
- [x] 实现 `ORP1` 全部包的 Big Endian 编解码。
- [x] 锁定固定头大小、最大名称、截断、未知 Kind、错误方向和错误 Magic。
- [x] 增加协议 Parser Fuzz 与基础 Benchmark。

### Task 2：RPC 配置与生成客户端适配

- [x] 实现 TCP RPC 默认配置、严格校验和历史内部字节额度派生；M15 按
  2026-07-29 最终结论删除字节额度。
- [x] M13 开发期配置公开 `send_queue_frames`；M15 按最终字段语义迁移为
  `send_queue_messages`，并同步迁移 `max_payload_size`、`read_idle_timeout`。
- [x] 生成请求编码器按 Request/Notify 取得准确 headroom。
- [x] 重复生成和 `origingen rpc --check ./...` 保持稳定。

### Task 3：出站会话与 pendingCall

- [x] 实现每会话最多 `65536` 个 pending Request。
- [x] 实现 RequestID 不回绕、响应严格一次、迟到响应丢弃和错误码恢复。
- [x] 发送失败撤销 pending，断线批量完成且不在锁内调度 Service。
- [x] 调用取消及时移除 pending，不增加第二个调用方 Timer。
- [x] 保存 pendingCall 值类型基线，按零分配结果决定不引入对象池。

### Task 4：TCP 握手、目标与连接管理

- [x] 实现 Hello/HelloAck 和公开 Service 指纹目录。
- [x] 实现先连接者保留、后来重复 NodeID 拒绝。
- [x] 实现目标 Add/Remove、相同地址幂等和不同地址拒绝。
- [x] 实现 `200ms` 到 `5s` 的有界指数退避及正负 `20%` 抖动。
- [x] 实现 Ping/Pong、ReadTimeout、WriteTimeout 和连接关闭原因。

### Task 5：入站请求与目标 Deadline

- [x] 网络 goroutine 完成固定头解析、契约准入和 Buffer 所有权转移。
- [x] Request 使用 Node 共享 M8 DeadlineQueue，不创建每请求 Go Timer。
- [x] Notify 不创建 RequestID、pending 或 Deadline。
- [x] 调用方断线后已准入任务继续执行，完成响应安全释放。
- [x] Service 队列拒绝返回稳定错误，Response/Pong 过载关闭连接。

### Task 6：Node/Application 生命周期

- [x] Node 在 OnInit 和 TimerEngine 后绑定 Listener。
- [x] Runtime 停止新准入并完成出站 pending，再由 Service 排空已接收任务。
- [x] Service 排空后关闭目标 DeadlineQueue 和最后的 RPC 资源。
- [x] Application 多 Node 使用配置顺序和真实 TCP，不共享连接或 Runtime。
- [x] 配置省略 RPC 时继续只支持同 Node RPC，不创建网络资源。

### Task 7：端到端和故障测试

- [x] Await、Async、Notify、空 payload、业务 error、panic 和编解码错误。
- [x] 普通 Go 类型、Protobuf、嵌套 Protobuf 和 M12 自定义 Codec。
- [x] 本 Service Await/Async/Notify 自调用不死锁且不形成框架转发环。
- [x] 默认/显式 Deadline、取消、断线、重连、迟到响应和手工重试边界。
- [x] 队列、pending、消息大小、重复 NodeID 和私有 Service 边界。
- [x] BufferPool、goroutine、连接和 Deadline 最终回落。

### Task 8：性能和里程碑验收

- [x] 保存协议头、headroom、pendingCall 和 loopback 端到端 Benchmark。
- [x] 记录 32B、1KB、64KB 和接近 `4M` payload 的分配与延迟。
- [x] 记录普通负载的 P50/P95/P99；过载以确定性的立即错误和资源回落测试验收。
- [x] 执行 `gofmt`、`go vet ./...`、`go test ./...`、`go test -race ./...`。
- [x] 执行覆盖率、Fuzz、逃逸分析、Windows/Linux 测试和跨平台构建。
- [x] 回写实际结果，将设计、计划、索引和复核状态更新为已完成。
- [x] 形成唯一 M13 中文提交并执行提交后复验。

## 5. 实现不变量

1. Service 业务方法只在所属 Service 的唯一执行槽内运行。
2. 同 Service RPC 也必须先入 FIFO；Await 释放执行权后目标任务才能执行。
3. TCP 请求成功入队后不自动重发，断线不能推断远端是否已经执行业务。
4. RequestID 只在原物理会话内有效，新会话不能命中旧 pending。
5. 已进入目标 Service FIFO 的任务不因调用方断线取消。
6. 一个 Buffer 任意时刻只有一个所有者，发送成功后只由 M5 Writer 释放。
7. 调用方显式 Deadline 只使用原 Go Timer；默认 Deadline 只使用所属 Service 的 M8。
8. 目标端使用自己的 M8 DeadlineQueue，不为每个请求创建 Go Runtime Timer。
9. 发送队列、pending、目标、连接和重试全部有界。
10. M13 不引入 M14/M15 或后续服务发现能力。

## 6. 当前状态

当前状态：**已完成。**

## 7. 实际验收摘要

- Windows 与 Linux 均通过 `go test ./...` 和 Race 验证；
- `origingen rpc --check ./...`、`go vet ./...`、覆盖率和协议 Fuzz 均通过；
- `linux/amd64`、`windows/amd64`、`darwin/arm64` 的纯 Go 交叉构建通过；
- Linux 真实 loopback TCP Await 基线为平均 `21.399µs`、P50 `20.527µs`、
  P95 `39.513µs`、P99 `52.797µs`；
- `pendingCall` 以值存入会话 Map，在 Windows/Linux 均为 `0 B/op`、
  `0 allocs/op`，因此没有增加对象池；
- 详细功能、测试矩阵和分档 payload 性能记录见对应 M13 设计文档第 24 节。
