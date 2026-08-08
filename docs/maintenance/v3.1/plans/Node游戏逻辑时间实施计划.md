# Node 游戏逻辑时间实施计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 为每个 Node 增加可设置、可增减的游戏逻辑时间，并让该 Node 全部业务 Timer 统一响应时间跳跃，同时保持基础设施 Deadline 使用真实单调时间。

**Architecture:** Node 持有原子时间偏移和低频修改锁，Service/Module 通过 `GetNode()` 获得最小 `NodeRuntime`。业务 Timer 保存逻辑名义时刻，仍把相对等待登记到既有真实 TimerEngine；时间修改时同步重排当前 Node 的 Scheduled Timer。

**Tech Stack:** Go 1.26、`sync/atomic`、既有 `internal/timerwheel`、ServiceScheduler、Go testing/benchmark/race detector。

## Global Constraints

- 基线为 v3.0，目标为 v3.1.0，不修改 RPC、发现和 Timer 既有公开方法签名。
- TimerEngine 和游戏逻辑时间均归属 Node；禁止包级可变状态和 Application 共享时间偏移。
- `AfterFunc`、`NewTicker`、`CronFunc` 使用逻辑时间；基础设施 Deadline 使用真实单调时间。
- Service、Module 入口固定为 `GetNode()`；不公开完整 `*node.Node`。
- `Now()` 热路径不得增加锁、goroutine 或堆分配。
- 所有行为修改遵循测试先行，并补充详细中文 GoDoc、关键并发和状态注释。

---

### Task 1: NodeRuntime 外观与 Node 逻辑时钟

**Files:**
- Create: `service/node_runtime.go`
- Create: `node/game_time.go`
- Modify: `service/runtime.go`
- Modify: `service/service.go`
- Modify: `service/module.go`
- Modify: `node/node.go`
- Test: `service/service_test.go`
- Test: `service/module_test.go`
- Test: `node/game_time_test.go`

**Interfaces:**
- Produces: `service.NodeRuntime`，包含 `ID() string`、`Now() time.Time`、`SetTime(time.Time) error`、`AddTime(time.Duration) error`。
- Produces: `(*service.Service).GetNode() service.NodeRuntime`、`(*service.Module).GetNode() service.NodeRuntime`。
- Produces: `(*node.Node).Now()`、`SetTime(time.Time) error`、`AddTime(time.Duration) error`。

- [x] **Step 1: 写 `GetNode` 和 Node 时间语义的失败测试**

  在 `service/service_test.go` 和 `service/module_test.go` 断言未绑定返回 `nil`、绑定后返回同一
  NodeID 与时钟外观；在 `node/game_time_test.go` 断言默认时间接近真实时间，Set/Add/负数/零
  增量正确，零 `time.Time` 和溢出不修改旧值。

- [x] **Step 2: 运行失败测试并确认失败原因是接口尚不存在**

  Run: `go test ./service ./node -run 'Test(Service|Module)GetNode|TestNodeGameTime' -count=1`
  Expected: FAIL，编译错误指向 `GetNode`、`Now`、`SetTime` 或 `AddTime` 尚未定义。

- [x] **Step 3: 实现最小 NodeRuntime 和原子偏移**

  `node/game_time.go` 使用 `atomic.Int64` 保存纳秒偏移，`Now` 返回
  `time.Now().Add(time.Duration(offset)).In(timerLocation)`；Set 通过回算校验目标是否可由
  `time.Duration` 表达，Add 在 Node 修改锁内检查 int64 溢出并线性化提交。Service Runtime 实现最小外观，
  `GetNode` 返回绑定 Runtime；Module 委托 owner Service。

- [x] **Step 4: 运行相关测试并保持通过**

  Run: `go test ./service ./node -run 'Test(Service|Module)GetNode|TestNodeGameTime' -count=1`
  Expected: PASS。

### Task 2: 业务 Timer 使用 Node 逻辑时间

**Files:**
- Modify: `service/timer_runtime.go`
- Modify: `service/timer_cron.go`
- Modify: `service/runtime.go`
- Modify: `node/node.go`
- Test: `service/timer_test.go`
- Test: `service/timer_cron_test.go`

**Interfaces:**
- Consumes: `service.Runtime.Now() time.Time`。
- Produces: `service.RebaseTimers(IService) error`，仅供 Node 在时间修改冷路径调用。

