# 01：创建第一个应用

## 我想启动多个 Node

运行两个 Node、两个 Service：

```text
REM 启动包含两个 Node 的示例。
examples\01-first-application\01-application-node-service\run.bat
```

完整源码：[examples/01-first-application/01-application-node-service](../../../../examples/01-first-application/01-application-node-service)。

配置中每个 Node 只声明实际要运行的 Service：

```yaml
nodes:
  # gateway-1 只创建网关 Service 实例。
  - id: gateway-1
    services: [GatewayService]
  # game-1 只创建玩家 Service 实例。
  - id: game-1
    services: [PlayerService]
```

## 我想确认启动和停止顺序

运行：[examples/01-first-application/02-lifecycle-order](../../../../examples/01-first-application/02-lifecycle-order)。按 `Ctrl+C` 后，`SecondService` 会先停止，`FirstService` 最后停止。

## 我想在创建 Application 时覆盖运行选项

运行：[examples/01-first-application/03-application-options-and-command](../../../../examples/01-first-application/03-application-options-and-command)。其中 `run.bat`/`run.sh` 运行一个自定义离线命令；`run-start.bat`/`run-start.sh` 则按普通方式启动 Application。

绝大多数程序只需要：

```go
// 零值 Options 使用全部默认值；这是最常见的入口。
var app = application.New()
```

`New` 只能省略 Options 或传入**一份** `application.Options`。它不接收 YAML/JSON 业务配置；配置仍由 `start --config` 加载。需要调整框架级边界时，在程序入口集中设置：

```go
var app = application.New(application.Options{
    // 0 表示框架不额外设置 Deadline；正数限制整个 Node 启动阶段。
    StartTimeout: 30 * time.Second,
    // 0 同样表示不额外限制完整停止阶段。
    StopTimeout: 45 * time.Second,
    Timer: application.TimerOptions{
        // 每个 Node 内所有 Service 合计的活跃业务 Timer 上限。
        // 0 使用默认 3,000,000；负数无效。
        MaxTimersPerNode: 10_000,
        // Cron 的统一时区；nil 使用创建 Application 时的 time.Local。
        Location: time.UTC,
    },
})
```

可用字段与默认值如下。Options 创建后不应再修改；`New` 返回值没有 error，非法负数、传入多份 Options 等错误会在 `Start` 的统一启动路径中报告。

| 字段 | 默认值 | 用途 |
| --- | --- | --- |
| `StartTimeout` | `0`（不额外超时） | 限制全部选中 Node 的完整启动阶段。 |
| `StopTimeout` | `0`（不额外超时） | 限制框架发起的完整停止、资源关闭阶段。 |
| `Timer.MaxTimersPerNode` | `3,000,000` | 每个 Node 的全部 Service 共享的活跃业务 Timer 硬上限；不会预分配同等数量内存。 |
| `Timer.Location` | `time.Local` | 该 Application 中所有 Cron 表达式使用的时区。 |
| `LogHandlerFactory` | `nil`（内置 Zap） | 替换日志输出后端的高级扩展点。常规控制台、文件、滚动等需求应优先使用 YAML/JSON 日志配置。 |

只有项目确实需要接入自己的日志 Handler 时才设置 `LogHandlerFactory`。Factory 接收已经合并完成的 `log.Config`，并返回 `log.Handler`；Origin 仍负责异步队列、调用方定位、Flush 与 Close：

```go
var app = application.New(application.Options{
    LogHandlerFactory: func(cfg originlog.Config) (originlog.Handler, error) {
        // 这里替换为项目自己的 Handler 构造函数。
        // 不要自行启动第二套日志 Runtime。
        return newProjectLogHandler(cfg)
    },
})
```

`Timer` 的日常使用、Ticker 和 Cron 示例见 [04：Timer、Event 与执行](./04-timer-event-and-execution.md)；日志 YAML/JSON、文件输出和滚动见 [02：配置应用](./02-configuration.md)。

