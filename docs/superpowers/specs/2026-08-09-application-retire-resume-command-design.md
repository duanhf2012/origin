# Application 运行期 Retire/Resume 命令设计

状态：已确认

目标版本：Origin v3 当前开发版本

兼容性：尚未对外发布，不保留旧命令、旧参数或 v2 兼容别名

## 1. 背景

当前 v3 已经提供 `Service.Retire/Resume`、`Node.Retire/Resume` 和
`Application.Retire/Resume`，也会在真实状态切换后向所属 Service 异步投递
`ServiceStateChanged`。缺口在本地进程控制层：命令行只能 `start`、`stop`，不能让另一个
命令进程控制已经运行的 Application 退休或恢复。

现有 `start --retired` 表示“以 Retired 作为首次发布状态启动”，不是控制已经运行的进程。
该外观与运维语义容易混淆，而且用户确认当前开发阶段不需要保留兼容，因此完整删除该参数、
`InitialRetired` 数据流及相关分支。

v2 的 `-retire nodeid=...` 通过单向平台信号通知进程，不能返回 Application 操作结果，且
Windows 不支持。v3 继承“由另一个命令进程控制运行中进程”的目标，不继承其命令外观和
平台限制。

## 2. 目标与非目标

### 2.1 目标

- 增加正式内置子命令 `retire` 和 `resume`。
- 命令按 `AppName` 找到唯一运行进程，对该 Application 的全部 Node 执行
  `Application.Retire/Resume`。
- 命令等待目标操作和发现状态发布完成，并返回真实成功或错误结果。
- Linux、macOS 和 Windows 使用相同公开语义。
- 实际状态变化继续触发各 Service 的 `ServiceStateChanged`，幂等调用不重复触发。
- 同步修正程序命令、进程控制和服务退休设计文档，以及第 09 章教程和对应示例。
- 删除不再需要的初始 Retired 和 v2 命令兼容代码，使代码只表达当前正式外观。

### 2.2 非目标

- 不增加按单个 Node 或 Service 的命令行控制。
- 不增加远程管理端口、HTTP 管理 API、交互式 Shell 或通用在线自定义命令。
- 不把 Retire 当作 Stop；退休不调用 `OnStop`，也不释放进程资源。
- 不为超时或部分失败回滚已经提交的本地状态。
- 不保留 `start --retired`、`--initial-retired`、`-retire`、`-resume` 等别名。

## 3. 正式命令外观

```text
game-server start --app-name game --config ./config [--pid-dir ./run] [--node game-1]
game-server retire --app-name game [--pid-dir ./run] [--timeout 30s]
game-server resume --app-name game [--pid-dir ./run] [--timeout 30s]
game-server stop --app-name game [--pid-dir ./run] [--timeout 30s]
```

规则：

- `retire` 和 `resume` 必须提供合法的 `--app-name`。
- `--pid-dir` 与 `start/stop` 使用同一解析规则，默认 `./run`，并立即固定为绝对路径。
- `--timeout` 默认 `30s`，必须是大于零的 Go Duration 字符串。
- `retire/resume` 的 timeout 覆盖定位目标、等待前一个控制请求、目标执行和读取结果的总时间。
- 目标未运行时 `retire/resume` 返回进程控制失败；只有 `stop` 保留“未运行即幂等成功”。
- 总帮助和子命令帮助只展示正式子命令，不接受带单横线的旧主命令。

## 4. 删除范围

本次直接删除下列能力，不保留弃用期和兼容分支：

1. `start --retired` 参数、帮助文本和解析测试；
2. `command.StartRequest.InitialRetired`；
3. Application 到 Node 的 `initialRetired` 传播；
4. `node.Options.InitialRetired`、Node 初始 Retired 字段和启动分支；
5. “首次发布直接 Retired”专用测试和示例描述；
6. `normalizeCommandName` 中 `-start`、`-stop`、`-help`、`-h` 等 v2 首参数别名。

删除后所有 Service 启动成功均进入 `Running`。进入 `Retired` 只允许通过运行期
`Service/Node/Application.Retire`，其中命令行只公开 Application 级控制。

## 5. 本地控制架构

### 5.1 边界

`command` 包负责：

- PID 锁和 AppName 定位；
- 跨进程请求/响应文件协议；
- timeout、请求串行化、结果解码和退出码；
- 把目标进程收到的动作作为强类型 `ControlRequest` 交给 Start Handler。

`application` 包负责：

- 在唯一生命周期循环中顺序处理 `Retire` 和 `Resume`；
- 调用现有 `Application.Retire/Resume`；
- 把聚合结果原样完成给 `ControlRequest`。

`command` 不导入 `application`，避免反转现有依赖方向。`application` 不解析控制文件，也不
感知平台进程细节。