- [x] **Step 1: 写业务 Timer 读取逻辑时间的失败测试**

  使用现有可控 Timer fixture 分离真实 Engine 时钟和 Runtime 逻辑时钟，分别断言 After、Ticker、
  Cron 的名义 `fireAt` 来自 Runtime.Now，而 DeadlineQueue 仍使用真实 Engine delay。

- [x] **Step 2: 运行测试并观察旧实现错误使用 `timerEngine.Now()`**

  Run: `go test ./service -run 'TestBusinessTimerUsesNodeTime|TestCronUsesNodeTime' -count=1`
  Expected: FAIL，逻辑时钟与真实 Engine 时钟存在偏移时，名义触发点或触发结果不匹配。

- [x] **Step 3: 最小替换业务名义时间读取**

  创建、暂停剩余时长、恢复、到期复核、Ticker/Cron 续订统一读取 `scheduler.runtime.Now()`；
  `dueAt` 和 ReadyDelay 诊断继续读取 `scheduler.timerEngine.Now()`，确保延迟统计保持真实时间。
  到期复核从“仅 Cron”扩展为全部业务 Timer，逻辑时间早于 `fireAt` 时重新登记真实等待。

- [x] **Step 4: 运行 Service Timer 全部测试**

  Run: `go test ./service -run 'Test.*Timer|TestCron' -count=1`
  Expected: PASS。

### Task 3: 时间调整时重排当前 Node 全部 Scheduled Timer

**Files:**
- Modify: `node/game_time.go`
- Modify: `node/node.go`
- Modify: `service/timer_runtime.go`
- Test: `service/timer_test.go`
- Test: `node/game_time_test.go`

**Interfaces:**
- Consumes: `service.RebaseTimers(IService) error`。
- Produces: Set/Add 返回前完成该 Node 全部已准备 Scheduler 的 Scheduled Timer 重排。

- [x] **Step 1: 写前进、后退和跨 Service 重排失败测试**

  覆盖前进后 After 触发一次、Ticker 合并历史、Cron 合并历史；后退后 Timer 不提前触发；
  Service-A 调整时间会重排同 Node Service-B Timer；Paused、Ready、Running 不撤回或重复。

- [x] **Step 2: 运行测试并确认旧 Deadline 没有随偏移更新**

  Run: `go test ./service ./node -run 'TestGameTime(Rebase|Forward|Backward|AcrossServices|Paused|Ready|Running)' -count=1`
  Expected: FAIL，旧实现仍等待原真实 Deadline 或只影响单个 Scheduler。

- [x] **Step 3: 实现 Scheduler 重排和 Node 广播**

  `RebaseTimers` 在 Scheduler 锁内只处理 Scheduled：按 `fireAt - runtime.Now()` 调用
  TimerEngine 原地重排并保留 DeadlineID；旧 ID 已到期时删除 Binding、增加代次并登记新 ID。
  Node Set/Add 在线性化更新偏移后按稳定
  Service 顺序调用重排；零或负 delay 统一登记为零延迟，不在调用栈执行用户代码。

- [x] **Step 4: 固定 Stop 竞争边界**

  Node 进入 Stopping 前关闭时间修改准入；Set/Add 与 Stop 竞争时只有“完整重排后 Stop”或
  “直接返回生命周期错误”两种结果。Scheduler 已进入 Draining 时不再重排，由停止路径取消。

- [x] **Step 5: 运行相关包测试**

  Run: `go test ./service ./node -count=1`
  Expected: PASS。

### Task 4: 隔离、并发、性能和基础设施回归

**Files:**
- Modify: `node/game_time_test.go`
- Modify: `service/timer_benchmark_test.go`
- Modify: `node/benchmark_test.go`

**Interfaces:**
- Consumes: 完整 Node 时间和 Timer 重排能力。
- Produces: 可重复的隔离、race 和 allocation 证据。

- [x] **Step 1: 写多 Node 与基础设施隔离测试**

  两个真实 Node 各建业务 Timer，只调整其中一个并断言另一个时间和 Timer 不变；同时在共享
  TimerEngine 登记基础 Deadline，断言大幅 AddTime 不使其提前到期。

- [x] **Step 2: 写并发 Set/Add/Now/Timer 创建测试**

  使用固定数量 goroutine 和有界循环并发读取、调整和创建/取消 Timer，断言无丢失、无重复、
  无溢出提交，并由 race detector 检查共享状态。

