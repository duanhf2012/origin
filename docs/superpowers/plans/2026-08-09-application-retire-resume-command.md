# Application Runtime Retire/Resume Command Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 增加可跨进程控制运行中 Application 全量退休和恢复的 `retire/resume` 子命令，删除初始 Retired 与 v2 兼容入口，并同步状态事件示例、设计文档和教程。

**Architecture:** `command` 包在 PID 目录中使用单请求、有限大小的请求/响应邮箱，把强类型 `ControlRequest` 交给目标进程的 Start Handler；`application` 在唯一生命周期循环中串行调用现有 `Application.Retire/Resume`。控制命令用同一个总体 timeout 等待锁、目标处理和响应，超时不杀进程、不回滚已经提交的 Service 状态。

**Tech Stack:** Go 1.24、标准库 `context/encoding/json/os/path/filepath/runtime/debug/time`、现有 `command/internal/processlock`、Origin `errs`、Go `testing`、真实跨进程集成测试。

## Global Constraints

- 当前尚未对外发布：直接删除 `start --retired`、`InitialRetired` 和 `-start/-stop/-help` 等 v2 兼容入口，不增加弃用别名。
- 正式命令固定为 `game-server retire|resume --app-name <name> [--pid-dir ./run] [--timeout 30s]`。
- `retire/resume` 只控制指定 AppName 的整个 Application，不增加 Node/Service 级命令。
- Linux、macOS、Windows 使用同一请求/响应协议；不新增 TCP、HTTP、Unix Socket 或外部依赖。
- 同一 Application 同时最多一个在线控制请求；请求和响应均有固定大小上限，不建立无界队列或 goroutine。
- timeout 是命令端总体 deadline，不杀目标进程、不触发 Stop、不回滚已提交状态。
- Application Retire 继续按 Node/Service 逆序，Resume 继续按正序，批量调用保持 best-effort 错误聚合。
- `ServiceStateChanged` 只在真实状态变化后异步投递；幂等 Retire/Resume 不重复投递。
- 所有代码修改使用详细中文注释；公开类型、方法和常量具有中文 GoDoc。
- 不覆盖当前工作树中的其他修改；每次提交只暂存本任务列出的文件。
- 执行前从包含设计提交 `9f987c2` 的当前 HEAD 创建隔离 worktree；当前主工作树已有未提交的 RPC/Discovery 修改，不在该工作树中直接开发或提交本功能。

---

## File Structure

### 新建文件

- `command/control.go`：公开控制动作/请求接口、磁盘协议记录、固定路径和严格编解码。
- `command/control_mailbox.go`：命令进程控制锁、请求提交、目标邮箱协程、结果等待和清理。
- `command/retirement.go`：`retire/resume` 参数解析及 Runner 执行入口。
- `command/control_test.go`：协议、编解码、路径、请求完成和边界测试。
- `command/control_mailbox_test.go`：单进程邮箱生命周期、串行化、timeout 和陈旧文件测试。
- `examples/09-retire-and-resume/01-service-retire-resume/retire.{bat,sh}`：执行 Application Retire。
- `examples/09-retire-and-resume/01-service-retire-resume/resume.{bat,sh}`：执行 Application Resume。
- `examples/09-retire-and-resume/01-service-retire-resume/stop.{bat,sh}`：停止示例进程并回收 PID 锁。

### 重点修改文件

- `command/command.go`：删除 `InitialRetired`，增加 `ControlAction`、`ControlRequest` 通道和通用控制 timeout 退出码。
- `command/runner.go`：只接受正式主命令，路由 `retire/resume`，把新命令设为保留名称。
- `command/start.go`：删除 `--retired`，启动并关闭控制邮箱，把请求通道交给 Start Handler。
- `command/stop.go`：复用统一目标参数解析并采用 `ExitControlTimeout`。
- `command/help.go`：更新命令清单和 Usage，删除兼容名称归一化。
- `command/process.go`：增加固定控制文件路径帮助函数，保持 PID 锁为目标身份依据。
- `application/application.go`：删除初始 Retired 传播，在 Running 主循环串行处理 ControlRequest。
- `application/retirement.go`：增加控制请求执行与 panic 隔离帮助函数。
- `node/config.go`、`node/node.go`：删除 InitialRetired 字段和启动分支，启动成功固定进入 Running。
- `tests/helpers/commandprocess/main.go`：让真实辅助进程消费 Retire/Resume 控制请求并记录状态。
- `tests/integration/command/command_test.go`：增加跨进程 retire/resume、timeout、串行和清理测试。
- `examples/09-retire-and-resume/01-service-retire-resume/main.go`：注册 `ServiceStateChanged`，不再用 Timer 自行切换状态。
- 对应 M4/M21/服务退休设计、教程第 01/09 章、API 索引、命令示例和 README：统一正式语义。

---

### Task 1: 删除初始 Retired 和 v2 命令兼容入口

**Files:**
- Modify: `command/command_test.go`
- Modify: `command/coverage_test.go`
- Modify: `command/example_test.go`
- Modify: `command/runner.go`
- Modify: `command/help.go`
- Modify: `command/start.go`
- Modify: `command/command.go`
- Modify: `application/application.go`
- Modify: `application/application_test.go`
- Modify: `node/config.go`
- Modify: `node/node.go`
- Modify: `node/retirement_test.go`