## 我想注册一个自定义命令

自定义命令适合一次性的离线任务，例如校验导入文件、生成预览、执行数据修复前检查。它不创建 Node、不取得 PID 运行锁，也不会连接发现服务或启动业务 Service；持续运行的业务仍应使用 `start`。

在 `app.Start()` 前注册即可：

```go
func init() {
    app.Setup(&ExampleService{})

    if err := app.RegisterCommand(command.Command{
        // 名称必须是小写 kebab-case，不能覆盖 start、stop、help、version。
        Name:    "print-options",
        Summary: "输出离线任务收到的参数",
        Usage:   "demo print-options [name]",
        Run: func(ctx command.Context, args []string) error {
            // 使用 ctx.Stdout/ctx.Stderr，便于测试、嵌入和命令行重定向。
            _, err := fmt.Fprintf(ctx.Stdout, "args=%v\\n", args)
            return err
        },
    }); err != nil {
        panic(err) // 注册错误属于程序装配错误，应在进程启动前立即暴露。
    }
}
```

运行后可从总帮助中看到它，并取得子命令说明：

```text
demo help
demo help print-options
demo print-options Alice
```

自定义命令得到的 `command.Context` 包含取消信号以及 `Stdin`、`Stdout`、`Stderr`。命令回调返回 error 或 panic 时退出码为 `1`；名称、用法或参数错误使用稳定的用法错误退出码。不要在自定义命令中直接复用正在运行 Application 的控制能力；首版的命令模型只面向当前进程的离线工作。

## Application 公开方法应在哪里调用

下表汇总业务项目实际会接触到的 `Application` 方法。除 `Start` 外，方法均可在持有具体 `*application.Application` 的程序入口、管理 Service 或受控后台任务中调用；普通业务 Service 通过 `s.Application()` 只能得到受限诊断外观，详见 [09：Diagnostics 与 pprof](./09-diagnostics-and-pprof.md)。

| 方法 | 合适的调用位置 | 教程 |
| --- | --- | --- |
| `Setup`、`RegisterCommand`、`RegisterDiscoveryProvider` | 程序装配阶段，首次命令执行前 | 本章；Provider 见 [07：服务发现](./07-discovery.md) |
| `Start` | `main`，每个 Application 仅一次 | [00：快速入口](./00-quickstart.md) |
| `Stop(ctx)` | 嵌入式宿主或明确的进程管理代码；正常 CLI 优先使用 `stop` 命令或 OS 信号 | 本节下文 |
| `State`、`Node(id)`、`Nodes`、`Logger` | 管理/观测代码；`Nodes` 返回独立 Slice 快照 | [09：Diagnostics 与 pprof](./09-diagnostics-and-pprof.md) |
| `Retire(ctx)`、`Resume(ctx)` | 管理 Service 或发布编排 | [08：Retire 与 Resume](./08-retire-and-resume.md) |
| `Diagnostics`、诊断 HTTP、pprof 方法 | 受控观测和按需诊断 | [09：Diagnostics 与 pprof](./09-diagnostics-and-pprof.md) |

`Stop(ctx)` 请求唯一的生命周期路径停止，并等待全部资源清理完成；重复调用共享同一结果。它适合 Application 被其他 Go 程序嵌入时的显式关闭：

```go
// 在外部管理协程中请求停止；Context 决定当前调用方最多等待多久。
stopCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
defer cancel()
if err := app.Stop(stopCtx); err != nil {
    reportStopError(err)
}
```

不要从普通 Service 的 RPC 或回调中随意调用 `Stop`；这会把局部业务失败升级为整个进程退出。若确有管理 Service 需要停止进程，应通过明确的管理入口发起，并在 Service Task 中用 `Await` 等待可能阻塞的 `Stop`，避免独占该 Service 的串行执行权。

## 我想让二进制携带版本、提交和构建时间

