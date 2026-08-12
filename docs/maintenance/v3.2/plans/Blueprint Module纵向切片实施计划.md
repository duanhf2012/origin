# Blueprint Module 纵向切片实施计划

> 状态：已完成
> 基线：Origin v3.0，目标版本：v3.2
> 设计依据：`../design/Origin Blueprint Module核心设计.md`
> 引擎依据：`github.com/duanhf2012/OriginBlueprint/engine/go/blueprint`，已验证提交 `14f0d1a`

## 1. 目标与实施策略

本切片交付 `sysmodule/blueprintmodule`、单元与 Origin Service 集成测试、一个完整游戏场景 Example、
使用指南、变更记录和双平台验收报告。实现只包装新版 Go 引擎的生命周期、Service 调度、Instance 所有权、
Execution 完成回调与显式热加载，不复制 Registry、Compiler、VM、热加载算法或业务异步节点。

实施顺序遵循“先冻结依赖与外观，再打通核心执行闭环，随后补热加载与可观测性，最后完成教程和全量验收”。
每个生产行为严格执行 RED → GREEN → REFACTOR；相关测试通过后再进入下一阶段，避免最后集中补测造成返工。

## 2. 全局约束

1. `v0.1.6` 发布提交 `e932de3` 的父提交为已验证的 `14f0d1a`，且两者的 `engine/go/blueprint` 目录零差异；
   Origin 固定正式版本，不提交本地 `replace`、旧版依赖或临时伪实现。
2. 公共外观以核心设计第 9 节为准，不增加 v2 兼容名、裸 `graphID` 执行入口、`Module.Start` 或
   `Engine()`。
3. `Run/Start/Reload` 由使用者在所属 Service 工作协程调用；首版通过文档和测试约束，不新增 goroutine
   身份强校验。
4. 首次节点执行内联于当前 Service 任务；Resume 后节点通过同一 Service 有界 FIFO；不得创建隐藏 Worker
   Pool、第二层队列或每 Execution 观察 goroutine。
5. 引擎工厂在加载、热加载和节点执行时均可能调用，每次必须返回新节点；工厂与 Trace/Diagnostic 回调不
   访问未加保护的 Service 业务字段。
6. 热加载只提供显式、单飞 `Reload`；编译失败不发布，活动 Execution 保持旧快照，下一次执行使用新图。
7. 所有导出标识符和重要内部状态具有完整中文注释；复杂函数按校验、状态转换、所有权和回滚分段说明。
8. 重点执行、恢复、取消、停止、热加载和回调路径尽量达到 100% 覆盖；不为数字编写无有效断言测试。
9. Windows 和 Ubuntu 都执行包级 Test/Race、全仓 Test/Race/Vet/Build；性能结论必须来自 Benchmark。

## 3. 实施任务

### Task 1：依赖门禁、包骨架与公共类型

**文件：**

- 修改：`go.mod`、`go.sum`
- 新建：`sysmodule/blueprintmodule/doc.go`
- 新建：`sysmodule/blueprintmodule/errors.go`
- 新建：`sysmodule/blueprintmodule/types.go`
- 新建：`sysmodule/blueprintmodule/option.go`
- 测试：`sysmodule/blueprintmodule/types_test.go`

**步骤：**

- [x] 确认远端 `v0.1.6` 发布提交为 `e932de3`，父提交是 `14f0d1a`，Go 引擎目录零差异；下载依赖并记录
      Go 版本和校验和。
- [x] 在 OriginBlueprint 发布基线运行 `go test ./engine/go/blueprint` 和 `go test -race
      ./engine/go/blueprint`。
- [x] 先写公共别名、错误别名、nil Option 与非法参数的编译/行为测试，并观察 RED。
- [x] 固定 `github.com/duanhf2012/OriginBlueprint v0.1.6`，实现设计确认的最小类型与错误外观。
- [x] 检查所有别名对应引擎真实导出名，不建立拼写兼容别名。

**验收：** 依赖可由 Windows、Ubuntu 和 Go Module Proxy 重复取得；包骨架可编译，公共外观无多余入口。

### Task 2：配置、节点注册与 Module 生命周期

**文件：**

- 新建：`sysmodule/blueprintmodule/config.go`
- 新建：`sysmodule/blueprintmodule/module.go`
- 新建：`sysmodule/blueprintmodule/dispatcher.go`
- 测试：`sysmodule/blueprintmodule/config_test.go`
- 测试：`sysmodule/blueprintmodule/module_test.go`
- 测试数据：`sysmodule/blueprintmodule/testdata/**`

**步骤：**