**Interfaces:**
- Consumes: 当前 `Runner.Run`、`StartRequest`、`node.Options` 和 Node 启动状态机。
- Produces: 不含 `InitialRetired` 的 `StartRequest`；只接受正式子命令的 Runner；Service 启动固定进入 `Running`。

- [ ] **Step 1: 把兼容测试改成正式外观和拒绝测试**

在 `command/command_test.go` 将 `TestRunUsageErrorsAndAliases` 改为 `TestRunUsageErrorsAndRejectsLegacyCommands`，用下列样本锁定删除行为：

```go
tests := []struct {
    name     string
    args     []string
    wantCode ExitCode
    wantErr  errs.Code
}{
    {name: "no command", args: nil, wantCode: ExitUsage, wantErr: errs.CodeInvalidArgument},
    {name: "unknown", args: []string{"missing"}, wantCode: ExitUsage, wantErr: errs.CodeInvalidArgument},
    {name: "legacy start", args: []string{"-start"}, wantCode: ExitUsage, wantErr: errs.CodeInvalidArgument},
    {name: "legacy stop", args: []string{"-stop"}, wantCode: ExitUsage, wantErr: errs.CodeInvalidArgument},
    {name: "legacy help", args: []string{"-help"}, wantCode: ExitUsage, wantErr: errs.CodeInvalidArgument},
    {name: "legacy short help", args: []string{"-h"}, wantCode: ExitUsage, wantErr: errs.CodeInvalidArgument},
    {name: "legacy long help", args: []string{"--help"}, wantCode: ExitUsage, wantErr: errs.CodeInvalidArgument},
}
```

在 `TestStartBuildsRequestAndPIDRecord` 中把主命令改为 `start`，删除 `--retired` 和
`received.InitialRetired` 断言。向 `TestStartRejectsInvalidArgumentsBeforeHandler` 增加：

```go
{
    name: "removed retired option",
    args: []string{"start", "--app-name", "game", "--config", configDir, "--retired"},
    code: errs.CodeInvalidArgument,
},
```

- [ ] **Step 2: 运行 command 测试确认旧实现失败**

Run: `go test ./command -run 'TestRunUsageErrorsAndRejectsLegacyCommands|TestStartBuildsRequestAndPIDRecord|TestStartRejectsInvalidArgumentsBeforeHandler' -count=1`

Expected: FAIL；旧别名仍成功、`--retired` 仍被接受或测试仍引用 `InitialRetired`。

- [ ] **Step 3: 删除命令层兼容和初始状态数据**

在 `command/runner.go` 直接使用首参数，不再归一化：

```go
name := args[0]
commandArgs := args[1:]
```

删除 `normalizeCommandName`。在 `command/help.go` 的 `runHelp` 同样直接使用
`args[0]`；从 start Usage 和 `runStart` flag 集中删除 `--retired`；从 `StartRequest` 删除：

```go
InitialRetired bool
```

- [ ] **Step 4: 删除 Application/Node 初始 Retired 传播**

把 `app.buildNodes` 恢复为只接收配置和发现选择：

```go
func (app *Application) buildNodes(
    configs []node.Config,
    discovery *discoverySelection,
) ([]*node.Node, error)
```

调用处改为 `app.buildNodes(selected, configured.discovery)`；删除
`node.Options.InitialRetired`、`Node.initialRetired` 及赋值。Node 启动成功固定执行：

```go
entry.setState(service.StateRunning)
```

删除 `TestApplicationInitialRetiredPropagatesStartFlag` 和
`TestNodeInitialRetiredPublishesNoRunningWindow`；保留普通启动/首次 Running 快照测试作为正向覆盖。

- [ ] **Step 5: 格式化并运行受影响测试**

Run: `gofmt -w command/command.go command/runner.go command/help.go command/start.go command/command_test.go command/coverage_test.go command/example_test.go application/application.go application/application_test.go node/config.go node/node.go node/retirement_test.go`

Run: `go test ./command ./application ./node -count=1`

Expected: PASS。

- [ ] **Step 6: 提交删除改动**

```bash
git add command/command.go command/runner.go command/help.go command/start.go command/command_test.go command/coverage_test.go command/example_test.go application/application.go application/application_test.go node/config.go node/node.go node/retirement_test.go
git commit -m "refactor(M4): 删除初始退休和旧命令兼容"
```

---

### Task 2: 定义有界控制协议和强类型请求接口

**Files:**
- Create: `command/control.go`
- Create: `command/control_test.go`
- Modify: `command/command.go`
- Modify: `command/process.go`

**Interfaces:**
- Consumes: `errs.Code`、PID 目录和标准库 JSON/Context。
- Produces: `type ControlAction uint8`、`ControlActionRetire`、`ControlActionResume`、`type ControlRequest interface`、`StartRequest.Controls <-chan ControlRequest`、严格磁盘记录编解码与固定控制路径。

- [ ] **Step 1: 写协议失败测试**

在 `command/control_test.go` 添加：

