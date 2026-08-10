# Origin 系统级稳定性、容量与性能验收报告

> 状态：已完成
> 基线：v3.0
> 目标：v3.1.0
> 兼容性：不保留未发布版本之间的兼容；已经人工检查的当前使用者外观以代码为准
> 验收日期：2026-08-10

## 1. 结论

Task 6 的系统稳定性、容量、正式性能、安全依赖和双平台冻结门禁已经完成。在本报告覆盖的
环境、功能和故障模型内，没有已知生产缺陷、竞态、死锁、资源泄漏或未解释性能异常，允许
冻结生产代码并进入 Task 7 的测试、Example 和教程最终收口。

“通过”表示当前验证范围内没有已知缺陷，不表示数学意义上的绝对无 Bug。现有外部 NATS、
etcd 集群属于使用者资源，本次只用独立命名空间验证正常通信，不停止、重配或删除；断线、
恢复风暴和集群故障使用测试拥有的内嵌集群完成。

## 2. 环境与范围

| 项目 | 实际环境 |
| --- | --- |
| 主验收环境 | Ubuntu 26.04 LTS，Linux 7.0.0-28-generic，amd64 |
| Go | 1.26.5，`GOMAXPROCS=8` |
| CPU | AMD Ryzen 7 7840HS，8 个逻辑 CPU |
| 内存 | 7,634,333,696 bytes，验收结束 `MemAvailable` 约 6.04 GiB |
| 文件描述符 | `ulimit -n = 1024` |
| Docker | 29.6.2 |
| 真实依赖 | NATS 2.14.3 三节点、etcd 3.7.1 三节点 |
| 对照环境 | Windows/amd64，Go 1.26.5 |

验收继续遵守三个停止条件：没有 Profile 或同环境退化证据时不改热路径；不为单纯提高数字
引入对象池、缓存、无锁结构或测试注入点；Full Diagnostics 和最大 Payload 等高成本能力按
已确认用途验收，不把人工排障路径优化成常驻采集系统。

## 3. 稳定性与故障场景

### 3.1 服务自调用 RPC

服务自己调用自己的 RPC 作为独立必过场景，而不是被普通 RPC 回归隐含带过：

- 普通 goroutine 使用同步 `Call` 能正常返回；
- Service Task 内 `Await` 会释放执行槽并恢复 FIFO；
- Service Task 内 `Async` 回调恰好一次并回到 owner 串行槽；
- Service Task 内 `Notify` 能在当前 Task 返回后执行；
- Service Task 内直接同步 `Call` 不会隐式重入，必须由调用方 Deadline 有界结束。

Ubuntu Race 独立重复 100 次通过；安全依赖修正后又定向重复 3 次，并包含在最终全仓 Race 中。

### 3.2 并发、故障与资源

| 场景 | Ubuntu 结果 |
| --- | --- |
| Scheduler 10,000 次 Await 恢复、1,000 次取消风暴、50 次启停 | Race 通过，2.26 秒 |
| Application 生命周期 | Race 重复 20 次通过，2.43 秒 |
| NATS 重连、过载、慢消费者、目标 panic | 内嵌真实协议 Race 重复 3 次通过 |
| TCP/NATS RPC、Broadcast、Retire、过载、优雅停止 | 系统集成 Race 重复 3 次通过 |
| 外部 NATS 三节点 | Transport 与 RPC Race 各重复 3 次通过 |
| 外部 etcd 3.7.1 | Provider 兼容 Race 重复 3 次通过 |
| 日志、Admin、Summary/Full Diagnostics、pprof、指标和 Service 调度共存 | Windows 重复 20 次、Ubuntu Race 重复 20 次通过 |

可观测性共存测试每轮顺序执行 5,000 个真实 Service Task，同时持续读取两个 Diagnostics 和
pprof。Ubuntu Race 的 Task P99 为 `93.415～121.666 µs`，每轮完成 `132～165` 个 Admin 请求和
`11～13` 个 pprof 请求；停止后端口可立即重绑，goroutine 回落。该一秒硬上限只用于发现死锁
和数量级退化，不是业务 SLA。

24 分钟正式性能矩阵、重复 Race 和真实集群测试结束后，未发现 Task 进程、临时目录或
6060～6065 监听残留。六个既有 NATS/etcd 容器的 `error/warn/slow consumer/panic/fatal` 计数为
零；没有停止、重配或删除这些容器。

## 4. 正式 RPC 性能

