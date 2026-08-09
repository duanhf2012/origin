# Admin 管理 HTTP、Diagnostics 与 pprof 验收报告

> 状态：已验收
> 基线：v3.0
> 目标版本：v3.1.0
> 验收日期：2026-08-09
> 范围：`ab364ac` 至 `9faffb5`，以及 Summary v2 优化 `5c4af48`

## 结论

实现、六组示例、第 10 章、迁移说明和线上监控摘要模型均已完成并经过独立复审。Admin 是唯一的管理
HTTP 入口；`--diagnostics` 与独立 Diagnostics HTTP Listener 已删除；pprof 保持独立且可运行期启停。

默认 `GET /admin/v1/diagnostics` 返回低基数 Summary schema v2，适合秒级采集；
`?detail=full` 仍返回兼容的 Full Snapshot schema v2，供按需排障。Summary 去除了 Listener 地址、
逐 Transport RPC 和逐 Service DTO，增加了堆目标、内存上限是否配置、RPC 总量及调度/Timer/Event
结果计数；Full 保留详细地址、Transport 和 Service 信息。

## 任务完整性审计

| 任务 | 结果 | 主要提交与复审 |
| --- | --- | --- |
| 1 Admin 值模型 | 完成 | `ab364ac`、`76e7ab2`；独立复审通过 |
| 2 Service 串行调用 | 完成 | `37a6c2c`、`4cc21a2`；独立复审通过 |
| 3 冷注册与路由冻结 | 完成 | `779ddc6`、`4e16845`；独立复审通过 |
| 4 Admin HTTP 边界 | 完成 | `c0979d8`、`e27e70f`、`f05be6c`；两轮复审修复后通过 |
| 5 控制与自定义路由 | 完成 | `1d81f4c`、`2145c58`；独立复审通过 |
| 6 Summary 与目录一致性 | 完成 | `e2bf2c0`、`3fdef3b`、`738a009`；两轮复审修复后通过 |
| 7 `--admin` 生命周期迁移 | 完成 | `de945fc`、`68b63d8`、`ee2d53f`；两轮复审修复后通过 |
| 8 Chapter 10 示例 | 完成 | `dc720fa`；独立复审通过 |
| 11 线上 Diagnostics Summary v2 | 完成 | `5c4af48`；独立复审通过 |
| 9 教程、索引与迁移 | 完成 | `4c5748a`、`c51cdf1`、`9faffb5`；两轮文档复审后通过 |
| 10 本验收 | 完成 | 本报告；见以下可复验记录 |

中途发现的 Critical/Important 问题均已按 RED→GREEN 修复；没有遗留开放的
Critical 或 Important 项。

## 环境

- Go：`go1.26.5 windows/amd64`
- OS：Windows 10.0.26200.0
- CPU：AMD Ryzen 7 7840HS with Radeon 780M Graphics

## 门禁记录

| 检查 | 结果 |
| --- | --- |
| `go test ./... -count=1` | 通过；最终复跑约 44 秒 |
| `go vet ./...` | 通过 |
| `go build ./...` | 通过 |
| `CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build ./...` | 通过 |
| `CGO_ENABLED=0 GOOS=darwin GOARCH=arm64 go build ./...` | 通过 |
| 核心包串行 Race（Admin/Application/command/Diagnostics/Node/RPC/Service 等） | 通过 |
| 内部发现、网络、代码生成及全部集成包串行 Race | 通过 |
| `go test -race -p 1 ./examples/...` | 通过 |
| `go test ./admin ./application -run 'Admin|InvokeService' -count=100 -timeout=10m` | 通过；Application 约 17.6 秒 |
| Chapter 10 示例 `go test`、`go build`、Race、Vet | 通过 |
| Markdown 链接、旧名称例外、敏感词扫描、`git diff --check` | 通过；命中仅安全说明、测试虚拟值与历史/迁移材料 |