```go
func TestControlRequestCodecIsStrictAndBounded(t *testing.T) {
    deadline := time.Now().Add(time.Minute).UTC().Format(time.RFC3339Nano)
    valid := []byte(`{"id":"0123456789abcdef0123456789abcdef","action":"retire","deadline":"` + deadline + `"}`)
    request, err := decodeControlRequest(valid)
    if err != nil || request.Action != controlActionRetireText {
        t.Fatalf("decodeControlRequest() = (%+v, %v)", request, err)
    }
    for _, invalid := range [][]byte{
        append(valid[:len(valid)-1], []byte(`,"extra":true}`)...),
        []byte(`{"id":"short","action":"retire","deadline":"` + deadline + `"}`),
        []byte(`{"id":"0123456789abcdef0123456789abcdef","action":"stop","deadline":"` + deadline + `"}`),
        bytes.Repeat([]byte{'x'}, maxControlRecordSize+1),
    } {
        if _, err := decodeControlRequest(invalid); err == nil {
            t.Fatalf("decodeControlRequest(%q) error = nil", invalid)
        }
    }
}
```

再测试 response 未知字段、非法 Origin 错误码、请求 ID 不匹配，以及四个路径均位于 PID 目录且使用 AppName 前缀。

- [ ] **Step 2: 运行测试确认协议尚不存在**

Run: `go test ./command -run 'TestControl(Request|Response|Paths)' -count=1`

Expected: FAIL with undefined `decodeControlRequest`, `maxControlRecordSize` 或路径函数。

- [ ] **Step 3: 实现公开动作和请求接口**

在 `command/control.go` 定义：

```go
type ControlAction uint8

const (
    ControlActionRetire ControlAction = iota + 1
    ControlActionResume
)

type ControlRequest interface {
    Action() ControlAction
    Context() context.Context
    Complete(error)
}

const maxControlRecordSize = 4 * 1024
```

在 `StartRequest` 增加：

```go
// Controls 由当前 start 持有的本地控制邮箱提供；nil 表示没有在线控制入口。
Controls <-chan ControlRequest
```

磁盘请求固定字段为 `ID`、`Action`、`Deadline`；响应固定字段为 `ID`、`Success`、
`ErrorCode`、`Message`。编码使用 `json.Marshal`，解码使用 `json.Decoder.DisallowUnknownFields()`，
并在 `Decode` 后再次读取以拒绝拼接 JSON。

- [ ] **Step 4: 增加固定路径和原子文件帮助函数**

在 `command/process.go` 增加：

```go
func controlLockPath(pidDir, appName string) string {
    return filepath.Join(pidDir, appName+".control.lock")
}
func controlRequestPath(pidDir, appName string) string {
    return filepath.Join(pidDir, appName+".control.request")
}
func controlProcessingPath(pidDir, appName string) string {
    return filepath.Join(pidDir, appName+".control.processing")
}
func controlResponsePath(pidDir, appName string) string {
    return filepath.Join(pidDir, appName+".control.response")
}
```

在 `control.go` 实现 `readBoundedRegularFile` 和 `writeControlRecordAtomic`：只接受普通文件，
读取上限为 `maxControlRecordSize+1`，临时文件权限 `0600`，完成 `Write/Sync/Close/Rename`，失败时删除临时文件。

- [ ] **Step 5: 运行协议测试**

Run: `gofmt -w command/control.go command/control_test.go command/command.go command/process.go`

Run: `go test ./command -run 'TestControl(Request|Response|Paths|Record)' -count=1`

Expected: PASS。

- [ ] **Step 6: 提交协议类型**

```bash
git add command/control.go command/control_test.go command/command.go command/process.go
git commit -m "feat(M4): 定义运行期控制协议"
```

---

### Task 3: 实现跨平台单请求控制邮箱

**Files:**
- Create: `command/control_mailbox.go`
- Create: `command/control_mailbox_test.go`
- Modify: `command/start.go`

**Interfaces:**
- Consumes: Task 2 的 `ControlRequest`、控制记录编解码和路径函数；现有 `processlock.TryLock/Release`。
- Produces: `startControlMailbox(context.Context, string, string) (*controlMailbox, error)`、`requestApplicationControl(context.Context, string, string, ControlAction) error`。

- [ ] **Step 1: 写正常往返和幂等完成失败测试**

在 `command/control_mailbox_test.go` 用真实临时目录启动邮箱：

```go
func TestControlMailboxRoundTrip(t *testing.T) {
    pidDir := t.TempDir()
    lease, err := acquirePIDLease(pidDir, "game")
    if err != nil {
        t.Fatal(err)
    }
    defer lease.close()
    mailbox, err := startControlMailbox(t.Context(), pidDir, "game")
    if err != nil {
        t.Fatal(err)
    }
    defer mailbox.close()

    result := make(chan error, 1)
    go func() {
        result <- requestApplicationControl(t.Context(), pidDir, "game", ControlActionRetire)
    }()
    request := <-mailbox.requests
    if request.Action() != ControlActionRetire {
        t.Fatalf("Action = %v", request.Action())
    }
    request.Complete(nil)
    request.Complete(errors.New("second completion must be ignored"))
    if err := <-result; err != nil {
        t.Fatalf("requestApplicationControl() error = %v", err)
    }
}
```

测试 response 保留 `errs.CodeDiscoveryUnavailable` 和有界 message；第二次 `Complete` 不阻塞、不覆盖第一次结果。

- [ ] **Step 2: 写 timeout、串行和陈旧文件失败测试**

覆盖：

```go
func TestControlMailboxTimeoutDoesNotDeleteProcessingRequest(t *testing.T)
func TestControlMailboxSerializesConcurrentCommands(t *testing.T)
func TestControlMailboxStartCleansStaleFiles(t *testing.T)
func TestControlMailboxRejectsDirectoryAsControlFile(t *testing.T)
func TestControlMailboxCloseCompletesPendingRequest(t *testing.T)
```