- [x] 先覆盖空目录、相对路径归一化、重复 Setup、未 Setup、nil 工厂、工厂返回 nil、自定义名称冲突、启动
      后注册与 Init 编译失败，观察 RED。
- [x] 实现无网络/文件 I/O 的 `New/Setup/RegisterNodes` 冷路径，配置转绝对路径并只冻结一次。
- [x] 实现 Module 状态机；OnStart 安装 Service Dispatcher、注册工厂并同步 Init，任一步失败不发布 Running。
- [x] 实现 OnStop 关闭准入、回收实例和关闭引擎；重复停止安全，启动失败不遗留可执行对象。
- [x] 用测试证明工厂每次返回独立节点，并记录工厂调用协程不得访问业务状态的约束。

**验收：** 生命周期所有稳定状态与错误路径均覆盖；没有包级可变状态、隐藏 goroutine或第二套蓝图注册表。

### Task 3：Instance、Execution 与同步/挂起执行闭环

**文件：**

- 新建：`sysmodule/blueprintmodule/instance.go`
- 新建：`sysmodule/blueprintmodule/execution.go`
- 修改：`sysmodule/blueprintmodule/module.go`
- 测试：`sysmodule/blueprintmodule/instance_test.go`
- 测试：`sysmodule/blueprintmodule/execution_test.go`
- 测试：`sysmodule/blueprintmodule/service_integration_test.go`

**步骤：**

- [x] 先写 Create 不存在图、WithKey、ID/Name/Key、指针使用、重复 Close、关闭后 Start、Module 停止兜底测试。
- [x] 实现 `*Instance` 包装与 noCopy 标记；Module 只维护生命周期索引，执行真相仍属于引擎。
- [x] 先写 Start 同步完成、首次 Yield 返回 Suspended、Run 同步快速路径和 Run 挂起协作等待测试。
- [x] 实现 Dispatcher：首次 SubmitInitial 当前任务内联，Resume Submit 进入所属 Service 有界 FIFO，拒绝错误原样
      进入 Execution 终态。
- [x] 实现 Execution 的 Done/State/IsDone/Result/Cancel，保持引擎错误链与结果快照语义。
- [x] 实现 `Module.Run` 临时实例：仅在终态释放，不提供异步临时实例入口。
- [x] 压测多个长期 Instance、多入口、多挂起 Execution 交错恢复，证明业务节点始终在 Service 串行环境。

**验收：** 同步路径不触发 Await；挂起等待释放 Service 执行权；外部 goroutine 只 Resume，后续节点回到同一
Service；Close/Cancel/Stop/迟到 Resume 竞态无泄漏。

### Task 4：OnComplete 完成任务与 Context 边界

**文件：**

- 修改：`sysmodule/blueprintmodule/execution.go`
- 测试：`sysmodule/blueprintmodule/completion_test.go`

**步骤：**

- [x] 先覆盖同步已完成 Execution、挂起后完成、单次登记、nil callback、Service 队列满、Context 取消与
      Deadline 测试，观察 RED。
- [x] 复用 `service.DispatchAsyncCompletion` 预留一个 Service 根任务；等待函数只等待 Done，不执行蓝图代码。
- [x] Deadline 到期时取消 Execution并等到终态；callback 使用新的任务 Context，在 Service 工作协程严格一次。
- [x] 注册失败不取消 Execution，句柄仍可由调用者 Run/Wait/Cancel；不创建观察 goroutine。

**验收：** callback 无内联重入；完成任务有界且可拒绝；取消、完成、队列拒绝竞争下无重复回调和资源泄漏。

### Task 5：单飞热加载与执行快照

**文件：**

- 新建：`sysmodule/blueprintmodule/reload.go`
- 测试：`sysmodule/blueprintmodule/reload_test.go`
- 扩充：`sysmodule/blueprintmodule/testdata/**`

**步骤：**

- [x] 先写成功 Reload、并发 Reload 快速拒绝、非法新图失败保留旧图和停止后拒绝测试。
- [x] 在 `Module.Await` 的等待函数中运行 HotReload；Service 释放执行权，普通任务仍可推进。
- [x] 覆盖旧 Execution 挂起 → Reload → 恢复得到旧结果；同一 Instance 下一次 Run 得到新结果。
- [x] 覆盖事务成功但 Context 在等待/恢复排队期间到期：Result 的 `Applied=true` 与 Deadline error 同时返回。
- [x] 验证发布只造成引擎短锁竞争，不在包装层增加图池、版本 Map 或迁移逻辑。

**验收：** 热加载失败无半成品；活动执行不乱序；并发 Reload 不排队、不合并；Service 无长时间阻塞。

