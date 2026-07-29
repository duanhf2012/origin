# Origin 第三版 M15 NATS 远程调用端到端闭环设计

> 文档类型：里程碑设计（讨论中，原 M14 顺延）
> 创建日期：2026-07-29
> 最后更新：2026-07-29
> 当前状态：已保存前期确认结论；M14 完成后继续 Review，禁止编写实施代码

## 1. 顺延原因

TCP 和 NATS 远端发送已经统一要求先读取当前 Node 的本地服务发现快照。正式发现目录
尚未实现，因此先以 M14 实现公共发现内核，原 M14 NATS RPC 顺延为 M15。

本文先保存已经确认的 NATS RPC 结论，避免里程碑调整造成遗漏。M14 完成后仍需结合最终
目录接口进行一次开工 Review。

## 2. 已确认结论

### 2.1 Subject 与命名空间

- 配置提供显式 NATS RPC namespace，用于隔离开发、测试、预发布和生产环境；
- 示例 namespace：`game-prod`；
- 请求 Subject：`orpc.{namespace}.req.{targetNodeID}`；
- 响应 Subject：`orpc.{namespace}.resp.{sourceNodeID}`；
- 每个 Node 只建立 Node 级请求和响应订阅；
- ServiceName 留在 NATS RPC 线协议中；
- Subject 在 Node 启动时生成并缓存，RPC 热路径不重复拼接字符串；
- 不使用 Queue Group 完成普通精确 RPC 路由。

### 2.2 服务发现前置

- NATS 发送前必须查询 M14 当前 Node 的公共可见快照；
- 没有发现目标 `NodeID + ServiceName` 时立即返回 `CodeRPCNoRoute`；
- 不通过“向无订阅者 Subject 发布并等待 15 秒”代替服务发现；
- TCP 与 NATS 使用同一发现、契约、状态和错误判断；
- NATS 不建立第二份目标目录。

### 2.3 Transport 边界

- 每个 Node 只配置一种业务 RPC Transport；
- TCP Node 只直接调用 TCP Node；
- NATS Node 只直接调用 NATS Node；
- 同一 Application 可以混合运行 TCP/NATS Node；
- 首版不实现跨 Transport Bridge；
- 跨 Transport 目标返回 `CodeTransportUnavailable`。

### 2.4 Runtime 结构

- `rpc.Runtime` 直接选择 TCP 专用 Runtime 或 NATS 专用 Runtime；
- 不建立通用 `remoteTransport` 大接口；
- 不建立通用 Packet 抽象；
- 只共享 RequestID、Dispatcher、Deadline、错误和服务发现等真正共同的逻辑；
- 单个 Node 的热路径不通过 Transport 接口装箱或动态分派。

### 2.5 NATS 线协议

- TCP 继续使用已经冻结的 `ORP1`；
- NATS 使用独立、最小的 `ORN1` Envelope；
- 共享业务 payload 编解码、RequestID、MethodID、ServiceName、Deadline 和错误语义；
- 不把完整 ORP1 再嵌入 NATS；
- 不为了 NATS 修改 TCP 头并让 TCP 携带无用字段；
- 首版不增加压缩、Reserved 或预留 Flags。

### 2.6 契约校验

- 每个 NATS Request 和 Notify 都携带完整 32 字节 ContractFingerprint；
- 目标端在解码业务 payload 前完成指纹校验；
- 不增加契约目录 Subject；
- 不增加远端契约缓存或模拟 TCP 握手；
- 发生指纹不一致时返回稳定契约错误；Notify 记录限频诊断。

### 2.7 断线重连

- NATS 正在断线或重连时拒绝新的 RPC，立即返回
  `CodeTransportUnavailable`；
- 断线前已经提交的 Await/Async pending 保留到响应或原 Deadline；
- 不因瞬时断线立即完成这些 pending；
- 不自动重新发布或重试非幂等请求；
- NATS 连接终态关闭或重连耗尽时统一完成全部 pending；
- 恢复连接只服务后续新调用。

## 3. 尚未确认

1. `ORN1` Request、Notify、Response 的最终字段和字节布局；
2. 新调用与 NATS Client 内部重连缓冲的最终隔离方式；
3. NATS `Message.Data` 在异步投递到 Service 前采用 BufferPool 复制，还是在订阅回调中
   先静态解码；
4. Node 级 pending 上限；
5. Request Service 队列满、Notify 过载和慢消费者的诊断策略；
6. 停止阶段请求订阅、响应订阅、已准入任务和 pending 的顺序；
7. 默认 namespace、NodeID 格式和完整配置结构；
8. Windows/Linux 三节点 NATS 集群故障测试与性能门禁。

## 4. 开工门禁

M15 只有在以下条件全部满足后才允许编写实施计划：

1. M14 服务发现本地目录已经实现并验收；
2. 本文全部尚未确认项完成 Review；
3. M6 NATS 基础库与当前固定依赖版本重新完成兼容性检查；
4. TCP 与 NATS 共用的调用路径没有复制生成客户端、Dispatcher 或 Codec；
5. 真实三节点 NATS 集群测试方案已写入实施计划。