串行测试同时提交 Retire/Resume，断言第二个请求在第一个 `Complete` 前不会出现在
`mailbox.requests`；timeout 测试断言返回 `errs.CodeDeadlineExceeded` 且不终止邮箱。

- [ ] **Step 3: 运行测试确认邮箱尚不存在**

Run: `go test ./command -run 'TestControlMailbox' -count=1`

Expected: FAIL with undefined `startControlMailbox` 或 `requestApplicationControl`。

- [ ] **Step 4: 实现请求对象和邮箱生命周期**

在 `command/control_mailbox.go` 定义：

```go
type controlRequest struct {
    action   ControlAction
    ctx      context.Context
    complete func(error)
}

func (request *controlRequest) Action() ControlAction      { return request.action }
func (request *controlRequest) Context() context.Context  { return request.ctx }
func (request *controlRequest) Complete(err error)        { request.complete(err) }

type controlMailbox struct {
    requests <-chan ControlRequest
    cancel   context.CancelFunc
    done     chan struct{}
    path     controlPaths
}
```

`startControlMailbox` 在启动 goroutine 前清理 request/processing/response，保留 control.lock；
goroutine 每 `25ms` 检查 `.request`，原子改名 `.processing`，严格解码并创建 deadline Context，
把单个请求送入单槽 channel，等待 `Complete` 或父 Context，原子写响应并删除 processing。

- [ ] **Step 5: 实现命令进程串行提交**

`requestApplicationControl` 固定执行：

```go
func requestApplicationControl(
    ctx context.Context,
    pidDir string,
    appName string,
    action ControlAction,
) error
```

它在 deadline 内轮询取得 `control.lock`，取得后再次调用 `readRunningPID`，清理仅属于已完成旧请求的 response，生成 16 字节 `crypto/rand` 请求 ID，原子写 request，等待 ID 匹配的 response；退出时只清理自己已确认归属的 request/response，不删除目标持有的 processing 文件。

- [ ] **Step 6: 把邮箱接入 start 的资源所有权**

`runStart` 在 PID lease 和平台 Stop 控制建立后调用：

```go
mailbox, err := startControlMailbox(runCtx, absolutePIDDir, *appName)
if err != nil {
    return ExitProcessControl, joinExecutionErrors(err, closeControl(), lease.close())
}
request.Controls = mailbox.requests
```

Handler 返回后固定按邮箱、平台控制、PID lease 的逆序关闭，并聚合全部清理错误。所有 defer 和正常清理继续使用现有幂等包装，邮箱 `close` 必须等待唯一 goroutine 退出。

- [ ] **Step 7: 运行邮箱和 start 资源测试**

Run: `gofmt -w command/control_mailbox.go command/control_mailbox_test.go command/start.go`

Run: `go test ./command -run 'TestControlMailbox|TestStart|TestParentCancellation' -count=1`

Run: `go test -race ./command -run 'TestControlMailbox|TestStart' -count=1`

Expected: PASS，且无竞态或 goroutine 泄漏。

- [ ] **Step 8: 提交邮箱实现**

```bash
git add command/control_mailbox.go command/control_mailbox_test.go command/start.go
git commit -m "feat(M4): 实现跨平台控制邮箱"
```

---

### Task 4: 增加 retire/resume 内置命令和统一 timeout

**Files:**
- Create: `command/retirement.go`
- Modify: `command/runner.go`
- Modify: `command/help.go`
- Modify: `command/stop.go`
- Modify: `command/command.go`
- Modify: `command/command_test.go`
- Modify: `command/coverage_test.go`
- Modify: `command/example_test.go`

**Interfaces:**
- Consumes: Task 3 的 `requestApplicationControl`。
- Produces: `runRetire`、`runResume`、共享 `parseControlTarget`，正式帮助和 `ExitControlTimeout`。

- [ ] **Step 1: 写内置命令解析失败测试**

在 `command/command_test.go` 添加表格：

```go
func TestRetireResumeArguments(t *testing.T) {
    tests := []struct {
        name string
        args []string
    }{
        {name: "retire missing app", args: []string{"retire"}},
        {name: "resume invalid app", args: []string{"resume", "--app-name", "Game"}},
        {name: "retire zero timeout", args: []string{"retire", "--app-name", "game", "--timeout", "0s"}},
        {name: "resume invalid timeout", args: []string{"resume", "--app-name", "game", "--timeout", "soon"}},
        {name: "retire positional", args: []string{"retire", "--app-name", "game", "extra"}},
    }
    for _, test := range tests {
        runner, _, _ := newTestRunner(t, noOpStart)
        code, err := runner.Run(t.Context(), test.args)
        if code != ExitUsage || !errs.IsCode(err, errs.CodeInvalidArgument) {
            t.Fatalf("%s = (%d, %v)", test.name, code, err)
        }
    }
}
```

帮助测试断言 `retire`、`resume` 出现在内置命令列表，`help retire`/`help resume` 包含
`--app-name`、`--pid-dir` 和 `--timeout 30s`。

- [ ] **Step 2: 运行测试确认命令尚未路由**

Run: `go test ./command -run 'TestRetireResumeArguments|TestHelpAndVersion' -count=1`

Expected: FAIL with unknown command `retire` 或缺少帮助文本。

- [ ] **Step 3: 实现共享控制目标解析**

在 `command/retirement.go` 定义：