正式 M22 矩阵在 Ubuntu 使用真实三节点 etcd/NATS、独立 TCP/NATS 目标进程执行。矩阵包含
Local/TCP/NATS、Await/Async、32 B/1 KiB/64 KiB、单并发和 32/64 并发共 24 个场景；每个场景
预热 5 秒、采集 15 秒、重复 3 轮，共 72 条结果。总耗时 1,441.50 秒，全部
`errors=0`、`timeouts=0`、`pending_end=0`。

下表为三轮中位数；吞吐仅按 RPC 业务 Payload 计算：

| Transport | 模式 | Payload/并发 | QPS | P50/P95/P99（µs） |
| --- | --- | --- | ---: | --- |
| Local | Async | 32 B / 1 | 162,745.7 | 4.471 / 11.461 / 23.476 |
| Local | Async | 32 B / 64 | 111,843.0 | 539.874 / 964.301 / 1,330.881 |
| Local | Async | 1 KiB / 32 | 117,163.9 | 234.039 / 561.851 / 855.248 |
| Local | Async | 64 KiB / 32 | 20,450.0 | 1,452.557 / 2,075.708 / 2,383.658 |
| Local | Await | 32 B / 1 | 177,861.1 | 4.406 / 9.887 / 22.281 |
| Local | Await | 32 B / 64 | 161,022.4 | 341.357 / 669.971 / 977.151 |
| Local | Await | 1 KiB / 32 | 138,970.8 | 202.576 / 504.481 / 714.155 |
| Local | Await | 64 KiB / 32 | 24,775.5 | 1,168.581 / 1,704.678 / 1,985.669 |
| TCP | Async | 32 B / 1 | 13,699.8 | 70.015 / 118.703 / 156.059 |
| TCP | Async | 32 B / 64 | 62,510.1 | 996.861 / 1,549.021 / 1,915.817 |
| TCP | Async | 1 KiB / 32 | 53,082.7 | 581.344 / 927.085 / 1,240.593 |
| TCP | Async | 64 KiB / 32 | 10,411.1 | 2,982.248 / 4,535.913 / 5,624.388 |
| TCP | Await | 32 B / 1 | 14,450.9 | 63.568 / 100.642 / 129.501 |
| TCP | Await | 32 B / 64 | 63,031.1 | 972.734 / 1,492.844 / 1,978.136 |
| TCP | Await | 1 KiB / 32 | 52,571.6 | 579.413 / 895.253 / 1,199.253 |
| TCP | Await | 64 KiB / 32 | 10,509.7 | 2,864.654 / 4,358.973 / 5,254.161 |
| NATS | Async | 32 B / 1 | 5,806.2 | 160.954 / 224.521 / 283.509 |
| NATS | Async | 32 B / 64 | 47,601.8 | 1,284.668 / 1,865.773 / 2,279.195 |
| NATS | Async | 1 KiB / 32 | 40,769.6 | 727.494 / 1,146.028 / 1,554.024 |
| NATS | Async | 64 KiB / 32 | 5,698.1 | 5,505.186 / 7,889.952 / 9,149.798 |
| NATS | Await | 32 B / 1 | 5,928.7 | 157.642 / 218.053 / 272.710 |
| NATS | Await | 32 B / 64 | 81,416.7 | 725.985 / 1,389.120 / 1,936.276 |
| NATS | Await | 1 KiB / 32 | 52,413.9 | 571.685 / 1,067.385 / 1,433.911 |
| NATS | Await | 64 KiB / 32 | 5,654.1 | 5,522.920 / 8,462.700 / 9,908.115 |

相对 v3.0 基线有 5/24 个 QPS 中位数超过 `-15%` 复核线，分别为 Local Async 32 B/64、
Local Async 1 KiB/32、Local Await 32 B/64、NATS Async 1 KiB/32、NATS Await 32 B/64；没有
场景超过 `-25%` 阻断线。Profile 和 Git 历史确认统一增加的 Async 约 6 次、Await 约 5 次分配
来自 v3.1 已确认的可选 Context、调用预算和严格一次完成语义，不是本轮偶发回归。

独立稳定 Benchmark 为：Local Await `5.738～5.876 µs/op，2101 B/op，31 allocs/op`；Local Call
`3.609～3.626 µs/op，1107 B/op，20 allocs/op`；补齐的 Local Async
`6.371～6.440 µs/op，2150～2151 B/op，35 allocs/op`。逐次 `context.AfterFunc` 是停止超时、
Scheduler 锁不可用和调用硬边界下仍能取消操作的必要所有权机制；改成统一扫描会增加锁和
无锁所有权复杂度，因此不做生产重构、池化或缓存化。

## 5. Diagnostics 容量

Ubuntu 64 Node × 64 Service 的三轮结果如下：

