# Origin 第三版 M11 RPC 契约与代码生成实施计划

> 文档状态：已完成
>
> 创建日期：2026-07-28
>
> 对应设计：[M11 RPC 契约与代码生成设计](../design/milestones/M11-RPC契约与代码生成设计.md)

## 1. 目标

在现有 Application、Node、Service 调度器、M8 DeadlineQueue 和 M2 BufferPool 之上，
实现不经过 TCP/NATS 的真实同 Node RPC 闭环，并冻结后续 Transport 必须复用的契约、
生成代码、静态编解码、错误语义和业务调用外观。

M11 最终交付：

1. `//origin:rpc` Go 接口契约和 `origingen rpc`；
2. ContractID、MethodID 和完整契约指纹；
3. 强类型 Async、Await、Notify、Broadcast 客户端；
4. Service 实现自动识别和 Dispatcher 适配；
5. 每 Node 独立 RPC Runtime 和本地注册目录；
6. 基础类型、普通结构体、容器、顶层 Protobuf 和嵌套 Protobuf 的静态编解码；
7. 使用 Service Ready FIFO、唯一执行槽和统一 Deadline 的真实本地调用；
8. 生成期全类型图校验、`--check`、确定性生成和完整质量门禁。

## 2. 实施约束

- 严格遵守 [开发指导原则](../../AGENTS.md)和 M11 已确认范围。
- 第一个代码任务必须修复 M9 唯一计时器差异；该测试通过前不实现 RPC Runtime。
- RPC 热路径不使用反射、JSON、字符串方法查找、运行时 Codec 注册表或每调用辅助
  goroutine。
- 生成代码先计算准确大小，再取得一个最终 Buffer；业务可见 `[]byte` 仍安全复制。
- M11 不实现 TCP、NATS、RequestID、远程 pending 表、发现、多 Node Broadcast、路由
  策略、自定义 Codec、流式 RPC 或压缩。
- `localCall` 先保持不池化；只有对照 Benchmark 证明稳定收益且状态机仍清晰时才启用池。
- 新增和修改代码必须具有详细中文 GoDoc、步骤注释、所有权说明和并发不变量。
- 依赖固定为 `golang.org/x/tools v0.48.0` 和
  `google.golang.org/protobuf v1.36.11`。

## 3. 文件职责

### 3.1 新建

| 文件或目录 | 职责 |
|---|---|
| `rpc/` | Target、ID、静态 Codec 基础、ResponseWriter、Client、Runtime 和本地调用状态 |
| `cmd/origingen/` | `origingen rpc` 命令入口和退出码 |
| `internal/rpcgen/` | 包加载、契约模型、类型图校验、指纹、代码渲染及原子文件提交 |
| `tests/integration/rpcfixture/` | 生成代码参与的真实 Node/Service RPC 集成测试 |

### 3.2 修改

| 文件或目录 | 职责 |
|---|---|
| `service/await.go` | 显式 Deadline 只用 Go Timer；默认 Deadline 只用 M8 |
| `service/completion.go` | 复用普通根任务和 Await 的 Async 回调容量预约 |
| `service/runtime.go` | `RuntimeOf` 冷路径桥接 |
| `node/` | 每 Node RPC Runtime、Dispatcher 注册和停止清理 |
| `application/` | 把 Application 共享 BufferPool 交给 Node RPC Runtime |
| `errs/` | 固定 RPC 错误码 `2001`～`2010` |
| `go.mod`、`go.sum` | 固定生成器和 Protobuf 依赖 |
| `docs/` | 回写真实实现、测试、Benchmark 和跨平台验收 |

## 4. 执行任务

### Task 1：修复 M9 唯一计时器语义

- [x] 为默认 Await Deadline 增加不创建 Go Timer、但正确暴露 `Deadline()` 的私有 Context。
- [x] 显式 Deadline 只继承调用方 Context，不登记 M8 Deadline。
- [x] 默认 Deadline 只登记一条 M8 Deadline，并在完成、取消和停止时清理。
- [x] 补充显式、默认、预取消、恢复排队超时、重复 Await 和停止竞争测试。
- [x] 复用现有 Await Benchmark，并用绑定数量测试确认默认路径没有新增 Go Runtime Timer。

### Task 2：建立 RPC 基础类型和稳定错误

- [x] 增加 `CodeRPCNoRoute`～`CodeRPCBroadcastPartialFailed` 及固定哨兵。
- [x] 实现 ContractID、MethodID、ContractFingerprint、CallKind 和 Target。
- [x] 实现无反射的 Sizer、Writer、Reader、Protobuf 辅助和严格边界校验。
- [x] 实现 Runtime 栈上 `ResponseWriter`，保证响应只申请一次最终 Buffer。
- [x] 覆盖数值、nil/空、截断、溢出、尾部数据和最大消息边界。

### Task 3：建立 Service 调度桥接

- [x] 实现 `service.RuntimeOf`，nil、未绑定和有类型 nil 安全返回。
- [x] 实现带父 Context 的框架任务投递，目标任务必须获得自己的 Task Context。
- [x] 在提交目标前预约一个普通 Service 根任务；使用轻量门闩处理中止、超时和完成，
  不增加第二套 Scheduler Task 状态。
- [x] Async 默认超时复用所属 Service DeadlineQueue，显式 Deadline 不重复登记 M8。
- [x] 覆盖队列满、完成与超时竞争、提交失败不回调、回调严格一次和停止清理。

### Task 4：实现每 Node RPC Runtime