```go
type controlTarget struct {
    appName string
    pidDir  string
    exists  bool
    timeout time.Duration
}

func (runner *Runner) parseControlTarget(
    commandName string,
    args []string,
) (controlTarget, ExitCode, error)
```

该函数使用独立 `flag.FlagSet`，默认 pid-dir `./run`、timeout `30s`，拒绝位置参数，调用
`validateKebabName`、`time.ParseDuration` 和 `resolvePIDDirForStop`。不存在的 PID 目录不创建，
并通过 `exists=false` 交给命令决定语义；返回的 pid-dir 如果存在则为绝对路径。

- [ ] **Step 4: 实现 retire/resume 并更新 Runner/帮助**

实现：

```go
func (runner *Runner) runRetire(ctx context.Context, args []string) (ExitCode, error) {
    return runner.runApplicationControl(ctx, "retire", ControlActionRetire, args)
}

func (runner *Runner) runResume(ctx context.Context, args []string) (ExitCode, error) {
    return runner.runApplicationControl(ctx, "resume", ControlActionResume, args)
}
```

`runApplicationControl` 使用 `context.WithTimeout(ctx, target.timeout)` 覆盖定位、控制锁、请求和响应；目标未运行返回 `ExitProcessControl`；deadline 返回 `ExitControlTimeout`；请求已送达后目标返回的 Origin 错误使用 `ExitFailure`。在 Runner switch、`isBuiltInCommand`、总帮助和内置帮助中加入两个命令。

- [ ] **Step 5: 让 stop 复用解析并统一 timeout 名称**

把 `ExitStopTimeout` 直接改名为：

```go
// ExitControlTimeout 表示在线控制命令超过调用方指定的总体等待时间。
ExitControlTimeout
```

不保留旧常量别名。`runStop` 使用 `parseControlTarget("stop", args)`；stop 未运行仍输出
`not running` 并成功，stop 等待 PID 解锁超时返回 `ExitControlTimeout`。

- [ ] **Step 6: 运行 command 全包测试**

Run: `gofmt -w command/retirement.go command/runner.go command/help.go command/stop.go command/command.go command/command_test.go command/coverage_test.go command/example_test.go`

Run: `go test ./command -count=1`

Expected: PASS。

- [ ] **Step 7: 提交命令外观**

```bash
git add command/retirement.go command/runner.go command/help.go command/stop.go command/command.go command/command_test.go command/coverage_test.go command/example_test.go
git commit -m "feat(M4): 增加应用退休与恢复命令"
```

---

### Task 5: 在 Application 唯一生命周期中串行执行控制请求

**Files:**
- Modify: `application/application.go`
- Modify: `application/retirement.go`
- Modify: `application/retirement_test.go`

**Interfaces:**
- Consumes: `command.StartRequest.Controls <-chan command.ControlRequest`、`ControlActionRetire/Resume`。
- Produces: `Application.handleControlRequest` 和无并发 Retire/Resume/Stop 的 Running 生命周期循环。

- [ ] **Step 1: 写 fake ControlRequest 和双向事件测试**

在 `application/retirement_test.go` 增加：

```go
type applicationControlRequest struct {
    action command.ControlAction
    ctx    context.Context
    result chan error
}

func (request *applicationControlRequest) Action() command.ControlAction { return request.action }
func (request *applicationControlRequest) Context() context.Context     { return request.ctx }
func (request *applicationControlRequest) Complete(err error)           { request.result <- err }
```

启动真实 Application，把 buffered controls channel 放入 `StartRequest.Controls`，发送 Retire 和
Resume，等待每次 result，断言已有 `changes` 精确为所有 Node/Service 的逆序 retired、正序
running；事件 `Previous/Current/ChangedAt` 两个方向均正确。

- [ ] **Step 2: 写 Stop 取消正在处理控制请求测试**

使用会阻塞发现发布直到 Context 取消的 Provider：发出 Retire，等待 Provider 进入，再取消
run Context，断言 Retire result 为 canceled/stopping，Application 随后进入 Stopped，且控制
处理与 Node Stop 没有竞态或死锁。

- [ ] **Step 3: 运行测试确认 Application 尚未消费控制通道**

Run: `go test ./application -run 'TestApplicationControl|TestApplicationStopCancelsControl' -count=1`

Expected: FAIL；请求未完成并触发测试 deadline。

- [ ] **Step 4: 实现安全控制请求处理**

在 `application/retirement.go` 增加：

```go
func (app *Application) handleControlRequest(
    lifecycleCtx context.Context,
    request command.ControlRequest,
) (result error) {
    if request == nil || request.Context() == nil {
        return errs.ErrInvalidArgument
    }
    defer func() {
        if value := recover(); value != nil {
            result = errs.Wrap(
                errs.CodeInternal,
                fmt.Errorf("Application 控制请求 panic: %v\n%s", value, debug.Stack()),
            )
        }
    }()
    controlCtx, cancel := context.WithCancel(request.Context())
    stopCancel := context.AfterFunc(lifecycleCtx, cancel)
    defer func() {
        stopCancel()
        cancel()
    }()
    switch request.Action() {
    case command.ControlActionRetire:
        return app.Retire(controlCtx)
    case command.ControlActionResume:
        return app.Resume(controlCtx)
    default:
        return errs.ErrInvalidArgument
    }
}
```

- [ ] **Step 5: 替换 Running 阶段单纯等待**

