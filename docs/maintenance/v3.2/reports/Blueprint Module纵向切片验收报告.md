# Blueprint Module 纵向切片验收报告

> 日期：2026-08-12
> 分支：`v3`
> Windows/Ubuntu Go：`go1.26.5`

## 1. 结论

Blueprint Module 的公共外观、Service 调度、Instance 所有权、同步/挂起执行、异步恢复、完成回调、
停止收口、热加载快照、诊断、教程和 Example 已完成。Windows 与 Ubuntu 的包级和全仓门禁均通过，
未发现已知竞态、活动 Execution 混用新旧编译图、Instance 泄漏或隐藏 goroutine。

## 2. 重点验证

| 风险 | 验证结果 |
| --- | --- |
| 首次节点是否在 Service 工作协程 | `SubmitInitial` 内联，集成测试通过 |
| Yield 等待是否占住 Service | `Run` 通过 Await 释放执行权，等待期间其他任务可推进 |
| 外部 Resume 后是否回到 Service | 从测试 goroutine Resume，后续节点由 Service FIFO 执行 |
| 完成回调是否安全 | `OnComplete` 非内联、严格一次，并在新的 Service task 执行 |
| 队列过载 | Resume 返回 QueueFull 且句柄可重试；OnComplete 拒绝不取消 Execution |
| 停止与迟到恢复 | 引擎关闭使挂起 Execution 以 `ErrBlueprintClosed` 终态收口，迟到 Resume 明确失败 |
| Instance 泄漏 | Close 幂等；Service 停止回收未主动关闭的 Instance |
| 热加载失败 | 不发布半成品，旧图继续执行 |
| 热加载快照 | 挂起旧 Execution 继续旧边；同一 Instance 下一次 Start 使用新边 |
| 热加载阻塞 | 读取、解析、编译在 Await worker；Service 普通任务仍可执行 |

包语句覆盖率为 `83.2%`。重点行为通过集成和竞态测试直接覆盖；未为 nil 接收者、`noCopy` 标记等低风险
机械分支编写无业务价值测试来追求总数字。

## 3. Windows 验收

Windows 11、AMD Ryzen 7 7840HS：

```text
go test ./sysmodule/blueprintmodule -count=1                 PASS
go test -race ./sysmodule/blueprintmodule -count=1           PASS
go test ./... -count=1                                       PASS
go test -race ./... -count=1                                 PASS
go vet ./...                                                 PASS
go build ./...                                               PASS
git diff --check                                             PASS
```

Example 实跑依次输出 `HP=90`、`HP=80`、`graph_count=1`、`HP=70`，随后通过 Origin `stop` 命令正常
输出 Node 与 Application stopped。

Benchmark：

| 基准 | ns/op | B/op | allocs/op |
| --- | ---: | ---: | ---: |
| Instance Start，同步 | 1,739 | 1,216 | 16 |
| Instance Create/Close | 431.4 | 272 | 4 |

最终补充场景基准的 Windows 结果：Service 内同步 `Instance.Run` 为约 `6.13µs/op`，挂起/恢复为约
`11.23µs/op`，两份小型蓝图的全量 Reload 为约 `1.30ms/op`。这些数值包含 Service 调度和测试夹具
Channel 往返，只作为当前提交的回归基线，不代表大型生产图的 Reload 时间。

## 4. Ubuntu 验收

Ubuntu 内核 `7.0.0-28-generic`、AMD Ryzen 7 7840HS，在独立目录同步同一工作树：

```text
go test ./sysmodule/blueprintmodule -count=1                 PASS
go test -race ./sysmodule/blueprintmodule -count=1           PASS
go test ./... -count=1                                       PASS
go test -race ./... -count=1                                 PASS
go vet ./...                                                 PASS
go build ./...                                               PASS
```

Linux Example 同样输出 `HP=90`、`HP=80`、`graph_count=1`、`HP=70`，`stop` 后 Node 与 Application
正常关闭，stderr 为空。

| 基准 | ns/op | B/op | allocs/op |
| --- | ---: | ---: | ---: |
| Instance Start，同步 | 1,110 | 1,216 | 16 |
| Instance Create/Close | 266.8 | 272 | 4 |

## 5. 性能与剩余边界

- 当前数据不支持增加对象池；池化会增加清零、所有权和挂起 Execution 复用风险。
- 热加载的绝对耗时取决于蓝图文件数量和复杂度，生产仍需在真实图规模下建立发布预算和告警。
- `Run/Start/Reload` 来自所属 Service 工作协程是公开使用约定，按确认结论未增加强制 goroutine 身份检测。
- Trace 会复制端口数据，只适合短时诊断窗口；默认保持关闭。