### Task 6：Trace、诊断、统计、GoDoc 与 Benchmark

**文件：**

- 新建：`sysmodule/blueprintmodule/stats.go`
- 修改：`sysmodule/blueprintmodule/option.go`
- 修改：`sysmodule/blueprintmodule/module.go`
- 测试：`sysmodule/blueprintmodule/observability_test.go`
- 测试：`sysmodule/blueprintmodule/example_test.go`
- 测试：`sysmodule/blueprintmodule/benchmark_test.go`

**步骤：**

- [x] 覆盖 Trace 默认关闭、运行期切换、nil/custom logger 与 DiagnosticSink 失败事件。
- [x] 实现精确轻量 Stats；不为完成统计增加观察 goroutine或保留终态 Execution。
- [x] 补齐所有导出标识符、状态机、并发不变量和所有权转移的中文注释及可编译 Go Example。
- [x] Benchmark 同步 Run、长期 Instance Run、Create/Close、挂起/恢复调度与 Reload 发布；保存 ns/op、B/op、
      allocs/op，不凭猜测增加对象池。
- [x] 生成逐函数覆盖率报告，审查低覆盖函数并补充有业务价值的边界测试。

**验收：** 正常热路径没有额外监控分配或 goroutine；Trace 开销可开关；重点路径覆盖充分且 Benchmark 可重复。

### Task 7：完整游戏 Example、使用指南与文档入口

**文件：**

- 新建：`examples/18-blueprint/README.md`
- 新建：`examples/18-blueprint/01-battle-workflow/**`
- 新建：`docs/maintenance/v3.2/guides/Blueprint Module使用指南.md`
- 新建：`docs/maintenance/v3.2/changes/Blueprint Module纵向切片变更记录.md`
- 修改：`README.md`
- 修改：`examples/README.md`
- 修改：`docs/maintenance/v3.2/README.md`

**步骤：**

- [x] 用同一个 BattleBlueprintModule 展示严格配置、节点注册、Module.Run、长期 Instance、多入口与 Close。
- [x] 展示同步业务节点安全访问 Service 数据，以及异步 RPC 风格节点 Yield/ResumeTo；外部回调不访问业务字段。
- [x] 展示 Start + OnComplete、显式 Reload、旧执行快照、Trace 诊断窗口和完整错误处理。
- [x] 配置文件、README 和源代码逐项标注函数及参数所在协程、所有权、默认行为和禁止用法。
- [x] 指南按“十分钟接入 → 外观选择 → 配置 → 节点 → Run/Start → 异步恢复 → 热加载 → 可观测性 →
      游戏场景 → 故障排查 → 性能”组织，语言简洁且不假设使用者熟悉引擎。
- [x] Windows、Ubuntu 实际构建并运行 Example，核对输出和相对链接。

**验收：** Example 可独立运行，教程覆盖全部公共外观和关键失败路径；不改变既有教程章节结构。

### Task 8：Windows、Ubuntu、最终 Review 与提交

**文件：**

- 新建：`docs/maintenance/v3.2/reports/Blueprint Module纵向切片验收报告.md`
- 修改：本计划、核心设计及实际发现涉及的文档

**步骤：**

- [x] Windows 执行 `gofmt`、包级 Test/Race、全仓 Test/Race、Vet、Build、覆盖率和 Benchmark。
- [x] 将同一提交同步到 Ubuntu `192.168.8.3` 的隔离工作目录，执行相同 Test/Race/Vet/Build 与 Example 实跑；
      不在命令、日志或报告中记录密码。
- [x] 对照核心设计逐项 Review 公共外观、协程、取消、停止、快照、错误链、性能、GoDoc、教程和 Example。
- [x] 修复所有 Critical/Important 发现并重新运行受影响门禁；记录无法稳定触发分支与剩余风险。
- [x] 确认工作树只包含 Blueprint 切片及必要依赖变更，更新计划为已完成并形成单一里程碑提交。

**最终命令：**

```text
go test -race ./sysmodule/blueprintmodule -count=1
go test ./... -count=1
go test -race ./... -count=1
go vet ./...
go build ./...
git diff --check
```

**完成标准：** Windows 与 Ubuntu 全部门禁通过，设计、计划、代码、测试、Example、指南、变更记录与验收
报告一致；无已知竞态、泄漏、快照错乱或未解释失败后，提交 v3 分支。

## 4. 当前门禁

截至 2026-08-12，`v0.1.6` 已发布。发布提交 `e932de3` 仅在 `14f0d1a` 上修改 VERSION 与前端生成文件，
`engine/go/blueprint` 内容不变；发布基线的包级 Test/Race 已通过，依赖门禁解除。