把 `<-lifecycleCtx.Done()` 改为唯一串行循环：

```go
controls := request.Controls
for lifecycleCtx.Err() == nil {
    select {
    case <-lifecycleCtx.Done():
    case control, open := <-controls:
        if !open {
            controls = nil
            continue
        }
        if lifecycleCtx.Err() != nil {
            control.Complete(errs.ErrServiceStopping)
            continue
        }
        control.Complete(app.handleControlRequest(lifecycleCtx, control))
    }
}
```

邮箱关闭后的 nil channel 不能 busy loop；Stop 取消通过 `context.AfterFunc` 传给正在执行的
Retire/Resume，完成返回后生命周期主循环才进入现有 Stop。

- [ ] **Step 6: 运行 Application 与退休测试**

Run: `gofmt -w application/application.go application/retirement.go application/retirement_test.go`

Run: `go test ./application ./node ./service -run 'Retire|Resume|Control|StateChanged' -count=1`

Run: `go test -race ./application ./node ./service -run 'Retire|Resume|Control|StateChanged' -count=1`

Expected: PASS。

- [ ] **Step 7: 提交生命周期接入**

```bash
git add application/application.go application/retirement.go application/retirement_test.go
git commit -m "feat(M21): 串行处理应用退休控制"
```

---

### Task 6: 完成真实跨进程命令验证

**Files:**
- Modify: `tests/helpers/commandprocess/main.go`
- Modify: `tests/integration/command/command_test.go`

**Interfaces:**
- Consumes: 正式 `retire/resume` 命令、`StartRequest.Controls` 和 `ControlRequest.Complete`。
- Produces: 三平台可运行的真实进程往返、timeout、并发串行和异常退出回归测试。

- [ ] **Step 1: 写真实 Retire/Resume 往返失败测试**

在集成测试新增 `ORIGIN_COMMAND_TEST_CONTROL_FILE`，启动 target 后执行：

```go
code, _, stderr := runHelper(
    t, nil,
    "retire", "--app-name", "runtime-control",
    "--pid-dir", pidDir, "--timeout", "3s",
)
if code != 0 {
    t.Fatalf("retire exit = %d: %s", code, stderr)
}
code, _, stderr = runHelper(
    t, nil,
    "resume", "--app-name", "runtime-control",
    "--pid-dir", pidDir, "--timeout", "3s",
)
if code != 0 {
    t.Fatalf("resume exit = %d: %s", code, stderr)
}
```

读取 control file，断言逐行为 `retired\nrunning\n`，然后正常 stop 并等待 target 退出。

- [ ] **Step 2: 写 timeout、并发和目标缺失测试**

增加测试：

```go
func TestRetireTimeoutLeavesTargetRunning(t *testing.T)
func TestConcurrentRetireResumeAreSerializedAcrossProcesses(t *testing.T)
func TestRetireMissingTargetFails(t *testing.T)
func TestTargetCrashReleasesControlLockAndLeavesRestartableState(t *testing.T)
```

timeout 模式让 helper 收到请求后等待 Context；断言命令退出码为 `4`、target 仍运行、随后 stop
可以清理。目标缺失断言退出码 `3` 且不创建 pid-dir。

- [ ] **Step 3: 运行测试确认 helper 尚未消费请求**

Run: `go test ./tests/integration/command -run 'Test(Retire|Concurrent)' -count=1 -v`

Expected: FAIL；目标未完成请求或正式命令尚不能跨进程返回。

- [ ] **Step 4: 让 helper 串行消费 Controls**

`runStart` 在写 ready 文件后使用：

```go
for {
    select {
    case <-ctx.Done():
        return nil
    case request, open := <-startRequest.Controls:
        if !open {
            return nil
        }
        switch request.Action() {
        case command.ControlActionRetire:
            request.Complete(appendControlState("retired"))
        case command.ControlActionResume:
            request.Complete(appendControlState("running"))
        default:
            request.Complete(fmt.Errorf("unknown control action %d", request.Action()))
        }
    }
}
```

`appendControlState` 使用环境变量指定文件并以 `O_CREATE|O_APPEND|O_WRONLY`、`0600` 写入一行；
delay 模式使用 `request.Context()` 可取消 Timer，不能用不可取消 sleep。

- [ ] **Step 5: 运行真实跨进程和竞态测试**

Run: `gofmt -w tests/helpers/commandprocess/main.go tests/integration/command/command_test.go`

Run: `go test ./tests/integration/command -count=1 -v`

Run: `go test -race ./tests/integration/command -count=1 -v`

Expected: PASS。

- [ ] **Step 6: 提交跨进程验证**

```bash
git add tests/helpers/commandprocess/main.go tests/integration/command/command_test.go
git commit -m "test(M4): 验证跨进程退休与恢复"
```

---

### Task 7: 更新状态事件示例和第 09 章 examples

**Files:**
- Modify: `examples/09-retire-and-resume/README.md`
- Modify: `examples/09-retire-and-resume/01-service-retire-resume/main.go`
- Modify: `examples/09-retire-and-resume/01-service-retire-resume/README.md`
- Modify: `examples/09-retire-and-resume/01-service-retire-resume/run.bat`
- Modify: `examples/09-retire-and-resume/01-service-retire-resume/run.sh`
- Create: `examples/09-retire-and-resume/01-service-retire-resume/retire.bat`
- Create: `examples/09-retire-and-resume/01-service-retire-resume/retire.sh`
- Create: `examples/09-retire-and-resume/01-service-retire-resume/resume.bat`
- Create: `examples/09-retire-and-resume/01-service-retire-resume/resume.sh`
- Create: `examples/09-retire-and-resume/01-service-retire-resume/stop.bat`
- Create: `examples/09-retire-and-resume/01-service-retire-resume/stop.sh`
- Modify: `examples/09-retire-and-resume/02-node-and-application/README.md`
- Modify: `examples/09-retire-and-resume/03-include-retired/README.md`