- [x] **Step 3: 写 Benchmark**

  增加 `BenchmarkNodeGameTimeNow`，使用 `b.ReportAllocs()` 并要求结果为 0 allocs/op；增加
  1、1,000、100,000 Scheduled Timer 重排样本，记录 `ns/op`、`B/op`、`allocs/op`。

- [x] **Step 4: 运行并发与 Benchmark 验证**

  Run: `go test -race ./service ./node -count=1`
  Expected: PASS，无 race。

  Run: `go test ./node ./service -run '^$' -bench 'Benchmark(NodeGameTimeNow|GameTimeRebase)' -benchmem -count=3`
  Expected: PASS，Now 为 0 allocs/op；保存输出用于最终报告。

### Task 5: 教程、示例和 StartTimeout 表达优化

**Files:**
- Create: `examples/04-timer-event-and-execution/04-node-game-time/main.go`
- Create: `examples/04-timer-event-and-execution/04-node-game-time/config/application.yaml`
- Create: `examples/04-timer-event-and-execution/04-node-game-time/README.md`
- Create: `examples/04-timer-event-and-execution/04-node-game-time/run.bat`
- Create: `examples/04-timer-event-and-execution/04-node-game-time/run.sh`
- Modify: `examples/04-timer-event-and-execution/README.md`
- Modify: `docs/maintenance/v3.1/guides/README.md`
- Modify: `docs/maintenance/v3.1/README.md`
- Modify: `docs/baseline/v3.0/guides/01-first-application.md`
- Modify: `examples/01-first-application/03-application-options-and-command/README.md`

**Interfaces:**
- Consumes: `GetNode().Now/SetTime/AddTime` 和三类业务 Timer。
- Produces: 可直接运行的完整使用示例和版本隔离教程。

- [x] **Step 1: 创建带详细注释的游戏时间示例**

  示例在一个 Node 的两个 Service 中分别登记 After、Ticker、Cron，由管理 Service 调用
  `GetNode().AddTime`，日志同时输出真实时间与 Node 逻辑时间，证明跨 Service 生效且不会同步
  执行回调。

- [x] **Step 2: 编写 v3.1 教程**

  先给出读取、设置、增加时间的最小代码，再说明 Node 作用域、前进/后退/暂停规则、基础设施
  隔离、不持久化和生产权限控制；每段代码关联示例目录。

- [x] **Step 3: 简化 StartTimeout/StopTimeout 说明**

  把 v3.0 教程改成“怎么配置、超时后怎样、0 是什么”的短段落和对照表；保留完整但易懂的
  回滚、Context 与不能强杀阻塞 goroutine边界，不引入 v3.1 新功能。

- [x] **Step 4: 验证示例构建与 Markdown**

  Run: `go build ./examples/04-timer-event-and-execution/04-node-game-time`
  Expected: PASS。

  Run: `git diff --check`
  Expected: PASS，无空白错误。

### Task 6: 全仓验收

**Files:**
- Modify: `docs/maintenance/v3.1/changes/Node游戏逻辑时间变更摘要.md`
- Modify: `docs/maintenance/v3.1/reports/Node游戏逻辑时间验收报告.md`

**Interfaces:**
- Consumes: 前述全部实现、测试、Benchmark、教程和示例。
- Produces: v3.1 可审计变更与验收记录。

- [x] **Step 1: 执行格式化和静态检查**

  Run: `gofmt -w <本次修改的 Go 文件>`

  Run: `go vet ./...`
  Expected: PASS。

- [x] **Step 2: 执行全仓测试和竞态检查**

  Run: `go test ./... -count=1`
  Expected: PASS。

  Run: `go test -race ./service ./node -count=1`
  Expected: PASS。

- [x] **Step 3: 检查跨平台编译**

  Run: `$env:GOOS='linux'; $env:GOARCH='amd64'; go test ./... -run '^$'; Remove-Item Env:GOOS; Remove-Item Env:GOARCH`
  Expected: PASS。

- [x] **Step 4: 生成覆盖率并复核新增路径**

  Run: `go test ./service ./node -coverprofile=game-time.coverage.out -count=1`

  Run: `go tool cover -func=game-time.coverage.out`
  Expected: `GetNode`、时间校验、前后跳、重排、生命周期拒绝均有真实行为测试覆盖。

- [x] **Step 5: 写入变更摘要和验收数据**

  记录公开 API、兼容性、测试命令、race、跨平台构建、覆盖率和 Benchmark 实测结果，不写未经
  执行的结论。