首次并行全仓 Race 曾在 `command` 的控制邮箱子进程测试中遇到 Windows 临时响应文件被短暂占用。
随后 `go test -race ./command -run '^TestControlMailboxPreservesBoundedOriginError$' -count=30 -p 1`
和 `go test -race ./command -count=3 -p 1` 均通过，并以串行分组 Race 覆盖全部有测试包；判定为
Windows 文件锁环境抖动，不是可复现的功能回归。

## 覆盖率

命令：`go test ./admin ./application ./node ./diagnostics -coverprofile=... -count=1`。

| 包 | 语句覆盖率 |
| --- | ---: |
| `admin` | 96.4% |
| `application` | 83.9% |
| `node` | 76.5% |
| `diagnostics` | 100.0% |

Admin 注册、Guard、HTTP 方法/Body/响应边界、限流、取消、错误映射、Service 串行调用、Summary
聚合和 Runtime `KindBad`/无限内存上限均有专门测试。低覆盖的 `emptyAdminControlHandler` 仅为
注册元数据占位，不经公开路由执行；pprof 的标准 profile 输出分支由标准库行为主导。

## Diagnostics 基准

命令：`go test ./application -run '^$' -bench 'BenchmarkDiagnostics(Summary|Full)' -benchmem -benchtime=100ms -count=5`。
以下是 64 Node × 64 Service 的五次中位数；fixture 构造不计入计时。

| 路径 | 中位 ns/op | B/op | allocs/op | response-bytes |
| --- | ---: | ---: | ---: | ---: |
| Summary 采集 | 179,503 | 33,488 | 3 | 77,205 |
| Full 采集 | 695,870 | 1,434,321 | 67 | 3,299,397 |
| Summary 采集 + JSON | 694,008 | 205,806 | 207 | 77,207 |
| Full 采集 + JSON | 8,211,919 | 9,391,884 | 28,769 | 3,298,305 |

Summary 的 Service 数从 1 到 64 时仍维持 3 次聚合输出分配，证明不建立逐 Service 输出 DTO；
Full 的大小与编码分配则按详细 Service 数据增长。结果只用于容量估算，不构成跨机器性能承诺。

## 安全与运行边界

- 无 Guard 时 Admin 仅允许环回绑定；非环回必须显式 Guard，并由部署层提供 TLS/网络访问控制。
- Admin 请求有私有 Mux、64 个活动请求上限、有限 Body/Response、严格规范路径、脱敏审计和 panic
  边界；Endpoint Deadline 为协作式 Context 取消，忽略 Context 的 Handler 会持续占用其额度至返回。
- Admin 空闲不周期采样，但保留 Listener/HTTP Server；查询读取 Runtime 并聚合 Node/Service/RPC/
  Timer/Event，不是只查询内存。
- OS RSS、容器 working set/limit、进程 CPU、宿主机负载和网络吞吐应由外部监控采集；pprof 不是
  常规指标接口，应短时、受保护地使用。

## 已知非阻塞项

- Task 2 的测试替身未完整模拟 `context.AfterFunc` 回调已开始时 `stop` 的返回值；生产代码不依赖
  该返回值。
- Task 4 的内部响应缓冲构造器接受负上限；线上边界始终传入正的框架常量。
- Task 5 的 `emptyAdminControlHandler` 是元数据占位，若未来包内代码直接调用会得到无操作 `204`。
- Task 6 的 64 Node/0 Service benchmark 使用 nil Node 公开诊断快路径；生产配置不允许该 Node。
  聚合 `uint64` 计数理论上会回绕，当前无饱和契约。
- Task 8 的 Application 控制示例单元测试直接调用 Endpoint Handler，未另起 HTTP Listener；实际
  路径已由 Application 路由测试覆盖。

所有上述项均已记录并被独立复审认定为非阻塞，未降低 Admin、Diagnostics、pprof 或示例的验收结论。