**Interfaces:**
- Consumes: 正式命令、`SubscribeEvent`、`ServiceStateChangedEventID` 和 `ServiceStateChanged`。
- Produces: 可复制的“start → retire → resume → stop”双终端示例及状态事件日志。

- [ ] **Step 1: 将示例 Service 改为只监听状态变化**

删除 `OnStart` 中的 Timer 自 Retire/Resume，改为：

```go
func (target *MaintenanceService) OnInit() error {
    return target.SubscribeEvent(
        service.ServiceStateChangedEventID,
        func(_ context.Context, raw service.Event) error {
            changed := raw.(service.ServiceStateChanged)
            target.Logger().Info(
                "service state changed: " +
                    changed.Previous.String() + " -> " + changed.Current.String(),
            )
            return nil
        },
    )
}
```

示例不自行触发状态变化，确保日志只能由外部 Application 命令产生。

- [ ] **Step 2: 更新启动和控制脚本**

`run.sh/run.bat` 只执行正常 start，显式使用：

```text
--app-name service-retire
--pid-dir ./examples/09-retire-and-resume/01-service-retire-resume/run
```

三个控制脚本分别执行正式 `retire`、`resume`、`stop`，同样传入 AppName、PID 目录和
`--timeout 30s`。Shell 脚本使用 `set -eu`，bat 使用 `@echo off`、`setlocal` 和固定仓库根目录。

- [ ] **Step 3: 重写 README 操作顺序和边界**

README 明确两个终端：终端 A 运行 `run` 并保持；终端 B 依次执行 retire、resume、stop。
写明事件只在真实变化时触发、幂等调用不重复触发、timeout 不回滚、Retire 不调用 OnStop。
02 示例保留进程内 Node/Application API 编排并链接 CLI 示例；03 保留 IncludeRetired，并说明
目标状态可由 CLI 触发。

- [ ] **Step 4: 构建并运行示例静态检查**

Run: `gofmt -w examples/09-retire-and-resume/01-service-retire-resume/main.go`

Run: `go build ./examples/09-retire-and-resume/...`

Run: `rg -n -- '--retired|InitialRetired' examples/09-retire-and-resume`

Expected: build PASS；rg 无输出。

- [ ] **Step 5: 提交示例**

```bash
git add examples/09-retire-and-resume
git commit -m "docs(M21): 演示命令退休和状态事件"
```

---

### Task 8: 同步主设计文档、教程和 API 索引

**Files:**
- Modify: `docs/baseline/v3.0/design/details/2026-07-25-程序命令与进程控制设计.md`
- Modify: `docs/baseline/v3.0/design/milestones/M4-程序命令与进程控制库设计.md`
- Modify: `docs/baseline/v3.0/design/details/2026-07-24-服务退休设计.md`
- Modify: `docs/baseline/v3.0/design/milestones/M21-业务运行时扩展收口设计.md`
- Modify: `docs/baseline/v3.0/design/设计文档索引.md`
- Modify: `docs/baseline/v3.0/guides/01.first-application.md`
- Modify: `docs/baseline/v3.0/guides/09.retire-and-resume.md`
- Modify: `docs/baseline/v3.0/guides/12.troubleshooting.md`
- Modify: `docs/baseline/v3.0/guides/reference/api-index.md`
- Modify: `docs/baseline/v3.0/guides/README.md`
- Modify: `README.md`

**Interfaces:**
- Consumes: 已实现的正式命令、timeout、错误和状态事件语义。
- Produces: 与代码一致且无旧外观残留的主设计、教程、索引和故障排查说明。

- [ ] **Step 1: 更新程序命令与进程控制设计**

在详细设计和 M4 设计中完成以下直接替换：

- 内置命令改为 `start/retire/resume/stop/help/version`；
- 删除所有“保留 v2 首参数兼容入口”的结论；
- start Usage 删除 `--retired`；
- 原“退休命令以后设计”章节改为固定控制文件、单请求串行、强类型 ControlRequest、总体
  timeout、目标错误响应和三平台统一语义；
- `ExitStopTimeout` 改为 `ExitControlTimeout`；
- 资源所有权加入目标控制 goroutine、单槽通道和固定文件清理；
- 测试/验收加入 retire/resume 真实跨进程路径。

- [ ] **Step 2: 更新服务退休与 M21 设计**

删除初始 Retired 入口，明确所有 Service 启动进入 Running；增加 Application CLI 数据流、
Node/Service 顺序、`ServiceStateChanged` 注册方式、真实变化/幂等边界、异步事件失败和发现发布
失败不回滚语义。设计文档必须区分本地 Service 状态事件与远端 discovery
`OnStateChanged`。

- [ ] **Step 3: 重写第 09 章教程的主操作路径**

第 09 章按以下顺序组织：