### 5.2 为什么使用本地请求/响应文件

采用 PID 目录中的有界请求/响应文件，并由目标进程唯一低频控制协程处理：

- 比 Unix 信号完整：可以返回发现发布失败、超时和聚合错误；
- 比平台双实现简单：Windows、Linux、macOS 使用同一协议；
- 比新增 TCP/HTTP 管理端口更小：不增加监听地址、认证令牌、端口冲突和网络暴露面；
- 只位于进程控制冷路径，不进入 Service、RPC、Timer 或调度热路径。

现有 Unix `SIGINT/SIGTERM` 和 Windows stop 请求仍只负责 Stop。Retire/Resume 不复用只能
表达停止意图的空 `.stop` 文件。

### 5.3 文件与串行化

每个 AppName 在同一个已解析 PID 目录中使用固定控制文件：

```text
<app>.control.lock
<app>.control.request
<app>.control.processing
<app>.control.response
```

- `control.lock` 使用现有跨平台文件锁，只在命令进程之间串行化控制请求。
- 命令在持有控制锁时检查 PID 锁仍由目标持有，再原子提交一个有随机请求 ID 和绝对
  deadline 的 JSON 请求。
- 目标控制协程把 `.request` 原子改名为 `.processing` 后读取，确保一个请求只执行一次。
- 目标完成后原子写入带相同请求 ID 的 `.response`，再删除 `.processing`。
- 命令只接受请求 ID 匹配的响应，完成清理后释放控制锁。
- 新的 start 在取得 PID 锁后清理同 AppName 的陈旧控制文件；未取得 PID 锁的进程不得清理。
- 所有控制文件限制为普通文件、`0600` 权限和固定最大字节数，JSON 拒绝未知字段。

同一 Application 同时最多存在一个已提交控制请求。并发命令在控制锁处有界等待，不建立
无界请求目录或 goroutine。

### 5.4 强类型控制请求

`command` 增加最小公开类型：

- `ControlActionRetire`；
- `ControlActionResume`；
- `ControlRequest`：包含动作、请求 Context 和只能完成一次的结果入口；
- `StartRequest.Controls`：只读控制请求通道。

目标 Runner 在调用 Start Handler 前启动唯一控制协程，并把通道放入 `StartRequest`。
Application 启动完成后不再只等待生命周期 Context，而是在同一个主循环中选择：

1. 生命周期取消：退出循环并执行现有反序 Stop；
2. Retire 请求：同步调用 `Application.Retire(request.Context())`；
3. Resume 请求：同步调用 `Application.Resume(request.Context())`。

控制动作因此不会相互并发，也不会由额外 goroutine 与 Application Stop 并发修改 Node。
控制请求在启动期到达时可以在单槽通道中等待；Application 真正进入 Running 后再处理，若
deadline 已到则直接返回超时。停止已经开始后不再执行新动作，并向等待方返回停止错误。

## 6. 请求与结果数据流

`retire` 的正常路径如下，`resume` 对称：

1. 命令解析 `app-name`、`pid-dir` 和 timeout，建立总体 deadline；
2. 检查 `<app>.pid` 仍被运行进程持锁并读取严格 PID 记录；
3. 在剩余 deadline 内取得 `<app>.control.lock`；
4. 再次检查 PID 锁，防止等待控制锁期间目标已经退出；
5. 写入 Retire 请求并等待匹配响应；
6. 目标控制协程读取请求并投递 `ControlRequest`；
7. Application 生命周期循环调用 `Application.Retire`；
8. Application 按 Node 启动顺序的逆序执行 Node Retire，每个 Node 再按 Service 逆序执行；
9. 每个真实 `Running -> Retired` 转换投递 `ServiceStateChanged`，并等待发现发布确认；
10. Application 聚合全部 Node 错误并完成请求；
11. 目标写回结构化结果，命令解码、清理文件并返回稳定退出码。

## 7. timeout、错误和幂等

### 7.1 timeout

`--timeout 30s` 是控制命令的总体等待上限，不是强制停止时间：

- 命令超时后返回非零退出码，不发送 Kill，也不触发 Stop；
- 请求中的绝对 deadline 会成为目标 `Application.Retire/Resume` 的 Context deadline；
- 已经提交的 Service 状态不回滚，因此超时可能留下部分已退休或已恢复状态；
- 操作幂等，运维方可以检查状态后重复执行同一命令。

现有退出码 `ExitStopTimeout` 重命名为覆盖全部在线控制的 `ExitControlTimeout`，数值保持内部
稳定即可；当前尚未发布，不保留旧常量别名。

### 7.2 结果编码

响应只保存：请求 ID、是否成功、Origin 错误码和有界错误摘要。它不序列化 Go 错误树、堆栈、
配置、token 或业务数据。目标日志保留完整的本地诊断上下文，命令进程只打印一次响应摘要。