- [x] Runtime 使用 Application 共享 BufferPool，不建立全局目录。
- [x] Node 冷路径登记每个实际 ServiceName、Service 实例和可选 Dispatcher。
- [x] 检查重复注册、ContractID、Fingerprint、目标 Node 和 Service 状态。
- [x] 实现请求、通知和本地 Broadcast 投递；成功后请求 Buffer 所有权转给目标任务。
- [x] 请求 panic 转换为统一错误并在 RPC 边界记录一条堆栈；Notify panic 只记录目标侧
  诊断，不重复抛到 Service 根任务边界形成第二条日志。
- [x] Node 停止时拒绝新调用并释放 Runtime 所有权。

### Task 5：实现 origingen 契约扫描与全图校验

- [x] 使用 `go/packages` 加载当前 Module 包、语法、类型和依赖信息。
- [x] 识别紧邻接口的 `//origin:rpc`，拒绝别名、泛型、接口嵌入和非法签名。
- [x] 递归验证全部输入、输出、容器和导出字段，错误包含完整路径。
- [x] 支持基础类型、具名类型、指针、数组、Slice、Map、结构体、顶层 Protobuf 和嵌套
  Protobuf 普通结构。
- [x] 拒绝 `uintptr`、复数、unsafe.Pointer、接口、函数、Channel、循环类型、oneof、
  Opaque API、非法 Map Key 和无导出字段具名结构体。
- [x] 任一错误阻止全部生成文件写入。

### Task 6：实现稳定标识、指纹和确定性文件管理

- [x] 按确认的域前缀、UTF-8 规范名、SHA-256 前八字节大端生成 ID。
- [x] 规范化完整 Schema、Codec 标识和格式版本并生成完整 SHA-256 指纹。
- [x] 模块级检测 ContractID 和 MethodID 碰撞。
- [x] 生成内容稳定排序、gofmt、临时文件和同目录原子替换。
- [x] `origingen rpc --check ./...` 检查缺失、过期和多余文件且不修改工作树。
- [x] 仅删除带完整 origingen 生成头且本轮确认多余的文件，并拒绝覆盖同名手写文件。

### Task 7：生成静态 Codec、客户端和 Dispatcher

- [x] 为每个契约生成一个 Client、构造函数、ID、指纹和静态 Codec。
- [x] 为带返回值方法生成 Async/Await/Notify/Broadcast；无返回值生成 Notify/Broadcast。
- [x] 所有生成请求—响应方法保留最终 error，非法 error 位置在生成期失败。
- [x] Dispatcher 使用 MethodID switch、CallKind 和 ResponseWriter，不进行运行时反射。
- [x] 为每个 Service 实现包生成 `RPCDispatcher()` 薄适配和编译期断言。
- [x] 生成代码不引用 Origin `internal` 包，不通过 `init()` 注册任何全局状态。

### Task 8：完成同 Node RPC 闭环

- [x] Await 编码、提交、目标执行、响应和恢复均走真实调度路径。
- [x] Async 立即失败不回调，返回 nil 后回调严格一次并重新取得调用方 Service 执行权。
- [x] Notify/Broadcast 接受后不创建响应和超时项，目标业务 error/panic 不回传。
- [x] 同 Service 自调用释放执行槽，不直接递归且不死锁。
- [x] 成功、失败、超时、取消、panic、停止和队列满路径全部配平 Buffer。

### Task 9：生成器与集成测试

- [x] 建立包含两个 Service、多个 RPC、普通结构体、Map、`[]byte`、多输入输出和 Protobuf
  的实际生成样本。
- [x] 覆盖生成重复一致、`--check`、旧文件、碰撞和完整错误路径。
- [x] 覆盖 Await、Async、Notify、Broadcast、自调用、错误、panic、超时和取消。
- [x] 覆盖业务保存或修改 `[]byte` 后不受 Buffer 复用影响。
- [x] 覆盖多个独立 Node Runtime 的目录隔离、Runtime 关闭和 Node 停止。

### Task 10：性能与里程碑验收

- [x] 保存 Target、基础 Codec、生成 Await 闭环及 Await/Async localCall 的
  `ns/op`、`B/op`、`allocs/op`。
- [x] 覆盖 `16B`、`1KB`、接近 `4M` 消息，并在非 Windows 部署平台记录
  同 Node Await 的 P50/P95/P99。
- [x] 分析 localCall 不池化与仅池化外层对象的收益；收益不足时保持不池化。
- [x] 执行 `gofmt`、`go vet ./...`、`go test ./...`、`go test -race ./...`、覆盖率、
  逃逸分析、Windows/Linux 测试及 Linux/macOS 交叉构建。
- [x] 回写真实数据，把计划和设计状态改为已完成。
- [x] 确认工作树范围后形成唯一 M11 中文里程碑提交，并在提交后重新运行全仓测试。

## 5. 不变量

1. 同一 RPC 调用最多只有一个有效 Deadline 和一个物理计时器。
2. 生成前先完成全 Module 校验；失败时没有部分生成结果。
3. Service 业务方法只在所属 Service 唯一执行槽内运行。
4. 请求 Buffer 在提交成功后只有目标任务一个所有者；响应 Buffer 在解码后只释放一次。
5. Async 回调容量在目标提交前预约；提交失败中止预约且不调用业务回调。
6. Notify 和 Broadcast 被目标队列接受后不再受调用方 Context 取消影响。
7. Runtime 注册表启动后只读，多个 Node 和 Application 之间不共享可变状态。
8. 生成 Codec 不使用反射、JSON、unsafe 或运行时类型分派。
9. 不支持类型在 `origingen` 阶段失败，绝不生成静默空编码。
10. M11 完成前不引入任何 M12～M15 能力。

## 6. 当前状态

当前状态：**已完成。**

M11 于 2026-07-28 完成实现、生成、测试、Benchmark、Windows/Linux 运行验证和
Linux/macOS 交叉构建。真实实现数据与最终池化决策已同步回写到对应里程碑设计。