1. 启动目标 Application；
2. 从第二终端执行完整 `retire`；
3. 在 Service `OnInit` 注册并解释 `ServiceStateChanged`；
4. 执行 `resume`；
5. 解释 timeout、幂等、best-effort、部分提交与 Stop 区别；
6. 继续保留单 Service/Node 的 Go API 和 IncludeRetired 边界。

所有命令都使用：

```text
game-server retire --app-name game --pid-dir ./run --timeout 30s
game-server resume --app-name game --pid-dir ./run --timeout 30s
```

- [ ] **Step 4: 修正受影响索引和故障排查**

API 索引把 command 内置项改为六个正式命令；第 01 章删除初始 Retired 启动阶段；第 12 章
增加“控制命令 timeout 但进程仍运行/状态可能部分提交”的排查项；README 和教程索引使用
“运行期 Application Retire/Resume”描述。

- [ ] **Step 5: 扫描文档冲突并检查 Markdown**

Run: `rg -n --glob '*.md' --glob '*.go' --glob '*.sh' --glob '*.bat' -- 'start --retired|InitialRetired|保留 v2|ExitStopTimeout|以后.*退休命令|不实现退休命令' .`

Expected: 除本功能设计/实施计划中描述“删除这些旧外观”的历史说明外无输出；任何主设计、
教程、代码和示例命中都必须修正。

Run: `git diff --check`

Expected: PASS。

- [ ] **Step 6: 提交文档同步**

```bash
git add \
  docs/baseline/v3.0/design/details/2026-07-25-程序命令与进程控制设计.md \
  docs/baseline/v3.0/design/milestones/M4-程序命令与进程控制库设计.md \
  docs/baseline/v3.0/design/details/2026-07-24-服务退休设计.md \
  docs/baseline/v3.0/design/milestones/M21-业务运行时扩展收口设计.md \
  docs/baseline/v3.0/design/设计文档索引.md \
  docs/baseline/v3.0/guides/01.first-application.md \
  docs/baseline/v3.0/guides/09.retire-and-resume.md \
  docs/baseline/v3.0/guides/12.troubleshooting.md \
  docs/baseline/v3.0/guides/reference/api-index.md \
  docs/baseline/v3.0/guides/README.md \
  README.md
git commit -m "docs(M4/M21): 统一运行期退休命令设计"
```

---

### Task 9: 全量质量门禁与覆盖率复核

**Files:**
- No planned modifications: 本任务只验证 Tasks 1–8 的最终提交；若验证失败，返回产生缺陷的原任务修复并重跑其局部测试。

**Interfaces:**
- Consumes: Tasks 1–8 的完整实现和文档。
- Produces: 可重复的格式、单元、集成、竞态、静态检查、覆盖率和跨平台构建证据。

- [ ] **Step 1: 格式和遗留符号扫描**

PowerShell：

```powershell
$goFiles = rg --files command application node service tests/helpers/commandprocess tests/integration/command examples/09-retire-and-resume -g '*.go'
$unformatted = gofmt -l $goFiles
if ($unformatted) { $unformatted; exit 1 }
```

Expected: 无输出。

Run: `rg -n --glob '*.go' --glob '*.md' --glob '*.sh' --glob '*.bat' -- 'start --retired|InitialRetired|initialRetired|normalizeCommandName|ExitStopTimeout' .`

Expected: 只有本 spec/plan 对删除项的说明；生产代码、主设计、教程和示例无命中。

- [ ] **Step 2: 运行分层测试**

Run: `go test ./command ./application ./node ./service -count=1`

Run: `go test ./tests/integration/command -count=1 -v`

Run: `go test ./... -count=1`

Expected: 全部 PASS。

- [ ] **Step 3: 运行竞态与静态检查**

Run: `go test -race ./command ./application ./node ./service ./tests/integration/command -count=1`

Run: `go test -race ./... -count=1`

Run: `go vet ./...`

Expected: 全部 PASS，无 race 和 vet 诊断。

- [ ] **Step 4: 生成覆盖率并检查新增低覆盖路径**

Run: `go test ./command ./application ./node ./service -coverprofile=retire-command.cover -count=1`

Run: `go tool cover -func=retire-command.cover`

Expected: 新增 `control.go`、`control_mailbox.go`、`retirement.go`、Application 控制分支的正常、
非法输入、timeout、取消和清理路径均被执行；删除临时 `retire-command.cover` 前记录未覆盖但无法
稳定触发的平台系统故障分支。

Run: `Remove-Item -LiteralPath 'retire-command.cover'`

Expected: 临时覆盖率文件已删除。

- [ ] **Step 5: 三平台构建**

PowerShell：

```powershell
$env:GOOS='windows'; $env:GOARCH='amd64'; go build ./...
$env:GOOS='linux';   $env:GOARCH='amd64'; go build ./...
$env:GOOS='darwin';  $env:GOARCH='amd64'; go build ./...
Remove-Item Env:GOOS
Remove-Item Env:GOARCH
```

Expected: 三次构建均成功；不得因为控制协议增加平台条件编译缺口。

- [ ] **Step 6: 检查变更范围和最终提交**

Run: `git diff --check`

Run: `git status --short`

确认隔离 worktree 只包含本计划列出的退休命令、测试、设计、教程和示例。质量门禁若失败，
返回对应 Task 修复、执行该 Task 的局部测试并使用该 Task 的文件清单提交；之后从本任务 Step 1
重新执行全部门禁。

Expected: `git status --short` 无未提交内容，功能提交边界清晰，所有验证证据来自最终工作树。