| 路径 | 中位耗时 | 响应 | B/op | allocs/op |
| --- | ---: | ---: | ---: | ---: |
| Summary | 142.324 µs | 约 77.2 KiB | 33,488 | 3 |
| Summary + JSON | 681.542 µs | 约 77.2 KiB | 203,386～206,816 | 207 |
| Full | 448.585 µs | 约 3.29 MB | 1,434,323 | 67 |
| Full + JSON | 8.161 ms | 约 3.29 MB | 8.35～10.01 MB | 28,764～28,769 |

Summary 适合按真实规模做秒级拉取；Full 继续只用于人工排障。没有数据支持给 Diagnostics
增加缓存、常驻采样、无锁化或并行聚合。

## 6. 安全、依赖与许可证

- `go mod verify` 在 Windows、Ubuntu 均通过；
- Go 官方 `govulncheck v1.6.0`、数据库更新时间 2026-07-27：可达 Symbol/Package 漏洞均为 0；
- 扫描发现 `github.com/klauspost/compress v1.18.6` 的 `GO-2026-5841`，虽当前不可达，仍按上游
  “只有安全修复、无其他变更”的补丁发布升级到 `v1.18.7`；修正后漏洞消失，NATS 定向、真实
  三节点、全仓 Test/Race 全部重跑通过；
- 剩余 `GO-2026-5932` 只影响不维护的 `golang.org/x/crypto/openpgp`。Origin 的实际编译依赖中
  `openpgp` 包数量为 0；当前只使用其他 `x/crypto` 包，因此记录为已解释的模块级不可达项，
  不引入替代 OpenPGP 库；
- `go-licenses v2.0.1 check ./...` 未发现禁用许可证；28 条可达库记录均完成分类：
  Apache-2.0 12 条、BSD-3-Clause 11 条、MIT 5 条、空许可证 0 条。11 条在线 License URL 因网络
  元数据请求超时未生成，但本地许可证类型已识别，不把 URL 超时误报为许可证缺失；
- 测试凭据、临时连接材料和敏感值未写入仓库或报告。

漏洞工具依据：[Go 官方 govulncheck](https://go.dev/doc/tutorial/govulncheck)、
[GO-2026-5841](https://pkg.go.dev/vuln/GO-2026-5841)、
[GO-2026-5932](https://pkg.go.dev/vuln/GO-2026-5932)。

## 7. 异常与处理结论

| 异常 | 定位与结论 |
| --- | --- |
| 第一次正式矩阵未显式扩大 `go test` 默认超时 | 在 5 条结果后终止精确测试进程并清理；该次不计结果，正式命令固定 35 分钟超时 |
| 一次正式矩阵从 Local 切入 TCP 时初始化失败 | 独立进程检查和后续完整 72 条结果未复现；新增外部 TCP/NATS/etcd 快速预检，避免长矩阵晚发现基础设施错误 |
| 可观测性测试最初请求 `detail=summary` 返回 400 | 当前代码规定 Summary 不带 Query；修正测试，不改变公开接口 |
| 可观测性 Worker 停止时读取 pprof 返回 `context canceled` | 属于主动取消的正常退出；测试只在未取消时报告读取错误，连续与 Race 复验通过 |
| 外部 etcd 首次参数缺少 URL scheme | clientv3 能探测版本，但 Origin 严格配置正确拒绝；改用公开格式完整 URL 后 3 次通过，不放宽解析 |
| Ubuntu 无法访问 Go 代理和 GitHub | 使用 Windows 已按 `go.sum` 校验的标准模块代理四件套建立临时 file-proxy；Ubuntu 再执行 `go mod verify`，测试后逐文件删除临时代理 |

以上异常均已定位，没有通过跳过、降低次数、放宽生产边界或掩盖失败关闭。

## 8. 最终门禁

| 门禁 | Windows | Ubuntu |
| --- | ---: | ---: |
| 全仓 `go test ./... -count=1` | 通过，51.25 秒 | 通过，46.93 秒 |
| `go vet ./...` | 通过，5.09 秒 | 通过，1.52 秒 |
| `origingen rpc --check ./...` | 通过，3.66 秒 | 通过，1.21 秒 |
| 全仓 `go test -race -p 1 ./...` | 通过，250.11 秒 | 通过，150.21 秒 |
| 格式与空白 | `gofmt`、`git diff --check` 通过 | 同步源码通过全部门禁 |

Task 6 完成门禁满足：生产代码冻结；Task 7 只能补充不改变生产行为的残余测试并收口 Example、
教程。若 Task 7 暴露生产问题，必须退回所属 Task 修复并重跑受影响及后续门禁。