### 7.3 状态与事件

- `Running -> Retired` 产生一条 `ServiceStateChanged`；
- `Retired -> Running` 产生一条 `ServiceStateChanged`；
- 已 Retired 再 Retire、已 Running 再 Resume 均幂等成功且不产生事件；
- 事件由状态变化的 Service 在 `OnInit` 中通过 `SubscribeEvent` 注册，只通知该 Service；
- 事件是异步本地事件，携带 `Previous`、`Current` 和 `ChangedAt`；
- 事件入队失败或发现发布失败会进入控制命令聚合错误，但已提交状态不回滚；
- Application/Node 批量操作保持 best-effort，不因单个失败跳过剩余对象。

## 8. 文档与示例

实施时必须同步修改真实主设计，不只新增本设计说明：

- 程序命令与进程控制详细设计及 M4 里程碑设计：删除兼容别名和初始 Retired，加入
  `retire/resume` 协议、timeout、退出码和跨平台边界；
- 服务退休详细设计及 M21 里程碑设计：补充 Application 命令入口与状态事件关系；
- 对应实施计划/设计索引中引用上述结论的条目，避免保留相互冲突的旧描述；
- 第 09 章教程：以两个终端展示 start、retire、resume、stop，明确 timeout、幂等、部分提交和
  `ServiceStateChanged` 注册方法；
- API 索引、命令帮助和故障排查中所有受影响的命令清单；
- `examples/09-retire-and-resume`：删除 `--retired` 启动流程，改为正常启动后由另一个命令
  进程执行 Retire/Resume。

示例中的 Service 在 `OnInit` 注册：

```go
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
```

示例脚本和 README 必须给出可复制的命令，不用 Timer 在进程内部自行 Retire/Resume，否则不能
证明跨进程命令能力。启动脚本只启动服务；退休、恢复和停止分别由明确的控制脚本或第二终端
命令执行。

## 9. 测试与验证

### 9.1 command 单元测试

- `retire/resume` 帮助、参数、默认 timeout 和多余位置参数；
- 不再接受 `start --retired` 和 `-start/-stop/-help`；
- 目标未运行、PID 记录损坏、控制锁等待超时和目标中途退出；
- 请求/响应 JSON 上限、未知字段、非普通文件、请求 ID 不匹配和陈旧文件清理；
- 并发 retire/resume 严格串行，无请求覆盖、goroutine 泄漏或文件遗留；
- 目标成功、业务错误、panic 隔离和 timeout 的退出码。

### 9.2 application 与生命周期测试

- 控制请求只在 Running 状态执行，并由 Application 主循环串行处理；
- Retire 逆序、Resume 正序和全部 Node 覆盖；
- Stop 与控制请求竞争时不并发执行状态切换；
- timeout/发布失败不回滚已提交状态，后续幂等重试可收敛；
- 删除初始 Retired 传播和测试后，启动首次状态固定为 Running。

### 9.3 状态事件测试

- `Previous/Current/ChangedAt` 在 Retire 和 Resume 两个方向都正确；
- 幂等 Retire/Resume 不重复触发；
- Application 批量命令触发每个真实变化 Service 的监听器；
- 事件队列失败被返回且不回滚状态；
- `go test -race` 下监听器、控制命令和 Stop 无数据竞争。

### 9.4 集成与平台验证

- 构建真实示例二进制，启动后从第二进程依次执行 retire、resume、stop；
- 验证命令输出、Service 事件日志、发现快照和最终 PID 锁释放；
- Windows、Linux、macOS 覆盖同一请求/响应协议；
- 全量执行 `go test ./...`、`go test -race ./...`、静态检查和跨平台构建。

## 10. 性能与资源边界

控制目录轮询和 JSON 编解码只发生在低频进程控制冷路径，不进入业务热路径。每个运行中的
Application 只增加一个有明确取消和等待责任的控制 goroutine、一个单槽请求通道和固定数量的
小文件；请求和响应均有大小上限，同时最多一个请求，不建立无界队列或按请求常驻资源。

## 11. 验收标准

1. 正式命令只有 `start`、`retire`、`resume`、`stop`、`help`、`version` 和用户注册的离线命令。
2. 仓库中不存在 `start --retired`、`InitialRetired` 或 v2 首参数兼容分支。
3. `retire/resume` 可以跨进程控制指定 AppName 的全部 Node，并等待真实结果。
4. timeout 不杀进程、不回滚状态，重复命令能够幂等收敛。
5. 真实状态变化会被 Service 注册的 `ServiceStateChanged` 监听器观察到。
6. 设计文档、教程、API 索引、帮助文本和示例与代码保持同一命令语义。
7. 目标测试、竞态测试、全量测试和三平台构建全部通过。
