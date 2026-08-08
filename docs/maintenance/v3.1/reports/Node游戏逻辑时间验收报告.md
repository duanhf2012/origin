# Node 游戏逻辑时间验收报告

> 状态：通过
> 基线：v3.0
> 目标版本：v3.1.0
> 验收日期：2026-08-08

## 环境

- Go：`go1.26.5 windows/amd64`
- 本机 CPU：AMD Ryzen 7 7840HS with Radeon 780M Graphics
- 竞态检测：Go race detector，Windows/amd64
- 跨平台编译：`linux/amd64`，`CGO_ENABLED=0`，使用执行包装器只验证测试二进制编译，不在 Windows 上执行 Linux 二进制

## 功能验收

| 范围 | 结果 |
| --- | --- |
| Service/Module `GetNode()`，未绑定返回 `nil` | 通过 |
| `Now`、Set、Add、负数、零增量、超范围和溢出 | 通过 |
| OnInit、OnStart、Running 修改，Stopping/Stopped/Failed 拒绝 | 通过 |
| 同 Node 跨 Service 生效，不同 Node 严格隔离 | 通过 |
| After 向前触发一次、向后不提前 | 通过 |
| Ticker 合并历史、Cron 跳过历史并继续计算 | 通过 |
| Paused 不重排，DuePending/Ready/Running 不撤回不重复 | 通过 |
| 时间轮原地重排保留 ID，到期竞争换新 ID | 通过 |
| 基础设施 Deadline 不随游戏时间提前 | 通过 |
| 并发 Add/Now/Timer 创建与取消 | 通过，无 race |

## 验证命令

| 命令 | 结果 |
| --- | --- |
| `go test ./... -count=1` | 通过，包含全仓单元与集成测试 |
| `go test -race ./internal/timerwheel ./service ./node -count=1` | 通过 |
| `go vet ./...` | 通过 |
| `GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go test ./... -run '^$' -exec <compile-only-wrapper>` | 通过 |
| 变更 Markdown 本地链接校验 | 19 个变更文件通过 |
| `git diff --check` | 通过 |

## 覆盖率

| 包 | 语句覆盖率 |
| --- | ---: |
| `internal/timerwheel` | 94.0% |
| `service` | 72.7% |
| `node` | 75.6% |

覆盖率为包级总量，不是只对新文件的百分比。新能力的正常、错误、生命周期、到期竞争、跨 Node 隔离和并发路径均有真实行为断言。

## 性能

`BenchmarkNodeGameTimeNow` 每组运行约一秒，重排 Benchmark 使用 `-benchtime=1x -count=3`，因为其是一次性管理冷路径。

| Benchmark | 实测范围 | B/op | allocs/op |
| --- | ---: | ---: | ---: |
| `NodeGameTimeNow` | 10.61–10.88 ns/op | 0 | 0 |
| `GameTimeRebase/1` | 8.6–11.8 µs/op | 0 | 0 |
| `GameTimeRebase/1,000` | 141.1–263.4 µs/op | 8,192 | 1 |
| `GameTimeRebase/100,000` | 37.98–45.97 ms/op | 802,816 | 1 |

初始的“取消旧 Deadline + 创建新 ID”方案在 100,000 Timer 样本中为约 89.68–127.68ms、18.09–18.13MB 和 1,029–1,055 次分配。定位到 TimerEngine/Scheduler 两张 ID Map 批量换键后，改为保留 DeadlineID 的原地重排，最终只保留稳定排序 ID Slice 的一次分配。

这些数字是当前开发机的回归基线，不是跨硬件 SLO。时间修改仍是 `O(Scheduled Timer 数)` 的显式冷路径。

## 已知边界

- 进程重启不恢复逻辑时间偏移；
- 框架不自动向其他 Node 同步时间；
- 框架不提供管理端身份认证和审计存储，生产环境必须由业务管理层限权；
- 大批量重排并不同步等待业务回调执行，回调仍受 Service 串行 Ready 队列与容量约束。