这不是新的“杂项”功能：它直接服务于 `version` 与 `help` 命令，因此归在 Application 入口。运行中的程序可使用 `buildinfo.Version()`、`buildinfo.Commit()` 和 `buildinfo.BuildTime()` 读取三项只读字符串；未在编译时注入时它们保持空值，框架不会伪造时间或版本。

可先运行：[examples/01-first-application/04-build-information](../../../../examples/01-first-application/04-build-information)。它以固定演示值编译并执行 `version`，方便先观察最终外观；实际项目再替换为后文的 CI/Git 值。

```go
// 例如在进程级启动日志、诊断适配器中读取构建身份。
target.Logger().Info(fmt.Sprintf(
    "build version=%q commit=%q time=%q",
    buildinfo.Version(),
    buildinfo.Commit(),
    buildinfo.BuildTime(),
))
```

正常用户通常不需要在业务代码中输出它们：编译后的 `program version` 会显示 `version`、
`commit`、`build_time` 与 Go 版本；`program help` 仅在 `BuildTime` 非空时显示构建时间。

### 以 PowerShell 编译 Windows 二进制

将下面的包路径替换为你的 `main` 包，例如 `./cmd/game`。三项值可来自 CI 的发布版本、
Git 提交和构建时间；`-X` 的左侧必须保持 Origin 的完整包路径及变量名不变。

```powershell
$buildTime = Get-Date -Format 'yyyy-MM-ddTHH:mm:ssK'
$version = 'v3.0.0-rc.1'
$commit = (git rev-parse --short HEAD)
$ldflags = @(
  "-X=github.com/duanhf2012/origin/v3/buildinfo.buildTime=$buildTime",
  "-X=github.com/duanhf2012/origin/v3/buildinfo.version=$version",
  "-X=github.com/duanhf2012/origin/v3/buildinfo.commit=$commit"
) -join ' '

go build -ldflags $ldflags -o ./bin/game.exe ./cmd/game
./bin/game.exe version
```

Linux/macOS Shell 的等价方式：

```bash
build_time=$(date '+%Y-%m-%dT%H:%M:%S%z')
version=v3.0.0-rc.1
commit=$(git rev-parse --short HEAD)
go build \
  -ldflags "-X=github.com/duanhf2012/origin/v3/buildinfo.buildTime=$build_time \
  -X=github.com/duanhf2012/origin/v3/buildinfo.version=$version \
  -X=github.com/duanhf2012/origin/v3/buildinfo.commit=$commit" \
  -o ./bin/game ./cmd/game
./bin/game version
```

本仓库的 [`scripts/buildwin.bat`](../../../../scripts/buildwin.bat) 与
[`scripts/buildlinux.bat`](../../../../scripts/buildlinux.bat) 已实现同一套注入规则：自动读取
本地时间、精确 Git Tag 和短 Commit，也允许通过 `ORIGIN_BUILD_TIME`、
`ORIGIN_BUILD_VERSION`、`ORIGIN_BUILD_COMMIT` 覆盖。它们是 Origin 源码仓库的构建脚本；业务
项目应在自己的 Makefile、CI 或构建脚本中复用上面的 `-ldflags` 规则，并将值固定在一次构建内。

## 深入一点：四个对象

```text
Application
  # Application 持有进程级资源与全部 Node。
  └── Node
        # Node 按配置拥有多个 Service。
        └── Service
              # Module 是 Service 内部的生命周期单元。
              └── Module
```

- `Application` 管理本进程中的全部 Node 和共享资源。
- `Node` 是配置、网络身份和 Service 容器。
- `Service` 是串行执行业务的基本单元。
- `Module` 是一个 Service 内部的生命周期组织单元，下一章后再实际使用。

Service 的 `OnInit`、`OnStart`、`OnStop` 分别适合读取配置/登记资源、开始对外工作、释放业务资源。不要在 `OnInit` 发起依赖其他 Service 的 RPC；应在 `OnStart` 或后续任务中进行。
