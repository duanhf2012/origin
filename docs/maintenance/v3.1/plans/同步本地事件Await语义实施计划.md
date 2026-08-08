# 同步本地事件 Await 语义实施计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 允许 `NotifyEventSync` 监听器使用通用 Await 和生成的 `AwaitXxx` RPC，同时保留调用方等待、监听器顺序和 Service 单执行权语义。

**Architecture:** 复用现有 Service Task 的 Waiting/RecoveryReady/FIFO 恢复机制；同步事件嵌套深度跟随原 Task 穿越 Await，不引入新公开 API 或新调度器。

**Tech Stack:** Go 1.26.5，Origin Service Scheduler，Go testing。

## Global Constraints

- 不改变 `NotifyEventSync`、`Await` 或生成 RPC 客户端的公开签名。
- 同步事件嵌套上限保持 64。
- 不转为 `CallXxx`，不自动退化为异步事件。
- 保留工作区未跟踪的 `run/`，不纳入本次变更。

---

### Task 1: 同步监听器 Await 调度回归

**Files:**
- Modify: `service/event_test.go`
- Modify: `service/await.go`
- Modify: `service/scheduler.go`
- Modify: `service/event.go`

**Interfaces:**
- Consumes: `Service.NotifyEventSync(context.Context, Event) error`，`Service.Await(context.Context, func(context.Context) error) error`
- Produces: 不变的公开接口，放宽同步事件帧内 Await 语义

- [x] **Step 1: 写入失败测试**

  将旧的“同步监听器禁止 Await”测试改为：监听器进入 Await 后另一 Service Task 得以执行并解除等待，原监听器恢复后才继续下一监听器，最终顺序为 `listener-before -> interleaved-task -> listener-after -> next-listener`。

- [x] **Step 2: 验证测试因旧限制失败**

  Run: `go test ./service -run 'TestNotifyEventSyncAllowsAwaitAndPreservesListenerOrder' -count=1`

  Expected: FAIL，Await 在启动等待函数前返回 `ErrInvalidArgument`。

- [x] **Step 3: 实现最小调度改动**

  删除 `awaitTask` 对 `syncEventDepth != 0` 的拒绝；保留深度在原 Task 上，更新代码注释，不修改 Await 交接算法。

- [x] **Step 4: 验证局部测试通过**

  Run: `go test ./service -run 'TestNotifyEventSync' -count=1`

  Expected: PASS。

### Task 2: 消除内部 Await 的遗留预拒绝

**Files:**
- Modify: `service/retirement.go`
- Modify: `service/control.go`
- Test: `node/retirement_test.go`

**Interfaces:**
- Consumes: `Service.Retire(context.Context) error`，`Service.Resume(context.Context) error`
- Produces: 同步监听器内与普通 Service Task 一致的内部 Await 行为

- [x] **Step 1: 增加同步事件中状态转换的回归测试**

  使用真实 Node/Service 运行时，从同步事件监听器调用 `Retire`，断言不因同步事件帧返回 `ErrInvalidArgument`，并完成状态发布。

- [x] **Step 2: 验证旧代码拒绝**

  Run: `go test ./node -run 'TestServiceRetireFromSynchronousEventListener' -count=1`

  Expected: FAIL with `ErrInvalidArgument`。

- [x] **Step 3: 删除遗留特判**

  删除 `changeRunningState` 中的同步事件预检查，并删除已无调用者的 `synchronousEventActive`。

- [x] **Step 4: 验证状态转换测试**

  Run: `go test ./node -run 'TestServiceRetireFromSynchronousEventListener|TestServiceRetireResume' -count=1`

  Expected: PASS。

### Task 3: 设计、教程与示例同步

**Files:**
- Modify: `docs/maintenance/v3.1/README.md`
- Modify: `docs/maintenance/v3.1/guides/README.md`
- Create: `docs/maintenance/v3.1/changes/同步本地事件Await语义变更摘要.md`
- Modify: `docs/baseline/v3.0/guides/05.timer-event-and-execution.md`
- Modify: `examples/05-timer-event-and-execution/02-local-event/main.go`
- Modify: `examples/05-timer-event-and-execution/02-local-event/README.md`

**Interfaces:**
- Consumes: Task 1 的已验证语义
- Produces: 面向使用者的可运行 Await 示例与明确注意事项

- [x] **Step 1: 更新可运行示例**

  在同步监听器中使用 `Await` 模拟 I/O，把结果写入局部变量，Await 返回后再记录或更新 Service 状态。

- [x] **Step 2: 替换教程中的旧禁止规则**

  说明“调用方等待 + 监听器有序 + Await 期间可插入其他 Service Task”，同时给出通用 Await 和 `AwaitXxx` RPC 片段。

- [x] **Step 3: 更新索引与变更摘要**

  在 v3.1 索引链接设计、计划和变更记录，明确 v3.0 基线与 v3.1 行为差异。

- [x] **Step 4: 格式化并验证**

  Run: `gofmt -w service/event_test.go service/await.go service/scheduler.go service/event.go service/retirement.go service/control.go node/retirement_test.go examples/05-timer-event-and-execution/02-local-event/main.go`

  Run: `go test ./... -count=1`

  Run: `go test -race ./service ./node -count=1`

  Expected: 全部 PASS，教程不再将同步监听器 Await 说成非法调用。
