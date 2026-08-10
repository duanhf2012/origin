# Origin 发布前全面复审与优化实施计划

> 状态：已完成，Task 0～8 全部通过，发布候选已冻结
> 基线：v3.0
> 目标：v3.1.0 发布候选收口
> 兼容性：不保留未发布版本之间的源码、配置或线协议兼容；已经人工检查的当前使用者外观以代码为准，未经单独确认不得改变
> 编制日期：2026-08-09
> 确认日期：2026-08-09

设计依据：[Origin 发布前全面复审与优化设计](../design/Origin发布前全面复审与优化设计.md)

## 1. 目标与实施边界

本计划把已经确认的总设计转换为可逐项执行、可暂停、可回退和可验收的任务。实施目标是：

1. 建立完整功能范围、当前外观、代码、测试、性能和教程基线；
2. 先确认并处理必要的高影响设计和代码问题，再处理局部问题；
3. 对全部生产代码完成 Review 和覆盖审计，重点功能以 `100%` 可达语句与关键分支为目标；
4. 通过单元、集成、端到端、Race、Fuzz、故障、压力、长期运行和跨平台验证消除已知缺陷；
5. 在生产代码稳定后，从使用者角度收口简洁教程和完整注释 Example；
6. 最终形成有证据支持的发布候选和总验收报告。

本计划不授权新增未确认功能、不授权重做已经合理且稳定的设计，也不授权为了覆盖率、形式
统一或微小性能收益扩大生产抽象。

## 2. 全局执行约束

1. 一次只允许一个 Task 或一个子批次处于进行中；当前批次通过门禁后才能开始下一批；
2. 前半程按影响等级和依赖顺序处理 L0～L2，后半程按教程功能顺序处理 L3，最后横向补漏；
3. 每项优化先判断必要性，记录保持现状、最小方案、范围外、停止条件和验收标准；
4. 当前使用者外观默认冻结；文档与外观冲突时先以当前代码为准；
5. 发现相邻问题但不阻塞当前任务时只进入台账，不顺手扩大当前修改；
6. 重构前增加最小特征测试，缺陷修复前增加失败测试，生产修改后立即执行相关验证；
7. 每个生产代码提交保持可构建、可测试，不长期保留依赖后续提交才能恢复的破损状态；
8. 生成代码不得手工修改；修改生成器后重新生成并检查结果确定性；
9. 保存并尊重使用者已有工作树改动，只处理本计划当前批次明确列出的文件；
10. 待确认问题按设计层级和依赖集中成批，不逐个零散打断；
11. 测试失败、偶发失败、Race、泄漏和性能异常必须定位，不能通过跳过或放宽门禁掩盖；
12. 每个生产代码批次都必须在指定 Ubuntu 实机执行受影响包验证；Task 收口和最终门禁必须
    执行 Ubuntu 全仓验证，Windows 结果和 Linux 交叉构建不能替代；
13. 测试环境凭据、密码、临时密钥和连接材料不得写入仓库、计划或验收报告；
14. 每个批次完成后更新本计划的状态、结论、验证证据和剩余风险。

## 3. 单一追踪台账

实施期间只在本计划维护问题和批次摘要，不为普通问题创建大量零散文档。每个有效问题使用
以下字段记录；同一根因影响多个文件时只建立一个主问题并列出影响范围。

| 字段 | 内容 |
| --- | --- |
| ID | 稳定编号，例如 `DES-001`、`CODE-001`、`TEST-001`、`PERF-001`、`DOC-001` |
| 功能/模块 | 所属使用者功能和代码包 |
| 问题与证据 | 代码位置、文档冲突、失败测试、覆盖率、Benchmark 或 Profile |
| 类型 | 功能、设计、正确性、复杂度、兼容、测试、性能、文档或发布质量 |
| 必要性 | 必须修改、有证据时修改、原则上不修改 |
| 影响等级 | L0～L4 |
| 外观影响 | 无、保持不变、需要开发者单独确认 |
| 方案与范围 | 保持现状、最小方案、范围内、范围外和停止条件 |
| 验证 | 测试、Race、Fuzz、Benchmark、集成或人工验收 |
| 结论 | 待确认、实施、保持现状、延期、已验证关闭 |

覆盖审计另外按“包/文件/函数、风险等级、基线覆盖、未覆盖路径、补测方式、例外依据、最终
结果”汇总。机器生成的覆盖率和 Profile 文件放在临时目录，不提交仓库；本计划只保存结论、
执行命令和需要长期保留的例外依据。

## 4. 通用批次循环

Task 3～Task 7 的每个实现批次统一执行：

1. 阅读对应主设计、计划、当前代码、测试、教程和必要 Git 历史；
2. 明确当前功能、外观、不变量、范围内、范围外和停止条件；
3. 只做盘点和 Review，先形成问题及推荐结论；
4. 对需要改变的 L0/L1 结论集中取得开发者确认；
5. 为保留行为补最小特征测试，或为缺陷补失败测试；
6. 实施当前已经确认的最小修改；
7. 先在本地运行相关单元、Race、Fuzz、集成和 Benchmark，再在 Ubuntu 实机运行受影响包的
   普通测试及适用的 Race、Fuzz、重复或真实协议测试；
8. 生成覆盖率明细，检查未覆盖函数、语句和人工关键分支清单；
9. 更新对应设计、代码注释、测试说明和台账；
10. Task 内子批次执行本地全仓回归；Task 收口同时执行本地和 Ubuntu 全仓 Test、Vet、生成
    一致性及适用 Race，确认没有范围外改动后关闭。

如果第 6～10 步暴露新的 L0/L1 问题，停止当前批次并退回设计门禁；不得在局部实现中绕过。

## 5. 验证基线

Task 0 先确认本机 Go、CGO、NATS、etcd 和跨平台条件，再固定最终命令。至少包含：

```text
git status --short
git diff --check
go version
go env GOOS GOARCH CGO_ENABLED
go list ./...
go vet ./...
go test ./... -count=1
go test ./... -coverprofile=<临时目录>/origin-cover.out -count=1
go tool cover -func=<临时目录>/origin-cover.out
go test -race -p 1 ./... -count=1
```

在此基础上按风险执行：

- 相关并发生命周期包和失败复现测试使用 `-count=50` 或 `-count=100`；
- 配置、Codec、Wire、发现输入和其他适用入口执行定时 Fuzz；
- TCP、NATS、etcd、命令和 Admin HTTP 执行真实协议或独立进程集成测试；
- 热路径执行 `-benchmem`，保存 `ns/op`、`B/op`、`allocs/op` 和必要的延迟分位数；
- Windows 与指定 Ubuntu 的平台分支执行实际测试；macOS 无实机时执行交叉构建并记录替代
  证据；Linux 交叉构建只能作为补充，不能替代 Ubuntu 实测；
- 示例执行编译、受控运行、退出和资源清理；全部 Markdown 相对链接检查有效；
- 生成器在干净工作树或隔离副本中运行两次，结果一致且没有未提交漂移。

若全仓 Race 受环境或真实外部依赖限制，必须按无重叠包组覆盖全部具有测试的包，并记录分组、
原因和结果，不能简单省略。

## 6. Task 0：建立现状基线

**允许修改：** 本计划中的基线记录；必要时增加不改变生产行为的验证脚本设计。

**禁止修改：** 生产行为、公开外观、既有设计结论和教程内容。

- [x] 记录当前分支、提交、工作树、Go 版本、平台和外部依赖条件；
- [x] 统计包、生产文件、测试文件、教程、Example、设计和历史验收资料；
- [x] 执行全仓测试、Vet、Race、覆盖率、生成一致性和跨平台构建基线；
- [x] 执行已有关键 Benchmark，记录样本、次数、硬件和结果；
- [x] 登记失败、跳过、外部环境限制、偶发行为和未覆盖包；
- [x] 形成后续所有修改都要比较的基线摘要。

**完成门禁：** 基线能够重复执行；所有失败和限制都有结论；没有借基线阶段修改生产行为。

**模型：** Terra 极高负责广度盘点；异常和高风险结论由 Sol 高复核。

### 6.1 Task 0 实际基线（2026-08-09）

#### 仓库与环境快照

| 项目 | 结果 |
| --- | --- |
| 分支与提交 | `v3`；`3fefc1eaee1c35f16666646ab1bb01e238a25a17` |
| 最近提交 | `3fefc1e feat: 完成RPC与Discovery运行时` |
| 平台与工具链 | Windows/amd64；Go `1.26.5`；本机 `CGO_ENABLED=1` |
| 模块依赖 | `go mod verify` 通过；NATS Go `v1.52.0`；etcd client/server `v3.7.1` |
| 外部命令 | 本机未安装 `nats-server`、`etcd`、Docker；内嵌 NATS/etcd 真实协议测试可执行 |
| 仓库规模 | 752 个文件、387 个 Go 文件、175 个生产 Go 文件、157 个测试 Go 文件 |
| 内容规模 | 73 个 Go 包、124 个 `docs` Markdown、56 个设计文档、3 个验收报告、285 个 Example 文件 |
| 基线工作树 | 仅包含本轮新增/修改的复审设计、实施计划和索引；无既有用户代码改动 |

#### 质量门禁结果

| 检查 | 结果 | 结论 |
| --- | --- | --- |
| `git diff --check` | 通过 | 无空白错误；索引文件仅有既有行尾提示 |
| `gofmt -l` | 32 个文件 | 登记为 `CODE-001`，Task 0 不格式化生产代码 |
| `go vet ./...` | 通过 | 无静态检查错误 |
| `go test ./... -count=1` | 通过，约 42.6 秒 | 全仓普通测试基线有效 |
| 串行全仓覆盖率 | 通过，约 137.7 秒 | 全仓 70.2%；排除 Example 与测试夹具后的生产代码 73.3% |
| `go test -race -p 1 ./... -count=1` | 通过，约 213.2 秒 | 未发现 data race |
| 生成一致性 | 通过 | 隔离副本显式生成两次，第二次无漂移；当前生成文件与 Git 对象一致 |
| Linux/amd64 构建 | 通过 | `CGO_ENABLED=0 go build -p 1 ./...` |
| macOS/amd64 构建 | 通过 | `CGO_ENABLED=0 go build -p 1 ./...` |
| Markdown 相对链接 | 206 个 Markdown，0 个断链 | 同时覆盖仓库级说明和 `docs`；仅检查本地相对目标是否存在 |

Linux 与 macOS 首次并行构建时 Go 链接器因本机内存不足退出；改为逐平台、`-p 1` 后均通过，
因此判定为执行并发造成的环境资源问题，不登记为产品缺陷。后续跨平台门禁固定使用受控并发。

#### 覆盖率基线

统一覆盖率文件包含 2019 个函数记录；排除 Example 与测试夹具后，生产代码包含 1725 个
函数记录，其中 222 个函数为 `0%`，795 个函数为部分覆盖。该统计是后续逐函数风险审计的
输入，不要求为不可达防御、平台替代实现或没有行为的声明机械补测试。

| 包 | 语句覆盖率 | 基线判断 |
| --- | ---: | --- |
| `internal/natsnet` | 28.6% | 明显短板；需结合真实 NATS 集成覆盖审计 |
| `rpc` | 57.7% | 重点功能短板；按 RPC/远程 RPC 批次逐路径审计 |
| `log/internal/rotate` | 68.6% | 文件系统错误与滚动边界需复核 |
| `internal/discovery/origin` | 72.5% | 会话、故障和恢复路径需复核 |
| `service` | 73.1% | Scheduler、Await、Timer、Event 为重点功能 |
| `node` | 76.5% | 生命周期、发布和退休恢复为重点功能 |
| `config` | 77.4% | 平台文件系统与错误路径需补充证据 |
| `discovery/provider` | 77.8% | 公共 SPI 契约和错误路径需复核 |
| `internal/discovery/etcd` | 78.1% | 外部集群故障恢复测试受环境限制 |
| `application` | 83.9% | 启停、回滚、命令控制和诊断需复核 |
| `command` | 84.9% | 已发现 Windows 文件读取偶发问题 |
| `log` | 84.5% | 队列、Flush、运行时控制和异常路径需复核 |
| `internal/rpcgen` | 86.9% | 生成错误图和确定性继续审计 |
| `internal/tcpnet` | 90.4% | 重点检查断线、过载和所有权分支 |
| `internal/timerwheel` | 94.0% | 保持高覆盖并人工检查关键分支 |
| `admin` | 96.4% | 认证、限流、暴露与关闭分支仍需人工复核 |
| `buildinfo`、`diagnostics`、`command/internal/processlock`、`internal/container/ringqueue` | 100% | 语句覆盖达标，仍不替代设计和分支 Review |

#### 代表性性能样本

硬件为 AMD Ryzen 7 7840HS，Windows/amd64，Go 1.26.5；以下均使用 `-benchmem`、
`-benchtime=100ms`、`-count=3`。短样本只建立同环境回归基线，不直接证明需要优化。

| Benchmark | 3 次样本范围 | 分配 |
| --- | ---: | ---: |
| Diagnostics Summary，0 Node/0 Service | 87.2～90.2 us/op | 208 B/op，1 alloc/op |
| Diagnostics Summary，64 Node/64 Service | 172.0～190.5 us/op | 33488 B/op，3 alloc/op |
| Config 单 YAML | 1.64～1.78 ms/op | 72049～73948 B/op，420～421 allocs/op |
| Discovery `Find100` | 15.94～16.47 ns/op | 0 B/op，0 alloc/op |
| TimerWheel `After` | 296.6～308.0 ns/op | 109～113 B/op，1 alloc/op |
| Log Disabled | 19.17～19.86 ns/op | 0 B/op，0 alloc/op |
| RPC RoundRobin | 12.48～12.82 ns/op | 0 B/op，0 alloc/op |
| RPC Primitive Codec | 36.73～38.26 ns/op | 0 B/op，0 alloc/op |
| RPC Await 基础分配 | 154.1～171.8 ns/op | 208 B/op，2 allocs/op |
| Scheduler 串行投递 | 468.1～484.8 ns/op | 48 B/op，2 allocs/op |
| 业务 Timer 创建并取消 | 320.0～351.3 ns/op | 0 B/op，0 alloc/op |
| 生成客户端本地 Await | 7.20～7.59 us/op | 2123～2125 B/op，32 allocs/op |

#### 条件跳过与环境限制

本机基线实际跳过 9 个条件测试：

- Windows 大小写不敏感文件系统：`TestLoadDirRejectsCaseFoldedPathCollision`；
- Windows 当前权限/能力不允许创建符号链接：`TestLoadDirSymlinkBoundary`、
  `TestLoadDirDoesNotFollowSymlinkDirectory`；
- 未配置外部 etcd：`TestEtcdProviderExternalServerCompatibility`、
  `TestEtcdProviderExternalClusterRecovery`；
- 未配置外部 NATS 集群：`TestExternalThreeNodeCluster`、`TestExternalClusterReconnect`、
  `TestExternalNATSRPCThreeNodeCluster`；
- 未显式开启 M22 性能矩阵：`TestM22RPCPerformanceMatrix`。

内嵌 etcd、内嵌 NATS、TCP 回环、跨进程命令和 Admin HTTP 测试均已在普通测试与 Race 中
通过。外部集群兼容、真实故障恢复、Linux/macOS 平台测试和完整性能矩阵仍由 Task 6 承接。

#### 初始问题台账

| ID | 问题与证据 | 必要性/等级 | 处理批次与当前结论 |
| --- | --- | --- | --- |
| `TEST-001` | 控制邮箱存在两个协议可靠性缺口：Windows 读取刚发布的响应文件会偶发返回 sharing/lock violation；同时响应错误码白名单截止到 Diagnostics `8002`，遗漏当前稳定 Admin 错误码 `8003/8004`。前者最初由覆盖率模式下 `TestRetireResumeCommandsRoundTrip` 暴露，Task 1 普通全仓回归又在 `TestControlMailboxPreservesBoundedOriginError` 复现，后者定向 `-count=100` 失败 2 次；后者可由 `errs` 常量与 `command.isKnownControlErrorCode` 静态对照确认 | 必须修复；L1；外观、文件格式和错误码数值保持不变 | **已关闭。** Task 3 增加实际 Windows 独占句柄分类和确定性重试测试，只对 sharing/lock violation 有界重试，并补齐 Admin 码；定向 100 轮、Command Race、全仓 Test/Vet 和三平台构建通过 |
| `TEST-002` | 生产代码语句覆盖率 73.3%，222 个函数为 0%，RPC、NATS、Service、Node、Discovery 等重点包存在明显缺口 | 必须逐风险关闭；跨 Task | **已关闭。** Task 5 按功能补齐关键路径，Task 7 合并七组 Profile 复核 1,672 个生产函数；跨平台仅余 15 个无操作接口、第三方罕见回调或内部防御函数，均有例外结论；重点功能和服务自调用 RPC 通过双平台 Race |
| `CODE-001` | `gofmt -l` 返回 32 个已提交 Go 文件，跨 Application、Command、Node、RPC、Discovery 等包 | 必须修正；L3；无外观影响 | **已关闭。** Task 8 独立终审发现原关闭记录仍漏 16 个历史文件，退回执行纯 `gofmt`；最终对全部 393 个 Go 文件检查为 0 个未格式化，受影响包 Race 与后续双平台全仓门禁通过 |
| `CODE-002` | RPC 保留已经被完整事实协调取代的 `AddTarget`/`RemoveTarget`，且 `Client` 为白盒测试保留未绑定调用预算和跳过 Prepare 的直接提交路径 | 有明确证据时简化；L2；正式外观不变 | **已关闭。** 删除旧单目标入口和测试专用生产 fallback；测试改用 `ReconcileTargets` 或显式白盒状态，集成夹具改走正式 Prepare 路径 |
| `CODE-003` | rpcgen 把全部 `.gen.go` 视为自身所有，tcpnet 暴露当前产品链路未使用的小端帧分支，Timer 仍保留 Go 1.23 前 Stop/Drain 规则 | 有明确证据时简化；L2；无正式外观影响 | **已关闭。** 生成器所有权收窄到 `.rpc.gen.go`，TCP 固定网络字节序，Go 1.26 Timer 使用当前 Stop/Reset 保证；对应测试、Fuzz 和生成确定性通过 |
| `CODE-004` | 生产代码残留大量 `M7/M11/M19` 和“未来/过渡”实施措辞，部分注释已与远端 RPC、Discovery 当前事实冲突 | 必须修正；L2 可读性；无行为影响 | **已关闭。** 统一改写为当前职责、协议和状态语义；保留行为仍必要的配置拒绝、Schema、fallback 和 SPI，不借注释清理改变外观 |
| `CODE-005` | YAML 原生 `.inf`、`-.inf`、`.nan` 可以进入配置快照；任意值读取会保留非有限数，强类型浮点解码也没有统一拒绝 | 必须修复；L3 正确性；配置外观收紧到确定性有限数 | **已关闭。** 快照建树和强类型浮点解码均拒绝非有限数；失败不修改调用方目标；定向测试、五类 Fuzz、Windows/Ubuntu 全仓门禁通过 |
| `CODE-006` | 最后一个 `OnInit` 返回时若启动 Context 已取消，Node 仍会启动 TimerEngine，随后才在 `OnStart` 屏障返回取消 | 必须修复；L3 生命周期；无公开外观影响 | **已关闭。** 在进入 Timer/Transport/Discovery 资源阶段前重新裁决 Context；失败测试确认取消后不创建运行资源，定向 100 轮、Race 和双平台全仓门禁通过 |
| `CODE-007` | 文本日志把消息、字段名和字符串值中的非法 UTF-8 原样写出，可能产生无法被标准文本工具可靠处理的日志 | 必须修复；L3 正确性；日志外观不变 | **已关闭。** 单次 Rune 扫描仅在发现非法编码时引用转义，保持单行有效 UTF-8；Fuzz、Race 和双平台门禁通过。基准中位数约增加 `2.4%`，分配保持 `264 B/op、2 allocs/op`，是必要且有界的正确性成本 |
| `CODE-008` | Module 创建的业务 Timer 可被同一 Service 的其他 Module 或 Service 直接控制，违背已确定的资源作用域；Ticker/Cron 自动终止后还会在 Module 注册表保留陈旧项 | 必须修复；L3 所有权与释放；公开签名不变 | **已关闭。** Timer 记录创建 Module 与注册项，Pause/Resume/Cancel 按调用作用域裁决，终态统一移除 Module 注册；越权失败样例、三类 Timer 委托和连续 panic 自动终止测试均通过 |
| `CODE-009` | 生命周期 `Await` 的等待函数 panic 时，若 Deadline 同时到期会既传播 panic 又累计 Timeout；与“panic 优先”设计及普通 Task Await 统计不一致 | 必须修复；L3 统计正确性；公开行为边界不变 | **已关闭。** 仅在等待函数未 panic 时记录 Timeout/Cancel，随后保持原有 panic 边界；确定性失败样例、Finalizer Await/panic 和 Race 均通过 |
| `TEST-004` | Application 公共 `Start`、固定数组原子解码、模板/Provider/命令注册边界和 Service `GetNode().ID()` 缺少直接证据 | 必须补齐；L3；无生产外观影响 | **已关闭。** 增加成功与非零退出的真实 `Start` 入口、数组成功/失败原子性、注册校验和 Node 外观测试；`Application.Start` 达到 100% 语句覆盖 |
| `TEST-005` | 日志字段映射、异步输出状态、崩溃日志与轮转重启，以及 Service/Module 成功、失败和逆序回滚缺少直接证据 | 必须补齐；L3；无生产外观影响 | **已关闭。** 补齐全部字段类型、异步确定性、文件恢复、生命周期与错误聚合测试；重点函数覆盖达到目标或形成可达性例外，Windows/Ubuntu 普通测试与 Race 均通过 |
| `TEST-006` | Timer/Event/Await/Safe、Module 委托、Finalizer 和 Cron 非法输入/随机输入缺少完整直接证据 | 必须补齐；L3；无生产外观影响 | **已关闭。** 补齐作用域、输入防御、错误/panic 聚合、Deadline、Finalizer、Safe 与 Module 全委托测试，并增加 Cron Fuzz；Service 覆盖率升至 `81.1%`，重点可达入口达到目标或记录例外 |
| `TEST-007` | 生成 RPC 客户端的普通 `Call`、Async 提交失败、无效绑定、并发复用、取消/超时后晚到响应等关键所有权和错误分支缺少直接证据 | 必须补齐；L3；无生产外观影响 | **已关闭。** 补齐业务错误、目标 panic、无路由、契约不匹配、32 路并发、队列满不回调、取消/超时晚到 Buffer 释放和可选 Context 测试；定向重复、Fuzz、Race 与双平台全仓门禁通过 |
| `DOC-001` | RPC 教程称 `go generate ./...` 可更新全部契约包，但当前唯一指令位于 `_support`，Go 通配递归会忽略下划线目录；显式生成脚本正确 | 必须修正；L3；外观以代码为准 | **已关闭。** 教程明确普通业务包可用 `go generate ./...`，以下划线、点或 `testdata` 开头的目录必须显式执行；当前教程契约使用 `go generate ./examples/_support/tutorialrpc` |
| `DOC-002` | 01～02 教程把终态后的重复 `Application.Stop` 写成返回同一结果，把 `app_name` 写成逐条日志字段，并链接到未发布旧示例占位；拆分配置示例注释还声称输出了实际未输出的标签 | 必须修正；L3；以当前代码外观为准 | **已关闭。** 文档改为当前 Stop、日志字段和示例事实，直接链接正式日志示例并删除旧占位；补充有限浮点说明，Markdown 相对链接复核 0 断链 |
| `DOC-003` | Application Options 示例使用 `\\n`，运行时输出字面量反斜杠和 `n`，与 README 预期换行不一致 | 必须修正；L3；Example 行为 | **已关闭。** 改为真实换行；Windows、Ubuntu 离线命令实跑均与 README 一致 |
| `DOC-004` | 日志教程包含未发布版本兼容历史，并把留存估算误写成磁盘上限；自定义 Handler 对任意字节直接转字符串；Module 示例绕过框架日志 | 必须修正；L3；以当前代码外观为准 | **已关闭。** 删除历史过程，明确留存不是硬配额；字节字段使用 Base64，示例改用 Service Logger，并补充 Module 必要拆分原则、静态上限和异步数据所有权说明 |
| `DOC-005` | Timer/Cron 教程混用系统墙上时间与 Node 逻辑日历语义；示例忽略 Timer/Safe 创建错误，本地事件统计写少一次同步监听，游戏时间示例又在 Ticker 回调提交合并数前读取统计 | 必须修正；L3；以当前代码外观与实跑结果为准 | **已关闭。** 统一逻辑时间表述并处理创建错误；事件统计修正为 `sync=3`；合并数改在当前回调返回后读取，Ubuntu 实跑得到 `coalesced=3`；删除发布前版本过程措辞 |
| `DOC-006` | RPC 教程在同 Node `OnStart` 直接调用尚处于 Starting 的本地目标，遗漏 Broadcast，未明确 Context Value 只在进程内保留；Example 根 README 又重复大段内部设计 | 必须修正；L3；以当前代码外观和生命周期为准 | **已关闭。** 本地调用改为全部 Service Running 后的 Timer Task；保留统一启动屏障，不放宽 Scheduler/RPC；补充 Broadcast、Context 传输边界和 Timer 错误处理，并把根 README 收敛为简洁使用路径；Ubuntu 实跑通过 |
| `CODE-010` | NATS 断线/重连回调先检查再写状态，可在 `Drain/Close` 提交终态后用迟到回调覆盖终态 | 必须修复；L3 并发终态；外观不变 | **已关闭。** 活跃状态转换与终态提交共用状态锁，迟到回调不能越过 `Draining/Closed`；状态转换函数覆盖 `100%`，重复与双平台 Race 通过 |
| `CODE-011` | Discovery 监听登记的状态检查与 Stop 清理存在竞态且遗漏 Retired；Provider 新快照应用与 TTL 清空可交叉，形成“已同步但目录为空” | 必须修复；L3 状态一致性；外观不变 | **已关闭。** 监听检查与登记在同一锁上线性化并接受 Retired；快照应用、同步元数据和 TTL 清空共用提交锁，过期后必须由新权威快照恢复；并发 100 轮、覆盖与 Race 通过 |
| `CODE-012` | Origin Server 的 Close 事实与普通命令共用有界 Actor 队列，队列满时可能永久丢失关闭；配置的控制连接上限未实际执行 | 必须修复；L3 资源与关闭；协议不变 | **已关闭。** Peer 关闭改为原子事实加合并唤醒，Actor 排队前后的 Close 均可收敛；实际执行连接上限；满队列回归、覆盖和 Race 通过 |
| `CODE-013` | RPC 自动候选把本地 Retired Service 映射成 Unknown，导致 `IncludeRetired()` 和本 Node 精确目标无法命中，与当前本地/远端合并候选外观冲突 | 必须修复；L3 路由正确性；当前外观不变 | **已关闭。** 本地候选完整映射 Running/Retired；默认仍排除 Retired，`IncludeRetired` 和精确本 Node 可调用；单元/生成集成重复 20 轮、Race 和教程实跑通过 |
| `TEST-008` | Service 自调用 RPC 只有 Await 零散证据，缺少 Call/Async/Notify、回调次数、执行槽释放和 Task 内同步 Call 的死锁边界 | 必须补齐；L3 核心 RPC；无生产外观影响 | **已关闭。** 增加普通 goroutine `Call`、Task 内 `Await/Async/Notify` 及 FIFO 屏障，并锁定 Task 内 `Call` 只能由 Deadline 返回；重复、Race 和双平台门禁通过 |
| `DOC-007` | 07～09 Example 忽略 Timer、Notify、Broadcast 或快照错误；自定义 Provider 使用低于 3 秒下限的 TTL；Await 输出与实际不一致 | 必须修正；L3 使用者路径 | **已关闭。** 全部创建/提交错误显式处理，Broadcast 输出部分失败详情；TTL 改为 3 秒并由 `Close` 回收 goroutine；输出按 Ubuntu 实跑修正 |
| `DEPLOY-001` | 仓库 NATS compose 只允许 8 MiB，但 Origin Discovery 系统消息上限为 16 MiB，使用仓库依赖的 NATS+Origin Discovery 必然冷启动失败 | 必须修复；L2 发布基础设施；不改变业务默认上限 | **已关闭。** `nats.conf` 提升到有界 16 MiB，并用 NATS 官方解析器增加配置契约测试；临时真实 Broker 完成 NATS RPC 与 Origin Discovery 实跑 |
| `ENV-001` | 本机没有外部 NATS/etcd/Docker，9 个环境或平台条件测试未执行 | 发布前必须取得替代或实测证据；L2 | **已关闭。** Task 5E 在指定 Ubuntu 使用真实 NATS 和 etcd 完成教程与集成路径；Task 6 又完成断线恢复、容量和长稳验收 |
| `CODE-014` | Admin/pprof 的 Start 在状态检查后才绑定并发布 Listener；整体 `closeResources` 可在这个窗口先观察到 stopped 并完成，随后迟到的 Start 遗留端口 | 必须修复；L3 HTTP 生命周期；公开外观不变 | **已关闭。** Application 专用冷路径锁把状态检查至 Listener 发布，与整体资源关闭线性化；网络绑定不持有 `app.mu`；确定性并发测试、双平台 Race 和全仓回归通过 |
| `CODE-015` | Endpoint 设计要求名称不超过 63 字节，Timeout/Body/Response Option 只能收紧 15 秒/1 MiB/4 MiB 硬上限，但代码只校验正数；后续 nil Option 还会覆盖先发生的错误 | 必须修复；L3 资源边界与错误确定性；当前外观不扩大 | **已关闭。** `Validate` 执行全部硬边界并保留首个 Option 错误；测试覆盖每个越界值和错误优先级；教程明确大结果改用文件或对象存储 |
| `CODE-016` | pprof 的超大 `seconds` 可在 `time.Duration` 乘法时溢出；索引按 Runtime 列表展示 Profile，私有 Mux 却硬编码名称；Symbol 缺少标准能力握手并静默截断超限 POST | 必须修复；L3 诊断正确性与有界输入；独立 Listener 外观不变 | **已关闭。** 转换前拒绝 Duration 溢出，按 `runtime/pprof.Profiles()` 注册路由，Symbol 返回握手并对超过 1 MiB 的 Body 返回 413；Handler、协议、真实 CPU/Trace 与 Race 均通过 |
| `TEST-009` | Admin 启停与整体关闭竞态、pprof 方法/取消/运行时互斥/协议，以及全部 Chapter 10 使用者路径缺少完整直接证据 | 必须补齐；L3 重点管理与诊断路径 | **已关闭。** 增加确定性生命周期和 pprof Handler 测试；Application 覆盖率升至 `89.5%`，Admin/Diagnostics 分别为 `96.4%/100%`；七组 Example 在 Ubuntu 全部实跑 |
| `DOC-008` | Chapter 10 已有七组 Example，但教程、计划、变更摘要和验收报告仍写六组；端点 Option 的硬上限对使用者说明不足 | 必须修正；L3 使用者事实；以当前代码外观为准 | **已关闭。** 全部数量修正为七组，教程在 API 就近位置说明 15 秒/1 MiB/4 MiB 只能收紧及大结果交付方式 |
| `DOC-009` | 性能教程把普通 Go Benchmark 与 P95/P99 混为同一输出，未说明 `MB/s` 只按请求 Payload；配置故障练习被非法 CLI Node 提前拦截且恢复示例使用错误 Service；Diagnostics 采集把 HTTP 错误当成功并错误声称产物已忽略 | 必须修正；L3 使用者路径与脚本可靠性 | **已关闭。** 区分平均/分位数/短矩阵/正式矩阵口径；配置练习真正命中 `nodes[0].id`；采集采用失败即非零和临时文件替换，生成物加入忽略规则；双平台脚本与 Ubuntu 实跑通过 |
| `DEPLOY-002` | 开发 Compose 默认把无生产认证的 etcd/NATS/监控/UI/Mongo 端口绑定全部网卡，并保留未纳入 v3.1 的 MongoDB 和仅 Compose 使用的未固定版本 etcdkeeper | 必须修复；L3 安全默认与范围简化；不影响框架外观 | **已关闭。** etcd/NATS 发布端口默认环回，可显式选择受控测试地址；删除 Mongo Compose 与 etcdkeeper，只保留实际验收的三节点 etcd/NATS；配置契约和 Ubuntu `docker compose config` 通过 |
| `TEST-010` | Chapter 11～12 的三个公开 Benchmark、24 场景短矩阵、四个排障练习和构建/部署命令缺少同一批实际执行证据 | 必须补齐；L3 发布辅助能力 | **已关闭。** Windows/Ubuntu 三个 Benchmark、普通与 Race 短矩阵、四个排障练习、双目标构建脚本、Ubuntu 可追溯制品和 Compose 展开均实测，结束现场清理完成 |
| `TEST-011` | 正式 M22 性能矩阵先运行约八分钟 Local 场景，外部 TCP/NATS/etcd 夹具错误只能在切换 Transport 后暴露 | 必须补齐；L3 测试基础设施；无生产影响 | **已关闭。** 增加只验证外部 etcd、NATS、TCP 监听、独立目标进程和跨进程路由的快速预检；不采集、不替代正式矩阵结果 |
| `TEST-012` | 缺少日志、Admin Summary/Full、pprof、执行统计和真实 Service 调度同时启用时的尾延迟与资源回收证据 | 必须补齐；L3 系统稳定性；无生产影响 | **已关闭。** 增加正式 Application 生命周期下的并行 HTTP 流量和 5,000 Task 采样；Windows 重复 20 次、Ubuntu Race 重复 20 次通过，端口与 goroutine 回落 |
| `PERF-001` | v3.1 RPC 正式矩阵部分场景低于 v3.0 复核线，Await/Async 分配统一增加；需判断是否值得优化 | 有证据时修改；L3 性能；外观冻结 | **保持现状并关闭。** 5/24 场景超过 `-15%` 复核线、0 场景超过 `-25%` 阻断线；Profile 与历史确认成本来自已确认的 Context、调用预算和严格完成语义。替换逐操作取消会增加锁与所有权复杂度，没有必要做生产优化 |
| `SEC-001` | `govulncheck` 发现间接依赖 `github.com/klauspost/compress v1.18.6` 存在当前不可达但有修复版本的 `GO-2026-5841` | 必须修复；L3 依赖安全；无外观影响 | **已关闭。** 升级到只有该安全修复的 `v1.18.7`；漏洞复扫消失，模块校验、NATS 定向/外部三节点及双平台全仓 Test/Race 全部通过 |

#### Task 0 结论

基线可重复执行，所有失败、偶发行为和环境限制均已有归类；生成器、Vet、普通测试、Race、
三平台构建、关键 Benchmark 和链接检查均取得有效结果。Task 0 没有修改生产行为、公开外观、
测试、Example 或教程，完成门禁满足，允许进入 Task 1。

## 7. Task 1：建立功能范围与使用者外观基线

**允许修改：** 契约保护测试和本计划台账；此阶段不改变外观行为。

- [x] 对照最终目标、v2→v3 迁移矩阵、设计索引、公开代码、教程和 Example 建立功能矩阵；
- [x] 把每项能力分类为必须发布、明确删除、明确延期或内部能力；
- [x] 盘点公开 API、配置、错误、CLI、RPC 生成客户端、Admin、Diagnostics 和扩展 SPI；
- [x] 区分正式外观、扩展 SPI、底层高级入口和内部实现；
- [x] 为重要外观建立 API、配置默认值、生成结果、CLI Help、路由和 Schema 契约保护；
- [x] 登记文档与当前外观代码的冲突，默认以当前外观代码为准；
- [x] 集中确认仍然存在的功能范围疑问。

**完成门禁：** 功能矩阵没有未知项；当前外观边界清楚；后续重构能够检测意外外观变化。

**模型：** Terra 极高完成清单；Sol 极高复核范围、外观分类和冲突结论。

### 7.1 当前代码外观裁决规则

本次盘点以当前代码和生成结果为外观事实来源。冻结的 v3.0 设计、迁移矩阵、教程和历史验收
用于解释意图与查漏，但不能覆盖已经人工检查过的当前外观。特别是：

- 当前管理入口是 `--admin`、`Start/StopAdminServer` 和 `AdminAddress`；旧独立 Diagnostics
  HTTP 外观不属于 v3.1；
- `Application.Diagnostics()` 仍是当前进程内 Full Snapshot 外观，
  `DiagnosticsSummary()` 是低基数 Summary 外观；
- 生成客户端的 `Await/Call/Async/Notify/Broadcast`、路由派生和 `IncludeRetired` 以当前
  `.rpc.gen.go` 为准；
- 未经开发者单独确认，后续设计和重构不得删除、改名或改变下表正式外观的语义。

### 7.2 发布功能矩阵

| 功能组 | 当前发布范围 | 主要代码/外观证据 | 教程与 Example | 结论 |
| --- | --- | --- | --- | --- |
| 工程、错误与构建信息 | 稳定错误码、版本/提交/构建时间 | `errs`、`buildinfo` | 01、12 | 必须发布；已实现 |
| Application 与命令 | Setup、start/help/version、自定义离线命令、stop/retire/resume、Node 选择、PID/控制邮箱 | `application`、`command` | 00～01、09 | 必须发布；已实现 |
| 配置 | YAML/JSON 目录合并、环境变量、严格框架配置、宽松业务配置、Duration/ByteSize | `config`、Application 配置镜像、Service 配置外观 | 02 | 必须发布；已实现 |
| 日志 | 包级和作用域 Logger、text/JSON、文件滚动、Flush、运行时级别和输出启停、自定义 Handler | `log`、`log/zaplog` | 03 | 必须发布；已实现 |
| Service 与 Module | 类型模板、实际实例名、静态 Module 树、生命周期、严格逆序释放、本地查询 | `service`、`node`、`application` | 04 | 必须发布；已实现 |
| 调度与安全执行 | 单执行槽、DispatchAsync、Await、默认预算、GoSafe/RunSafe、panic 隔离 | `service`、`internal/timerwheel` | 05 | 必须发布；已实现 |
| Timer、Event 与游戏时间 | After/Ticker/Cron、暂停/恢复/取消、同步/异步事件、Node Now/SetTime/AddTime | `service`、`node` | 05、v3.1 Node 时间教程 | 必须发布；已实现 |
| RPC 契约与本地调用 | origingen、静态 Codec、普通 Go/Protobuf、Bind、Await/Call/Async/Notify/Broadcast | `cmd/origingen`、`internal/rpcgen`、`rpc`、生成文件 | 06 | 必须发布；已实现 |
| TCP 与 NATS 远程 RPC | 同一客户端外观、Wire、连接恢复、Deadline、背压、消息上限 | `rpc`、`internal/tcpnet`、`internal/natsnet` | 07 | 必须发布；已实现 |
| 路由与 Broadcast | 精确 Node、RoundRobin、Random、稳定 Key、自定义 Selector、多目标和部分失败 | 生成客户端、`rpc.RouteSelector`、`BroadcastError` | 07、09 | 必须发布；已实现 |
| 服务发现 | 本地目录、筛选、监听、Await、Origin、etcd、自定义 Provider SPI | `discovery`、`discovery/provider`、内置 Provider | 08 | 必须发布；已实现 |
| 退休、恢复与优雅停止 | Service/Node/Application 退休恢复、发布确认、排空、回滚和异常恢复 | `service`、`node`、`application`、`command` | 09 | 必须发布；已实现 |
| Admin 与诊断 | Guard、Application/Service Endpoint、固定控制路由、Summary/Full、动态 pprof、监控 Source | `admin`、`application`、`diagnostics` | v3.1 第 10 章 | 必须发布；已实现 |
| 性能与排障材料 | Local/TCP/NATS Benchmark、可控配置/RPC/发现/诊断故障 | Benchmark、`examples/11-*`、`examples/12-*` | 11～12 | 发布辅助能力；Task 5G 已复核并实跑 |
| 部署运维材料 | 构建注入、停止预算、安全绑定、外部 NATS/etcd、本地 Compose | 构建脚本、部署指南、`deploy/compose` | 独立部署与运维指南 | 发布辅助能力；Task 5G 已补齐并实测 |

上述发布功能均能在当前代码中找到实现和至少一个测试或 Example 入口；Task 1 未发现“设计宣称
必须发布、但代码完全不存在”的功能。是否正确、完整、稳定以及测试是否足够，仍按 Task 2～7
逐层验证，不能由本矩阵提前判定通过。

### 7.3 明确不属于本次发布的能力

| 分类 | 能力 | 已确认去向 |
| --- | --- | --- |
| 已删除/替换 | v2 反射 RPC、框架级 Global 配置、Service 多业务 goroutine、全局 flag 命令表、正则发现筛选、独立 static Provider | 已由生成 RPC、任意业务根配置、单执行槽、实例 Runner、精确筛选和动态 Provider 替换 |
| 明确不实现 | RPC 隐式 JSON 回退、首版压缩及压缩配置、无限自动重启 | 避免隐式协议、未证明性能收益和不受控恢复 |
| 发布后组件 | gRPC/显式 JSON、TcpModule、KCP、WebSocket、HTTP/Gin、消息队列、MySQL/Redis/MongoDB/Kafka、HTTP Client Pool | 需要独立需求、设计、依赖和验收，不属于 Origin v3.1 核心缺失 |
| 待真实需求 | 固定步长 FrameTimer、Rank、SkipList/随机/UUID、Blueprint/DeepCopy 等通用工具 | 不因 v2 曾存在而机械迁移 |
| 待性能证据 | 同进程 RPC 透明短路、额外对象池、压缩、复杂无锁优化 | 只有 Profile/Benchmark 证明必要后单独设计 |

因此，后续 Review 不得把上述项目作为“顺手补功能”加入当前范围；发现真实项目需求时应另立
设计并取得确认。

### 7.4 当前公开外观分层

| 层级 | 包与入口 | 使用规则 |
| --- | --- | --- |
| 正式使用者外观 | `application.Application`、嵌入式 `service.Service/Module`、生成 RPC Client、`admin` Endpoint、`config`、`log`、`errs`、发现和值诊断类型、`buildinfo`、`command.Runner` | 教程和普通项目直接使用；当前代码优先并默认冻结 |
| 扩展 SPI | `discovery/provider`、`discovery/providertest`、`admin.Guard/Provider`、`log.Handler/Controller`、`rpc.StaticCodec/RouteSelector`、`diagnostics.Source`、自定义 `command.Command` | 只在替换后端或高级集成时使用；必须保持最小接口和所有权说明 |
| 框架集成/高级入口 | `node.New/Start/Stop/Rollback`、`rpc.Runtime/Client/Reader/Writer/Sizer`、`service` 的 Scheduler/Module 装配函数 | 当前仍是导出代码，但普通教程不得引导直接装配；本轮不以“清理导出符号”为理由改动外观 |
| 工具与测试 | `cmd/origingen`、`discovery/providertest`、测试夹具 | 分别用于生成和 SPI 一致性验证，不属于业务运行时调用面 |
| 内部实现 | 全部 `internal/*` | 不构成使用者兼容承诺；可在保持正式外观和行为的前提下优化 |

### 7.5 契约保护证据

新增 `tests/contracts/public_api_contract_test.go`，只通过编译期接口和准确函数签名固定正式教程
外观，包括 Application、Service/Module、NodeRuntime、命令、配置、日志、Admin、扩展 SPI
和真实生成 `PlayerServiceClient`。它有意不冻结框架集成层，避免把内部包协作误当作普通用户
契约。

既有行为测试继续保护：

- CLI Help、参数、退出码和废弃命令拒绝：`command` 单元及跨进程集成测试；
- 配置默认值、严格字段和教程配置：`application`、`config`、`rpc`、`log` 测试；
- 稳定错误码：`errs.TestStableCodes` 及各模块错误映射测试；
- 生成结果、ABI、非法签名和确定性：`internal/rpcgen` 与生成 RPC 集成测试；
- 路由、Broadcast、Retired 和 Context：`rpc` 与 `tests/integration/rpcfixture`；
- Diagnostics Summary/Full JSON：`diagnostics` Schema 测试及 Application Admin 路由测试；
- Admin Guard、输入输出上限、Service 执行槽和固定控制路由：`admin`、`application` 测试。

### 7.6 Task 1 新增问题

| ID | 问题与证据 | 必要性/等级 | 处理批次与当前结论 |
| --- | --- | --- | --- |
| `DOC-002` | 当前使用指南索引和多处 00～12 交叉链接仍指向冻结的 `10.diagnostics-and-pprof.md`；该页包含已删除的 `--diagnostics` 和 `Start/StopDiagnosticsServer`，公开 API 索引也把 v3.0/v3.1 外观混在同一行 | 必须修正；L3；代码外观优先 | **已关闭。** 当前学习入口统一指向 v3.1 Admin 教程，API 索引按代码与 `tests/contracts` 重写；冻结历史页保留迁移提示，不再作为当前教程入口 |
| `DOC-003` | 根 README 宣称学习路径覆盖部署，设计也要求不占编号的部署运维材料，但当前只有本地开发 Compose 和散落说明，没有面向使用者的构建、启动、停止、安全、Grace Period 与外部依赖指南 | 必须补齐或收缩声明；L3 | **已关闭。** Task 5G 新增简洁的独立部署运维指南，覆盖可追溯构建、目录权限、start/stop、内部/外部停止预算、systemd、安全边界、依赖与发布检查；不新增生产框架 |
| `DOC-004` | 根 README 的“06 的 TCP 示例”和 Example 索引“前六章”与实际 00～06 本地无外部依赖路径不一致 | 必须修正；L4 | **已关闭。** Task 5G 统一为 `00～06` 以及 `07` 的 TCP 示例不需要外部中间件 |
| `TEST-003` | 过去没有一处只锁定正式使用者外观、同时排除框架集成层的编译契约 | 必须补齐；L3；无生产影响 | **已关闭。** 新增 `tests/contracts/public_api_contract_test.go`，明确排除框架集成层；定向 Test/Vet 和后续双平台全仓门禁通过 |

### 7.7 Task 1 范围结论

当前功能矩阵没有未知项：每项能力均已归入必须发布、发布辅助、明确删除/替换、发布后组件、
待真实需求或内部实现。当前正式外观和扩展 SPI 已分层，文档冲突均按当前代码裁决；没有需要
开发者立即选择、会改变本次发布功能边界的疑问。

新增契约包 Test/Vet 和全仓 Vet 均通过，`git diff --check` 通过。普通全仓 Test 只失败于已经
登记、且随后以定向 `-count=100` 再次复现的 `TEST-001`；该失败与新增编译契约无关，并已提升
为 Task 2/3 优先处理的 L1 正确性问题。Task 1 自身完成门禁满足，允许进入 Task 2。

## 8. Task 2：全局设计与 L0/L1 复核

本阶段只确认设计，不修改生产代码。按以下顺序逐项读取主设计和对应实现：

1. 功能、错误、配置、日志、命令和诊断等公共约束；
2. Application、Node、Service、Module 生命周期和资源所有权；
3. Scheduler、Await、Timer、Event、游戏逻辑时间和 panic 边界；
4. RPC 契约、生成器、Codec、Wire、路由和 Broadcast；
5. TCP/NATS Transport、Discovery、Origin/etcd Provider、恢复和退休状态；
6. Admin、Diagnostics、pprof、安全、可观测性和运维边界。

- [x] 每个主题给出保持现状、需要简化、需要修正或拆分确认的结论；
- [x] 每个优化写明问题、证据、最小方案、范围外、停止条件和验证方式；
- [x] 删除只由理论优雅、未来扩展、形式统一或冷路径微收益驱动的建议；
- [x] 按依赖和影响整理需要确认的 L0/L1 问题批次；
- [x] 将候选冻结结论回写单一主设计，避免多个文档继续冲突；
- [x] 形成 Task 3 的唯一允许实施清单。

**完成门禁：** L0/L1 没有未确认项目；设计和外观边界冻结；未进入清单的建议不得实施。

**模型：** Sol 极高。

### 8.1 复核方法与证据范围

本阶段没有只按目录或只按教程线性阅读，而是先按跨模块依赖检查所有权，再按功能核对现行
设计、代码、测试和教程外观：

1. 读取公共错误、配置、日志、命令、Admin/Diagnostics 设计和当前实现，确认稳定值与外观；
2. 顺着 `Application → Node → Service → Module` 启动、失败回滚和严格反序停止链路检查资源；
3. 检查 Scheduler、Await、Timer、Event、发现投递和 finalizer 的状态、容量与 goroutine 交接；
4. 检查生成 RPC 外观、Runtime、TCP/NATS 恢复 owner、路由快照、Provider actor 和关闭等待；
5. 对照普通测试、Race、故障恢复测试、跨进程测试、基准和 Task 0 覆盖率基线判断风险；
6. 搜索全部生产代码中的 Deprecated、兼容路径、后台 goroutine、Context 根、取消和等待点。

包依赖保持单向：`application` 负责装配，`node` 组合 Service/RPC/Discovery/Timer，`rpc` 只依赖
Service 最小能力和内部 Transport，`service` 不反向依赖具体 Node，`admin` 不依赖 Application。
未发现 import cycle、第二套生命周期所有者或需要调整目录层级才能修复的问题。

### 8.2 分主题设计结论

| 主题 | 结论 | 依据与范围控制 |
| --- | --- | --- |
| 公共错误、配置、日志与命令 | **修正一项，其余保持** | 当前错误码、配置单位/严格度、日志所有权和 CLI 外观合理；控制邮箱的 Windows 瞬时读冲突与错误码白名单漂移合并为 `TEST-001`。不新增全局错误注册表，不改变错误码、命令或文件格式 |
| Application/Node/Service/Module 生命周期 | **保持现状** | 资源在启动前完成静态校验，启动失败按实际取得的所有权回滚，正常停止按依赖反序并继续尝试全部清理；Admin/pprof 最后关闭。没有证据支持增加容器、生命周期接口或第二份停止顺序 |
| Scheduler/Await/Timer/Event/游戏时间 | **保持结构，后续逐函数补测和清理注释** | 单执行槽、Await 原 goroutine 恢复、DeadlineQueue、Timer 代次墓碑、发现合并投递和 finalizer 选举共享同一组不变量，机械拆分类或合并状态会增加交接风险。纯 Go 无法可靠识别 goroutine 身份的限制已有明确契约；不引入 goroutine ID、`unsafe`、固定 Runner 池或新的调度框架 |
| RPC 契约、生成器、Codec、Wire、路由与 Broadcast | **保持现状** | 生成代码是正式调用外观；本地/TCP/NATS 共用契约、Deadline 和 Buffer 所有权，自动路由读取不可变发现快照，Broadcast 明确部分失败。没有 Profile 证据支持透明本地短路、压缩、额外对象池或无锁重写 |
| TCP/NATS Transport 与 Discovery Provider | **保持现状** | 每个 TCP 目标、TCP Listener、NATS Runtime、Origin/etcd Provider 都有唯一恢复 owner、可取消生命周期和退出等待；启动由调用 Context 有界，运行期持续恢复由 Stop 取消。不得为形式统一合并两种 Transport/Provider 状态机，也不增加自动重发业务 Request |
| 退休、恢复和发现发布 | **保持现状** | Retired 是可观察状态而非隐式拒绝；动态发布用代次合并并等待 ACK，停止先撤销发现、关闭入站准入，再排空 Service。该顺序与当前用户语义一致，不把退休扩展成摘流、暂停 Timer 或自动停止 |
| Admin、Diagnostics、pprof 与安全 | **保持架构，系统阶段继续验收** | Admin 由 Application 拥有并把 Service Endpoint 投递到唯一执行槽；无 Guard 只允许环回；pprof 使用独立 Listener，避免把高敏感 Profile 混入常开管理面。Task 6 继续验证暴露、限流、关闭和资源上限，不在本阶段增加认证框架、Router 或监控存储 |

本阶段未发现 L0 架构问题。现有核心结构虽然代码量较大，但复杂度对应真实的状态、并发、回滚、
协议和资源所有权约束；“文件很长”“状态较多”或“可以抽象得更统一”本身不构成必要优化证据。

### 8.3 必要优化决策：控制邮箱协议可靠性

| 字段 | 决策 |
| --- | --- |
| 问题 | Windows 客户端在响应文件已经可见、但另一句柄尚未允许共享读取的极短窗口内立即失败；控制响应白名单也遗漏已经公开的 Admin 稳定错误码 |
| 证据 | 两次全仓运行和 `-count=100` 已复现 sharing violation；`command/control.go` 的末段范围只到 `CodeDiagnosticsStateConflict`，而 `errs/code.go` 已继续定义 `CodeAdminUnavailable` 与 `CodeAdminStateConflict` |
| 保持现状 | 不可接受。前者造成已成功执行的控制操作被调用方误报失败；后者使合法 Origin 错误不能通过既定响应校验 |
| 最小方案 | 仅在 Windows 把 `ERROR_SHARING_VIOLATION` 和 `ERROR_LOCK_VIOLATION` 识别为响应读取的瞬时错误，由现有 25ms 轮询和请求 Context/Deadline 有界重试；其他文件类型、权限、I/O 和解码错误语义不变。白名单末端扩展到 `CodeAdminStateConflict` |
| 范围外 | 不改变公开函数、CLI、控制文件名/JSON、轮询间隔、默认 Deadline 或错误码；不重写为 socket/pipe，不给所有 I/O 加通用重试，不增加固定 Sleep，不吞掉持久错误 |
| 停止条件 | 两个确定性回归通过且原偶发测试高次数稳定后停止；若错误不是这两个 Windows 系统错误，或修复要求修改协议/外观，则退出当前批次重新进入设计门禁 |
| 验证 | Windows 独占句柄确定性测试；Admin 8003/8004 编解码测试；`command` 定向 `-count=100`、Race、全仓 Test/Vet、`git diff --check`、Linux/macOS 交叉构建 |

请求端 claim、服务端 processing 读取和删除路径目前没有同类失败证据，而且扩大重试点会改变损坏
文件与权限错误的诊断速度，因此不随手扩展。Task 5 逐功能 Review 若取得新的确定性证据，再以
独立问题处理。

### 8.4 兼容与复杂度候选的处理结论

| 候选 | 当前结论 |
| --- | --- |
| `Application.Logger`、`Node.Logger` Deprecated 外观 | **保留。** 它们属于当前代码外观；“未发布所以无需兼容”不能覆盖已经明确的外观优先原则，删除必须单独确认 |
| Diagnostics Full v2 Deprecated 字段 | **保留。** 当前 Full Schema 和教程外观优先；Summary 已采用更简洁口径，不为字段整齐破坏 Full |
| `rpc.AddTarget`、生成器旧文件名识别、RPC/Timer 测试 Runtime 回退 | **进入 Task 4 逐项证明。** 只有确认不属于正式外观、扩展 SPI、底层集成或必要测试边界后才能删除 |
| Scheduler 大文件和多个私有状态 | **不按行数重构。** Task 5 只处理有特征测试保护、能减少真实重复职责且不改变状态机的局部简化 |
| 透明本地 RPC、压缩、额外池、无锁化 | **不实施。** 当前基准/Profile 没有证明必要，属于本轮明确范围外 |

### 8.5 Task 3 唯一允许实施清单

Task 3 只允许一个 `TEST-001` 命令控制协议批次，按以下顺序实施：

1. 增加 Windows 确定性失败测试：响应文件由不共享读的句柄短暂持有，释放后验证等待循环返回
   原响应；测试由同步信号控制句柄生命周期，不靠概率竞争；
2. 增加 Admin `8003/8004` 合法控制响应回归；
3. 增加平台私有瞬时错误判断，Windows 仅匹配 sharing/lock violation，其他平台恒为否；
4. 仅在 `waitForControlResponse` 的响应读取处继续轮询上述瞬时错误；
5. 把控制错误码末端范围扩展到 `CodeAdminStateConflict`；
6. 执行定向、高次数、Race、全仓和跨平台验证，确认正式外观与协议数据不变。

没有其他 L0/L1 生产修改进入 Task 3。任何新发现都先回到本节集中确认，不得顺手实现。

### 8.6 Task 2 当前门禁结论

全局设计复核已经完成，冻结结论已回写总设计；没有 L0，也没有需要改变当前使用者外观的
设计。唯一 L1 是保持外观不变的 `TEST-001` 最小修复。该具体设计已于 2026-08-10 取得开发者
确认，Task 2 正式完成，允许进入 Task 3。

## 9. Task 3：实施必要的 L0/L1 修改

如果 Task 2 没有确认的 L0/L1 修改，本 Task 记录“无必要修改”后跳过。

- [x] 按实际包依赖图自底向上排列已确认修改；
- [x] 公共契约变化先建立契约测试，并逐项适配全部调用方；本批无公共契约变化；
- [x] 核心架构变化先锁定状态机、并发和所有权不变量；本批无核心架构变化；
- [x] 生成契约变化只修改生成器和输入，再生成并验证确定性；本批无生成契约变化；
- [x] 每个独立主题完成相关测试、Race、覆盖率、Benchmark 判断和全仓回归；
- [x] 验证当前确认外观没有发生未经批准的变化；
- [x] 更新设计、台账和验证结果后关闭主题。

**完成门禁：** 已确认 L0/L1 全部关闭；架构和外观重新冻结；不存在依赖后续批次才能正确的
临时路径。

**模型：** Sol 极高；普通机械适配可使用 Sol 高，但最终由 Sol 极高复核。

### 9.1 `TEST-001` 实施结果

实施严格限制在 `command` 私有控制协议路径：

- 新增 Windows 平台错误判断，只用 `errors.Is` 匹配
  `ERROR_SHARING_VIOLATION`/`ERROR_LOCK_VIOLATION`；Unix 恒不重试；
- 响应等待仍使用既有 25ms ticker 和调用 Context/Deadline，仅在上述瞬时错误后进入下一轮；
- Access Denied、非普通文件、其他 I/O、JSON 损坏和请求 ID 不匹配语义保持不变；
- 控制错误码白名单上界从 Diagnostics `8002` 延伸到当前 Admin `8004`；
- 未修改公开函数、CLI、控制文件路径、JSON 字段、轮询周期、默认超时或错误码数值。

测试先于生产实现建立。实际 Windows 测试使用 share mode `0` 的独占句柄确认操作系统错误，
确定性 reader 测试确认首轮 sharing violation 后读取同一成功响应；另有 Access Denied 和仅含
相似文本的普通 error 反例，防止宽泛重试。

| 验证 | 结果 |
| --- | --- |
| 新增 Windows/Admin 定向测试 | 通过 |
| 原失败测试与新增回归 `-count=100` | 通过，20.763 秒 |
| `go test ./command -count=1` | 通过 |
| `go test -race ./command -count=1` | 通过 |
| `go test ./command -cover -count=1` | 通过，语句覆盖率 85.3%（基线 84.9%） |
| `go test ./... -count=1` | 通过，44.5 秒 |
| `go vet ./...` | 通过 |
| Linux/amd64、macOS/amd64 `CGO_ENABLED=0 go build -p 1 ./...` | 均通过 |
| `gofmt -l`（本轮 Go 文件）与 `git diff --check` | 通过 |

本修改是低频本地进程控制路径，未改变轮询周期、正常成功路径或任何业务热路径，因此没有新增
有意义的 Benchmark；高次数稳定性、Race 和全仓回归是本问题的直接性能/正确性证据。

Task 3 唯一 L1 已关闭，当前外观和架构重新冻结，不存在依赖后续批次才能正确的临时路径。

## 10. Task 4：L2 跨模块结构与兼容代码优化

- [x] 盘点 Deprecated、兼容、fallback、旧入口、重复路径和未使用公开面；
- [x] 判断其是否属于当前外观、扩展 SPI、必要测试边界或纯历史遗留；
- [x] 检查重复状态机、错误处理、Context、资源清理和基础算法；
- [x] 检查单一实现接口、过深调用层级、分散提交、重复校验和失效注释；
- [x] 只实施能减少真实概念、状态、分支、依赖或所有权歧义的最小方案；
- [x] 删除兼容路径时同步删除过时测试和文档，并保留当前功能契约测试；
- [x] 对保持现状项记录理由，避免后续重复讨论。

**完成门禁：** 所有跨模块复杂度和兼容路径均有保留或删除结论；架构冻结；没有为了代码行数
或形式统一增加新抽象。

**模型：** Sol 高实施；涉及生命周期、并发或多模块状态时使用 Sol 极高。

### 10.1 删除与简化结论

| 候选 | 结论与边界 |
| --- | --- |
| RPC `Runtime.AddTarget` / `RemoveTarget` | 删除。当前 Node/Application 只以 `ReconcileTargets` 提交完整发现事实；旧入口不属于冻结的正式外观，且形成第二套地址替换语义 |
| RPC 未绑定调用预算与直接 `Await/Call/Async` fallback | 删除。生成客户端必须执行 `Prepare → 编码 → 提交`，三阶段共享唯一 Deadline；白盒测试不再向生产状态机注入专用行为 |
| rpcgen 旧聚合文件迁移与宽泛 `.gen.go` 所有权 | 删除。生成器只拥有 `.rpc.gen.go`；历史聚合文件按普通未拥有文件保留，不扩大自动删除边界 |
| tcpnet `ByteOrder` / 小端帧 | 删除。当前 ORP/TCP 只使用网络字节序，仓库外没有该 internal 配置外观，不保留未发布 TcpModule 协议分支 |
| Go 1.23 前 Timer Stop/Drain | 删除。`go.mod` 最低版本为 Go 1.26.5，直接使用当前 `Timer.Stop/Reset` 的无旧值保证 |
| 历史里程碑和“过渡/未来”注释 | 删除或改写为当前事实；配置错误分类、协议数值、状态机和执行行为不变 |

### 10.2 明确保留结论

| 候选 | 保留理由 |
| --- | --- |
| `Application.Logger`、`Node.Logger` | 属于当前人工确认外观；继续标记 Deprecated，但不再承诺未经确认的删除版本 |
| Diagnostics Full v2 Deprecated 字段 | 属于当前 Schema v2；字段继续稳定返回，不能按普通历史兼容代码删除 |
| `service.Runtime` / `NodeRuntime` 分层与时钟 fallback | 基础调度 Runtime 不应被迫实现 Node 高级外观；正式 Node 提供逻辑时间，窄宿主使用 TimerEngine 实时时钟 |
| `rpc.RemoteResolver` / `RemoteSnapshotResolver` | 分别服务精确 Node 路由和自动实例选择，职责不同且避免热路径强迫读取完整快照 |
| Application 进程内 Discovery Source | 当前同进程多 Node 发现的数据源，不是历史 Provider 兼容层；只修正误导命名说明 |
| Context 错误互操作、宽松业务配置、日志 fallback、TLS 下限、临时 Accept 重试 | 都是当前正式错误、配置、故障诊断、安全或网络恢复语义，不因关键词命中而删除 |

### 10.3 实施中暴露的测试问题

移除 RPC 直接提交 fallback 后，`TestRemoteContractFingerprintMismatchFailsBeforeSend` 首次回归
返回 `invalid argument`。检查确认产品生成代码始终先执行 `PrepareAwait`，只有该夹具手工跳过
正式调用链。测试现已改为直接断言 Prepare 在编码和发送前返回契约不匹配；定向 50 次和完整
RPC 集成包均通过，没有恢复生产兼容分支。

### 10.4 Task 4 验证结果

| 验证 | 结果 |
| --- | --- |
| Windows 全仓 `go test ./... -count=1` | 通过，42.7 秒 |
| Windows 全仓 `go test -race -p 1 ./... -count=1` | 通过，261.2 秒 |
| Windows `go vet ./...`、origingen `--check` | 通过 |
| Ubuntu 26.04 / Linux amd64 / Go 1.26.5 全仓 Test、Vet、origingen `--check`、全仓 Race | 全部通过，总流程 231.5 秒 |
| Ubuntu tcpnet Fuzz | 3 秒执行 442897 次，无失败 |
| Ubuntu origingen 连续两次生成 | 生成文件汇总哈希前、第一次、第二次完全一致 |
| macOS/amd64 `CGO_ENABLED=0 go build -p 1 ./...` | 通过 |
| TCP 帧定向 Fuzz / Race / Benchmark | 通过；帧编解码 100% 覆盖，约 0.27～0.32 ns/op、0 分配 |
| Timer 定向 100 次与 Race | 通过；构造 100% 覆盖，Reset/Stop 正常路径已执行 |
| 变更文件格式与空白 | 69 个变更/新增 Go 文件均 gofmt-clean；`git diff --check` 通过 |

RPC 包覆盖率由基线 57.7% 变为 57.4%，原因是删除原本被测试覆盖的旧入口后分子、分母同时
减少；本批新增的 `invocationContext` 拒绝路径为 100%，没有新增未覆盖函数。RPC 全量逐函数
补测仍按 `TEST-002` 在 Task 5D 完成，不为了维持总百分比恢复无效生产代码。

Task 4 完成门禁满足：所有命中候选都有删除或保留结论，当前正式外观、配置、Wire 和 Schema
继续冻结，未增加新抽象。允许进入 Task 5A。

## 11. Task 5：按教程功能顺序完成 L3 纵向闭环

每批重复执行第 4 节通用循环，并在完成后单独运行全仓回归：

| 批次 | 功能 | 重点风险 | 模型 |
| --- | --- | --- | --- |
| A | 00～02：Application、Node、配置、命令 | 启动、默认值、失败回滚、平台进程 | Sol 高；生命周期用 Sol 极高 |
| B | 03～04：日志、Service、Module | 队列、Flush、资源归属、模块逆序释放 | Sol 高 |
| C | 05：Timer、Event、Await、游戏时间 | 单执行槽、恢复、取消、时间跳跃、竞态 | Sol 极高 |
| D | 06：RPC、生成器、Codec、本地调用 | 契约、所有权、错误映射、零拷贝边界 | Sol 极高 |
| E | 07～09：远程 RPC、Discovery、退休恢复 | Wire、pending、断线、重试、路由、状态一致性 | Sol 极高 |
| F | 10：Admin、Diagnostics、pprof | 认证、暴露、执行权、限流、敏感信息 | Sol 极高 |
| G | 11～12：性能、故障排查、部署运维 | 基准可重复性、故障真实性、脚本安全 | Sol 高 |

每批必须完成：

- [x] 功能、设计、当前外观、实现、测试、性能、Example 和教程一致性检查；
- [x] 功能遗漏、代码完整性、潜在错误和局部复杂度 Review；
- [x] 正常、边界、错误、取消、回滚、过载、并发、关闭和平台路径测试；
- [x] 重点功能 `100%` 可达语句和关键分支目标，或完整例外证据；
- [x] 热路径 Benchmark/Profile 和有证据的必要优化；
- [x] 无新增 L0/L1 问题，无范围外修改，无未解释失败。

**完成门禁：** A～G 全部关闭；每项发布功能都有实现、测试、性能和使用者路径证据。

### 11.1 Task 5A 实际结果：Application、Node、配置、命令

本批按教程 00～02 的使用者路径完成纵向 Review，再横向复核四个包。当前 Application、Node、
配置与命令外观继续冻结；没有新增抽象、兼容分支或功能。RPC、Discovery、退休、游戏时间、
Admin/Diagnostics 的专属分支分别留在 5C～5F，不用 A 的覆盖率机械提前测试。

#### 必要修改

- 配置快照和强类型解码统一拒绝 YAML 非有限浮点数；固定数组长度或元素错误继续保持整体
  原子性。这是确定性与序列化正确性修复，不是扩大配置设计；
- Node 在全部 `OnInit` 成功后、创建 Timer/Transport/Discovery 资源前再次检查启动 Context，
  已取消时直接进入既有失败回滚语义；正常启动顺序、错误码和公开接口不变；
- 增加 `Application.Start` 进程参数成功路径与非零退出路径、Setup/Provider/Command 注册、
  固定数组和 `GetNode().ID()` 的直接测试；不为不可达内部不变量增加生产测试钩子；
- 教程按当前代码修正 Stop 终态幂等、日志字段和拆分配置说明，删除旧日志示例占位；自定义
  命令示例改为真实换行。当前使用者外观始终以代码为准。

#### 覆盖与稳定性

| 项目 | 结果 |
| --- | --- |
| Windows 包覆盖率 | Application `85.0%`（基线 `83.9%`）；Node `76.7%`（基线 `76.5%`）；Command `85.3%`；Config `78.2%`（基线 `77.4%`） |
| Ubuntu Config 覆盖率 | `81.5%`；符号链接边界 `validateFileLink` 为 `82.4%`，Windows 无法执行的文件系统分支已实测 |
| 重点入口 | `Application.Start`、`LoadDir`、`LoadSnapshot`、`Snapshot.Decode`、`View.Decode`、`View.DecodeStrict` 均为 `100%`；固定数组解码 `87.5%` |
| 覆盖例外 | Node Start 中 Scheduler、RPC、Discovery 等依赖失败分支由 5C～5E 接续；随机源失败、损坏反射索引等不可达防御不增加生产注入点 |
| 重复与 Race | 四包随机顺序 `-count=10`、新增生命周期/入口定向 20～100 轮、四包 Race 均通过 |
| Fuzz | Windows 五入口 3 秒合计 `1,084,957` 次；Ubuntu 合计 `881,475` 次，无 panic、挂起或错误结果 |

#### Example、教程与全仓门禁

Windows 编译教程 00～02 的 9 个 Example 目录，并实跑自定义命令和带链接值的 `version`；Ubuntu
逐个构建并受控运行 8 条长期启动路径，以 `SIGTERM` 确认优雅停止，再实跑两个离线路径。全部
输出、退出和清理符合 README。185 个 Markdown 文件的本地相对链接复核为 0 断链。

| 门禁 | 结果 |
| --- | --- |
| Windows 全仓 Test / Vet / origingen `--check` | 通过；整组普通门禁 49.4 秒 |
| Windows 全仓 Race | 通过，228.4 秒 |
| Ubuntu 26.04 / Linux 7.0 / Go 1.26.5 | 全仓 Test 30.69 秒、Vet 0.99 秒、生成检查 1.24 秒、Race 148.53 秒，全部通过 |
| 格式与空白 | 本轮 Go 文件 gofmt-clean；`git diff --check` 通过 |

Application/Node 启动和配置解析都是冷路径；本批修复没有增加业务热路径工作，也没有 Profile
证据支持额外缓存、并行扫描或状态抽象，因此性能结论为保持现状。系统容量与性能仍按原计划
在 Task 6 统一验收。Task 5A 无已知遗留缺陷，完成门禁满足，允许进入 Task 5B。

### 11.2 Task 5B 实际结果：日志、Service、Module

本批按教程 03～04 完成纵向 Review，再横向复核日志实现和 Service/Module 生命周期。当前公开
日志、Service 与 Module 外观继续冻结。日志 Runtime/队列的所有权结构，以及 Module 为部分
初始化、部分启动、严格逆序回滚和幂等停止保留的状态，分别对应真实语义，删除会损失正确性，
因此不做设计改写。Scheduler、Timer、Event、Await 的内部实现及 Module 委托入口由 Task 5C
统一闭环，避免跨批次重复修改。

#### 必要修改

- 文本日志对消息、字段名和字符串值中的非法 UTF-8 进行最小引用转义，保证每条输出仍是
  单行有效 UTF-8；公开字段、级别、格式和配置不变；
- 自定义 Handler 示例把任意字节编码为 Base64，避免 JSON 编码时静默替换数据；异步保留
  `AnyField` 时补充复制所有权说明；
- 补齐日志全部公开字段种类、级别转换、Runtime 空值、异步输出状态、Crash/轮转恢复，以及
  Service/Module 正常启动、失败回滚、错误聚合和重复停止测试；
- 教程删除兼容历史，修正 `max_age` 整天约束和磁盘硬上限误导；Module 只按清晰职责与资源
  生命周期拆分，不因文件长度过度设计，示例统一使用框架 Logger。

#### 覆盖、性能与稳定性

| 项目 | 结果 |
| --- | --- |
| Windows 包覆盖率 | `log 87.6%`（基线 `84.5%`）、`log/zaplog 89.0%`（基线 `82.9%`）、`log/internal/rotate 71.6%`（基线 `68.6%`）、`service 75.3%`（基线 `73.1%`） |
| 重点入口 | `Level.String`、`ParseLevel`、`Logger.Log`、字段转换、Zap 级别转换、`State.String` 均为 `100%`；`StartWithModules 79.2%`、`StopWithModules 90.9%`，已覆盖全部支持的成功与失败回滚语义 |
| 覆盖例外 | 轮转底层不可达防御和损坏内部状态不增加生产注入点；Module 的 Timer/Event/Await 委托随 Task 5C 测试 |
| Fuzz 与 Race | 文本编码 Fuzz：Windows `104,749` 次、Ubuntu `59,250` 次；定向和全仓 Race 在两平台全部通过 |
| 性能取舍 | 文本编码基准中位数约由 `431.0 ns/op` 变为 `441.2 ns/op`（约 `+2.4%`），分配保持 `264 B/op、2 allocs/op`；无 Profile 证据支持扩大编码器重构 |

#### Example、教程与全仓门禁

Windows 编译/测试教程 03～04；Ubuntu 从临时工作目录构建并运行 7 个真实示例，等待启动后
发送 `SIGTERM`，验证正常退出、Service/Module 逆序停止和日志仅写入临时目录，随后清理。
185 个 Markdown 文件、693 个本地相对链接复核为 0 断链。

| 门禁 | 结果 |
| --- | --- |
| Windows 全仓 Test / Vet / origingen `--check` | 全部通过；分别为 `41.66`、`4.41`、`2.03` 秒 |
| Windows 全仓 Race | 通过，`66.22` 秒 |
| Ubuntu / Linux 7.0 / Go 1.26.5 | 全仓 Test `28.70` 秒、Vet `0.64` 秒、生成检查 `0.84` 秒、Race `39.08` 秒，全部通过 |
| 格式与空白 | 本轮 88 个变更或新增 Go 文件 gofmt-clean；`git diff --check` 通过 |

本批只接受有失败样例或所有权证据支持的修改。日志队列、Runtime、Module 生命周期结构和轮转
恢复设计均保持现状；性能广度、容量与长稳测试仍由 Task 6 统一执行。Task 5B 无已知遗留
缺陷，完成门禁满足，允许进入 Task 5C。

### 11.3 Task 5C 实际结果：Timer、Event、Await、游戏时间

本批按教程 05 的四条使用者路径完成纵向 Review，再横向复核 `service` Scheduler/Timer/Event/Await、
`node` 游戏逻辑时间和 `internal/timerwheel`。当前单执行槽、Await 所有权交接、生命周期
Finalizer、有界队列、Timer tombstone/对象复用、取消与停止竞态、游戏时间重排分别服务于明确
正确性和容量语义；没有证据支持拆分新的调度层、改写时间轮或引入额外索引，因此不做广泛
设计重构。公开签名、配置与使用方式继续冻结。

#### 必要修改

- Module Timer 增加创建作用域记录，只有创建它的同一 Module 可以 Pause/Resume/Cancel；Service
  Timer 继续由 Service 控制。Ticker/Cron 自动到达终态时同步释放 Module 注册，修复越权控制和
  陈旧资源登记，不增加公开接口；
- 生命周期 Await 在等待函数 panic 时不再同时累计 Deadline/Cancel，统一为 panic 优先；普通
  Task Await、Context、返回错误和统计外观保持不变；
- 补齐 EventID 防御、同步/异步错误与 panic 聚合、Module 全委托、Safe、Finalizer、Await 预算、
  Timer 作用域和自动终止测试，并以 Fuzz 验证 Cron 解析和 Next 计算不 panic；
- 教程和示例按当前代码修正 Cron 逻辑日历时间、Timer/Safe 错误处理、本地事件统计和 Ticker
  合并统计读取时点；删除尚未发布产品不需要的版本兼容过程措辞。

#### 覆盖、性能与稳定性

| 项目 | 结果 |
| --- | --- |
| Windows 包覆盖率 | `service 81.1%`（本批起点 `75.4%`）、`node 76.7%`、`internal/timerwheel 94.1%` |
| 重点入口 | Event 检查/统计、Safe、Module 的 Dispatch/Event/Await/Timer/Stats 委托、Service Timer 外观与游戏时间 Rebase 达到 `100%` 或接近完整；`executeFinalizer 95%`、Await 核心 `93.3%` |
| 覆盖例外 | After 的 nil 回调、生命周期 Await 非法内部状态和损坏状态防御不增加生产注入点；Retire、Discovery、RPC 专属分支留给 5D～5E，不为总百分比提前制造耦合测试 |
| 重复、Fuzz 与 Race | 作用域、Event 聚合和 Await 定向测试重复 `20` 轮通过；Cron Fuzz 为 Windows `384,615` 次、Ubuntu `324,759` 次；定向及全仓 Race 双平台通过 |
| Timer 作用域成本 | 创建/取消中位数约 `+2.9%`，暂停/恢复约 `+4.5%`，仍为 `0 allocs/op` 且样本区间重叠；接受一次必要指针归属检查，不扩大优化 |
| 游戏时间热路径 | Windows `Node.Now()` 约 `10.02～10.36 ns/op`，Ubuntu 约 `51.52～52.44 ns/op`，均为 `0 allocs/op` |
| 时间重排基线 | 10 万 Scheduled Timer：Windows 约 `29.39～37.71 ms`，Ubuntu 约 `28.38～30.98 ms`，约 `0.8 MB` 收集列表；符合 O(n) 设计，无场景证据支持复杂索引 |

#### Example、教程与全仓门禁

Windows 编译并测试教程 05 的四个 Example。Ubuntu 从隔离仓库构建并受控运行四个真实进程，
验证延迟/Cron、本地同步与异步事件、Await/Safe、Node 时间快进及 After/Ticker/Cron；全部在
`SIGTERM` 后输出停止完成并以 `0` 退出，临时进程和目录均已清理。游戏时间实跑确认
`Ticker coalesced=3`。185 个 Markdown 文件、693 个本地相对链接复核为 0 断链。

| 门禁 | 结果 |
| --- | --- |
| Windows 全仓 Test / Vet / origingen `--check` | 全部通过；分别为 `44.1`、`6.2`、`4.8` 秒 |
| Windows 全仓 Race | 通过，`226.5` 秒 |
| Ubuntu / Linux 7.0 / Go 1.26.5 | 全仓 Test `33.25` 秒、Vet `0.64` 秒、生成检查 `1.20` 秒、Race `147.33` 秒，全部通过 |
| 格式与空白 | 本批变更或新增 Go 文件 gofmt-clean；`git diff --check` 通过 |

本批只实现由失败样例、资源所有权或文档实跑支持的修改。Scheduler、TimerEngine、时间轮和
游戏时间重排结构保持现状；更大规模容量、尾延迟和长稳由 Task 6 统一验收。Task 5C 无已知
遗留缺陷，完成门禁满足，允许进入 Task 5D。

### 11.4 Task 5D 实际结果：RPC、生成器、Codec、本地调用

本批先按教程 06 的契约、生成、绑定、Await/Call/Async/Notify/Broadcast 路径纵向 Review，再
横向复核 `rpc`、`internal/rpcgen`、静态 Codec 和生成集成夹具。Prepare、编码、提交、完成的
分段状态，调用级绝对 Deadline、`localCall` 一次完成、冻结目标和生成静态 Codec 分别承担明确
的错误裁决、Buffer 所有权和晚到响应安全；没有证据支持合并状态、改为反射 Codec 或引入对象
池，因此生产结构和当前公开外观保持不变。

#### 必要修改与设计取舍

- 补齐普通 goroutine `Call` 的业务错误、目标 panic、无路由、契约不匹配和 32 路并发复用，
  以及取消/超时后晚到响应的 Buffer 释放；
- 在 Task 5E 回看时补齐 Service 自调用 RPC：普通 goroutine `Call`，Task 内 `Await/Async/Notify`
  及回调/FIFO 屏障；同时锁定 Task 内同步 `Call` 不释放当前执行槽、只能由 Deadline 结束，
  作为明确误用边界而不是隐式承诺重入；
- 补齐 Async 已准备但目标队列在提交时变满的回调抑制，以及 nil owner、空目标和
  nil/Background/TODO Await Context 的安全边界；不为不可达防御增加生产测试钩子；
- 教程示例实跑发现同 Node `OnStart` 期间本地目标仍处于 Starting，直接 RPC 必然返回无路由。
  保留 Node“全部 OnStart 成功后统一 Running”的正确性屏障，示例改为启动后的 Timer Task，
  不为教程放宽 Scheduler 或 RPC 生命周期；
- 教程明确 `go generate ./...` 不遍历下划线目录，补齐 Broadcast、Timer 创建错误和 Context
  Value 传输边界：同进程调用保留 Value，TCP/NATS 不序列化任意 Go Context Value，跨节点数据
  必须放入 RPC 参数；Example 根 README 删除重复内部设计，收敛为使用者最短路径。

#### 覆盖、Fuzz 与稳定性

| 项目 | 结果 |
| --- | --- |
| 包覆盖率 | `internal/rpcgen 86.9%`；`rpc` 直接包测试为 `57.4%`，合并生成集成测试并以 `-coverpkg` 统计为 `76.5%`，后者更能反映正式生成客户端路径 |
| 重点入口 | `FinishInvocation`、预算建立/归一化/校验、abort/abandon、回调所有权和 Context 错误映射达到 `100%`；生成 Prepare/Call/Async/Notify/Broadcast 达到 `83.3%～92.3%`，核心 Await 达到 `89.5%` |
| 覆盖例外 | Codec 主要读写路径完整覆盖；损坏内部状态和无法由生成代码构造的防御分支不增加生产注入点；远端/NATS/Discovery 专属分支由 Task 5E 接续 |
| 重复、Fuzz 与 Race | 新增关键测试重复 `20` 轮通过；Codec Fuzz 为 Windows `1,763` 次、Ubuntu `2,023` 次；RPC/rpcgen/生成集成定向 Race 和双平台全仓 Race 全部通过 |
| 生成确定性 | origingen 正常生成连续执行两次无 `.rpc.gen.go` 漂移，双平台 `--check` 均通过 |

#### 性能取舍

| 路径 | Windows | Ubuntu |
| --- | --- | --- |
| Primitive Codec | `38.11～40.62 ns/op`，`0 allocs/op` | `39.84～42.13 ns/op`，`0 allocs/op` |
| 绑定生成客户端 | `44.48～46.03 ns/op`，`0 allocs/op` | `46.00～46.56 ns/op`，`0 allocs/op` |
| 完整本地 Await | `6.96～7.58 µs/op`，`2106 B/op`、`31 allocs/op` | `5.62～5.73 µs/op`，`2101 B/op`、`31 allocs/op` |
| 完整本地 Call | `4.13～4.32 µs/op`，`1108～1109 B/op`、`20 allocs/op` | `3.54～3.65 µs/op`，`1107 B/op`、`20 allocs/op` |
| `localCall` 状态基线 | Await `156.7～165.5 ns/op`、`208 B/2 allocs`；Async `317.6～337.1 ns/op`、`432 B/4 allocs` | Await `72.20～73.15 ns/op`、`208 B/2 allocs`；Async `137.2～140.4 ns/op`、`432 B/4 allocs` |

当前分配对应一次完成 Channel、调用状态和晚到响应所有权。没有已确认容量目标或 Profile 证据
支持引入池化及 ABA 代际复杂度，因此本批不做性能代码修改；容量、P95/P99 和过载表现由
Task 6 在真实系统场景统一验收。

#### Example、教程与全仓门禁

Windows 编译并测试教程 06 的两个 Example。Ubuntu 从隔离仓库构建并受控运行两个真实进程，
分别验证绑定、Await、Call，以及 Async、Notify、Broadcast；输出 `player-1001`、`player-2002`
和刷新版本 `7/8`，均在 `SIGTERM` 后完成停止并以 `0` 退出。185 个 Markdown 文件、694 个
本地相对链接复核为 0 断链。

| 门禁 | 结果 |
| --- | --- |
| Windows 全仓 Test / Vet / origingen `--check` | 全部通过；分别为 `42.08`、`7.64`、`4.77` 秒 |
| Windows 全仓 Race | 通过，`208.55` 秒 |
| Ubuntu / Linux 7.0 / Go 1.26.5 | 全仓 Test `30.27` 秒、Vet `0.42` 秒、生成检查 `1.27` 秒、Race `131.52` 秒，全部通过 |
| 格式与空白 | 本批变更或新增 Go 文件 gofmt-clean；`git diff --check` 通过 |

本批没有发现需要扩大 RPC 生产设计或修改当前使用者外观的缺陷。关键本地调用路径已补齐测试，
教程已按实际生命周期和传输边界修正，Task 5D 无已知遗留缺陷，完成门禁满足，允许进入
Task 5E。

### 11.5 Task 5E 实际结果：远程 RPC、Discovery、Retire/Resume

本批按教程 07～09 纵向复核 TCP/NATS 远程 RPC、路由与 Broadcast、Origin/etcd/自定义
Discovery、Retire/Resume 和优雅停止，再横向阅读 `rpc`、`internal/natsnet`、`node`、
`internal/discovery`、`internal/discovery/origin`、`internal/discovery/etcd`、`application` 与
`service`。当前 Transport、Provider SPI、发现目录和退休外观保持不变；修改均针对可稳定复现的
终态、状态一致性、路由或教程缺陷，不新增兼容层、后台重试框架或推测性缓存。

#### 必要修改与设计取舍

- NATS 活跃回调与 `Drain/Close` 终态共用线性化状态锁，迟到的 disconnected/reconnected
  回调不能覆盖终态；没有把高层恢复状态机下沉到基础连接库；
- Discovery 监听登记与 Stop 清理在同一锁内裁决，并允许仍可执行的 Retired Service 注册；
  Provider 新快照与 TTL 过期清空以 `applyMu → mu` 固定锁序提交，过期后清除同步标志，必须先
  收到新权威快照才可由 Ready 恢复可用；
- Origin Server 把 Peer Close 改为不可丢的原子事实与合并唤醒，并实际执行已有控制连接上限；
  Close 不再竞争普通有界命令队列，也不增加无界队列；
- RPC 本地候选补齐 Retired 状态映射。默认自动路由仍排除 Retired，只有 `IncludeRetired()` 或
  精确 `OnNode(本 Node)` 可命中，和远端当前外观一致；
- 仓库 NATS 配置从 8 MiB 修正为有界 16 MiB，以覆盖 Discovery 系统消息上限；增加官方配置
  解析契约测试，不降低系统快照上限，也不扩大业务默认 4 MiB payload；
- 07～09 Example 对 Timer、Notify、Broadcast 和 Provider 快照错误显式处理；自定义 Provider
  使用最小合法 TTL 3 秒并在 `Close` 中取消、等待 goroutine；教程输出按 Ubuntu 实跑修正。

#### Service 自调用 RPC 专项

| 场景 | 结果与边界 |
| --- | --- |
| 普通 goroutine 使用该 Service 绑定的客户端 `Call` 自身 | 成功；调用方没有持有 Service 执行槽 |
| Service Task 内 `Await` 自身 | 成功；协作式释放当前执行槽，响应后恢复原 Task |
| Service Task 内 `Async` 自身 | 成功；回调在调用方 Service FIFO 中恰好执行一次 |
| Service Task 内 `Notify` 自身 | 成功；后置 FIFO 屏障确认通知效果已经提交 |
| Service Task 内同步 `Call` 自身 | 明确误用；不释放当前执行槽，测试用 Deadline 稳定返回 `ErrDeadlineExceeded`，不得无期限调用 |

这组测试属于本地 RPC 重入边界，不改变生成客户端方法或调度模型。教程 06 已明确：Service Task
自调用使用 `Await/Async/Notify`；同步 `Call` 只用于未持有该 Service 执行槽的普通 goroutine。

#### 覆盖、Fuzz 与稳定性

| 项目 | 结果 |
| --- | --- |
| 包覆盖率 | `rpc 57.6%`、`node 76.6%`、`internal/discovery/origin 72.2%`、`internal/discovery/etcd 78.1%`、`internal/discovery 85.2%`、`application 85.0%`、`service 81.2%`；`internal/natsnet` 直接包为 `29.8%`，真实连接行为另由集成包覆盖 |
| 重点入口 | RPC 本地候选捕获、NATS 活跃状态转换、Discovery `addListener`、Origin 客户端/服务端 `OnSystemClose` 均为 `100%`；Provider `replaceSnapshot 78.6%`、`expireSnapshot 85.7%`，有效快照、未到期、到期和并发提交均已覆盖 |
| 覆盖例外 | Provider 剩余为失效内部对象、底层目录 apply 失败和 Timer Channel 极窄竞态防御；不增加生产注入点或复制实现来追数字。Origin Actor 的损坏内部命令和不可能编码状态同理保留防御 |
| 重复与并发 | 新增状态、关闭、本地 Retired 和自调用 RPC 测试重复 `20` 轮通过；Provider 快照/TTL 并发提交循环 `100` 轮；定向和全仓 Race 在 Windows、Ubuntu 全部通过 |
| Fuzz | RPC Codec/TCP/NATS Wire、Origin Wire、目录快照、etcd Record 和生成自定义 Codec 共 7 个入口；Windows 3 秒窗口合计 `979,067` 次，Ubuntu 合计 `1,026,527` 次，无 panic、挂起或错误结果 |

#### 性能范围控制

本批修改位于连接状态回调、Provider 快照提交、监听登记和控制连接关闭冷路径。没有 Profile 证据
支持连接池、额外缓存、无锁化或后台重试，因此不做性能结构修改；`applyMu` 只串行化本就必须
原子提交的权威快照与 TTL 清空。吞吐、P95/P99、过载和恢复风暴仍在 Task 6 用真实负载验收。

#### Example、真实依赖与全仓门禁

Ubuntu 隔离副本实际构建并受控运行 07～09 全部 11 个 Example：TCP RPC、路由/Broadcast、
Origin、自定义 Provider、Await/Lost、Application/Node/Service 退休恢复和 `IncludeRetired` 均取得
预期日志；独立进程执行 `retire → resume → stop` 成功。真实 etcd 完成双 Node 发现/Lost；使用
修正配置启动的临时真实 NATS Broker 完成三 Node Origin Discovery 与远程 RPC，并在结束后删除
临时容器、二进制、配置和 PID 目录。

| 门禁 | 结果 |
| --- | --- |
| Windows 全仓 Test / Vet / origingen `--check` | 全部通过；分别为 `43.74`、`4.69`、`3.46` 秒 |
| Windows 全仓 Race | 通过，`234.13` 秒 |
| Ubuntu / Linux 7.0 / Go 1.26.5 全仓 Test / Vet / 生成检查 | 全部通过；分别为 `31.11`、`0.83`、`1.39` 秒 |
| Ubuntu 全仓 Race | 通过，`150.59` 秒 |
| Markdown 相对链接 | 185 个文件、694 个链接、0 断链（忽略 fenced/inline code） |
| 格式与空白 | 本批变更或新增 Go 文件 gofmt-clean；`git diff --check` 通过 |

本批没有遗留已知功能缺陷、竞态或教程不可执行项，也没有需要扩大整体设计或修改当前使用者
外观的证据。Task 5E 完成门禁满足，允许进入 Task 5F。

### 11.6 Task 5F 实际结果：Admin、Diagnostics、pprof 与指标适配

本批按教程第 10 章和七组 Example 纵向复核 Admin 启动/运行期启停、Application 与 Service
Endpoint、内置控制、Diagnostics Summary/Full、独立 pprof 和指标适配，再横向阅读 `admin`、
`application`、`diagnostics`、`node` 与 `service`。现有架构保持：Application 继续拥有两个独立
HTTP Listener，Service Endpoint 继续进入目标 Service 的唯一执行槽，无 Guard 时 Admin 继续只
允许环回。没有增加认证框架、TLS、通用 Router、流式响应、后台采样或监控存储。

#### 必要修改与范围控制

- 执行设计中已经存在但代码遗漏的 Endpoint 硬边界：名称最多 63 字节；Timeout、POST Body、
  Response Option 只能在 15 秒、1 MiB、4 MiB 内收紧；同时固定首个 Option 错误，不让后续 nil
  覆盖根因。大结果应写入文件或对象存储，不扩大管理端点预算；
- 用 Application 专用冷路径锁把 Admin/pprof 的“状态检查—绑定—发布”与整体资源关闭线性化，
  修复关闭完成后迟到 Start 泄漏 Listener 的竞态。网络绑定期间不持有 `app.mu`，请求和
  Diagnostics 读取不受阻塞；不把锁扩展成新的通用生命周期层；
- pprof 在乘法前拒绝超过 `time.Duration` 的秒数；私有 Mux 从当前 Runtime 动态注册全部 Profile；
  Symbol 补齐 `num_symbols` 握手并对超过 1 MiB 的 POST 明确返回 413。独立 Listener、运行期
  启停和标准 Profile URL 保持不变；
- 教程、Example 索引、专项计划、变更摘要和验收报告统一为七组；教程在 Option 附近直接说明
  硬上限和大结果替代路径。外观判断继续以人工确认过的当前代码为准。

这些修改都有设计约束、失败样例或协议证据。没有 Profile 证明 Diagnostics 聚合、Admin 路由或
pprof 需要缓存、无锁化、常驻采样或并行重写，因此本批不做推测性性能改造；Diagnostics 现有
Summary/Full 基准继续作为 Task 6 的容量基线。

#### 覆盖与关键路径

| 项目 | 结果 |
| --- | --- |
| 包覆盖率 | `admin 96.4%`、`application 89.5%`、`diagnostics 100.0%`；Application 较本批开始的 `85.0%` 提升 `4.5` 个百分点 |
| Admin/Diagnostics 重点入口 | `StartAdminServer`、`StopAdminServer`、请求路由、Body/Response 边界、错误映射及 Diagnostics 采集入口均为 `100%` |
| pprof 重点入口 | `newPprofMux`、Index、Cmdline、方法校验为 `100%`；Named/CPU/Trace/Symbol Handler 为 `93.8%～96.2%`；已覆盖成功、非法方法、无效参数、取消、进程级互斥、超限 Body 和符号查询 |
| 覆盖例外 | `StartPprof 94.4%`、`closeResources 94.7%` 的剩余语句是底层 bind/log/清理错误和损坏内部状态防御；标准库 Profile 写失败没有稳定公共注入点，不增加生产钩子或复制实现追求数字 |
| 并发与真实采样 | Admin Start/整体关闭确定性竞态测试通过；Windows、Ubuntu pprof 定向 Race 均通过；Ubuntu 实际 CPU Profile 与 1 秒 Trace 均生成非空结果 |

#### 七组 Example 的 Ubuntu 实跑

| Example | 实际验证 |
| --- | --- |
| 01 Service Endpoint | GET Summary、POST Reload、异步 Refresh/FIFO 结果、非法 JSON 400 |
| 02 Application 与内置控制 | Application GET/POST，Application/Node/Service Retire/Resume，缺失目标 404 |
| 03 本地 Diagnostics | Full Snapshot schema v2 以及 Application/Node/Service 摘要 |
| 04 Admin Diagnostics | Summary、`detail=full`、非法查询 400、错误方法 405 |
| 05 动态 pprof | 初始监听、关闭、重开、再次关闭；Admin 保持可用；Goroutine/Symbol、超大 seconds 400、真实 1 秒 CPU Profile |
| 06 Metrics Adapter | 五类低基数适配指标均输出实际值 |
| 07 动态 Admin | 初始关闭、打开、关闭、重开、再关闭；两个开放窗口均可查询 |

另用无需 Admin 的 Snapshot Example 在 Ubuntu 只开启 pprof，真实下载 1 秒 Trace，得到
`18,053` 字节；进程通过 Origin 自身 `stop` 退出。全部实跑结束后已确认相关进程、6060～6065
端口和临时文件均为空，未触碰指定隔离副本之外的目录。

#### 双平台门禁

| 门禁 | 结果 |
| --- | --- |
| Windows 生产修改后的全仓 Test / Vet / origingen `--check` | 全部通过；分别为 `47.92`、`4.42`、`3.40` 秒 |
| Windows 全仓 Race | 通过，`225.50` 秒；最终新增 pprof 测试的定向 Race 也通过 |
| Ubuntu / Linux 7.0 / Go 1.26.5 生产修改后的全仓 Test / Vet / 生成检查 | 全部通过；分别为 `28.89`、`0.53`、`1.42` 秒 |
| Ubuntu 全仓 Race | 通过，`132.78` 秒；最终新增 pprof 测试的定向 Race 也通过 |
| 最终测试集普通全仓回归 | Windows `40.12` 秒、Ubuntu `26.45` 秒，全部通过 |
| Markdown 相对链接 | 185 个文件、694 个链接、0 断链（忽略 fenced/inline code） |

Task 5F 没有遗留已知功能缺陷、竞态、资源泄漏或教程不可执行项，也没有扩大设计或性能优化
范围的必要证据。完成门禁满足，允许进入 Task 5G。

### 11.7 Task 5G 实际结果：性能、故障排查与部署运维

本批按第 11～12 章、七个公开脚本和部署入口纵向复核，再横向检查性能 Harness、构建信息、
进程控制、Compose 与当前功能矩阵。没有发现需要修改生产 Go 代码或框架外观的问题；所有修改
都限制在错误教程事实、脚本失败语义、开发依赖安全默认和明确不属于 v3.1 的部署残留。

#### 性能口径与实际结果

- 第 11 章明确区分普通 Benchmark 的平均 `ns/op`/分配、Linux 分位数 Benchmark、24 场景短
  矩阵和正式发布矩阵；TCP/NATS 的 `MB/s` 只按请求业务 Payload 计算，不再称为链路总带宽；
- Windows 三个公开脚本实际通过：Local Await `7,406 ns/op`；TCP 32 B `211,503 ns/op`、
  约 4 MiB `7.78 ms/op`；NATS 32 B `426,020 ns/op`、约 4 MiB `17.95 ms/op`；
- Ubuntu 同脚本实际通过：Local Await `6,087 ns/op`；TCP 32 B `41,546 ns/op`、约 4 MiB
  `6.25 ms/op`；NATS 32 B `74,852 ns/op`、约 4 MiB `8.92 ms/op`；这些数据只证明脚本可执行，
  不跨 OS 判断回归；
- Ubuntu 分位数入口得到 Local/TCP/NATS P50 为 `4.688/35.013/68.962 us`，P95 为
  `11.200/62.993/120.580 us`，P99 为 `21.761/86.804/164.503 us`；
- 24 场景短矩阵在 Windows、Ubuntu 的普通模式与 Race 下全部通过，`errors/timeouts/pending_end`
  均为零。短模式只验证场景、统计和清理，不用其 20/50ms 数据作性能结论；正式 5s/15s/3 轮
  真实集群矩阵按原计划留在 Task 6。

当前没有 Profile 或同环境退化证据支持修改 RPC、调度、Codec、Transport、缓存、对象池或锁；
因此本批不做生产性能优化。这符合“性能进入阶段 3、只优化有证据热点”的范围原则。

#### 排障 Example 与脚本

| 练习 | Ubuntu 实际验证 |
| --- | --- |
| 配置错误 | 脚本不再传非法 `--node` 抢先失败，实际命中配置 `nodes[0].id` 的 kebab-case 校验；恢复示例改用已登记的 `HelloService` |
| RPC 超时 | 真实生成 RPC Deadline、晚到响应和 Async 恰好一次集成测试通过 |
| Discovery Lost | 无网络 Provider 实际输出 `discovered → lost`；随后通过 Origin `stop` 退出 |
| Diagnostics 收集 | 实际生成 Full schema v2；Server 停止后采集返回非零、保留上一份有效 JSON 且不残留临时文件 |

Diagnostics 采集现在先写 `.tmp`，仅在 HTTP 成功时替换最终文件；两个生成文件和根 `run/`
运行目录已加入 Git 忽略。所有实跑 PID/JSON 均在验证后按精确路径删除。

#### 部署范围与安全默认

- 新增不占教程编号的部署运维指南，覆盖可追溯构建、目录权限、start/stop、内部
  `StopTimeout`、外部 `--timeout`、systemd、安全绑定、依赖保护和发布检查；
- `buildwin.bat`、`buildlinux.bat` 默认全包构建均通过且没有产生仓库文件；Ubuntu 以实际
  `main` 包构建临时制品，`version` 正确输出注入的 version/commit/build_time/go_version；
- Compose 端口默认从全部网卡收紧到 `127.0.0.1`，跨主机联调必须显式设置
  `ORIGIN_BIND_ADDRESS` 并自行提供认证和网络控制；
- 删除没有当前 MongoDB 适配器却暗示集成的 `mongo-compose.yml`，以及没有发布需求、未固定版本
  且只在 Compose 中出现的 etcdkeeper。Ubuntu 展开后只有实际验收的 etcd1～3、n1～3；
- 配置测试固定四个发布端口的环回默认，并继续用 NATS 官方解析器固定 16 MiB 消息上限。

#### 双平台门禁

| 门禁 | 结果 |
| --- | --- |
| Windows 全仓 Test / Vet / origingen `--check` | 全部通过；分别为 `41.53`、`4.08`、`3.36` 秒 |
| Windows 24 场景短矩阵 Race | 通过，`13.71` 秒 |
| Ubuntu / Linux 7.0 / Go 1.26.5 全仓 Test / Vet / 生成检查 | 全部通过；分别为 `28.72`、`0.38`、`1.23` 秒 |
| Ubuntu 24 场景短矩阵 Race | 通过，`6.89` 秒 |
| 构建与 Compose | Windows 两个构建脚本通过；Ubuntu 可追溯制品、Compose 契约和 `docker compose config` 通过 |
| Markdown 相对链接 | 186 个文件、700 个链接、0 断链（忽略 fenced/inline code） |

Task 5G 没有遗留已知功能缺陷、脚本静默失败、虚假部署能力或需要提前做的性能修改。至此
Task 5A～5G 全部满足门禁，生产功能纵向闭环完成，允许进入 Task 6 的 Ubuntu 系统级稳定性、
容量和正式性能验收。

## 12. Task 6：系统级稳定性、容量和性能验收

- [x] 以下系统级场景以 Ubuntu 实机为主环境执行，并记录系统、内核、Go 版本和资源条件；
- [x] 多 Node、多 Service 和典型业务规模端到端运行；
- [x] 真实 TCP、NATS、etcd 的启动、正常通信、断线、恢复和停止；
- [x] Discovery 更新、路由、Broadcast、Retire/Resume 与 RPC 并发；
- [x] 服务自己调用自己的 RPC：goroutine `Call`、Task 内 `Await/Async/Notify`，以及 Task 内同步 `Call` 的 Deadline 边界；
- [x] 队列满、pending 上限、过载拒绝、慢消费者和恢复风暴；
- [x] 重复启停、部分初始化失败、取消、Deadline、panic 和逆序回滚；
- [x] 长时间运行中的 goroutine、连接、Timer、Buffer 和内存稳定性；
- [x] 日志、Admin、Diagnostics、pprof 和指标同时启用时的资源与尾延迟；
- [x] 小消息、典型消息、最大消息和峰值负载的吞吐、P50/P95/P99；
- [x] 安全、敏感信息、依赖许可证、可重复构建、部署和跨平台复核；
- [x] 所有异常、波动和资源增长都有定位与结论。

**完成门禁：** 无已知缺陷或未解释异常；容量和性能满足确认目标；生产代码冻结。发现生产
问题时退回所属 Task，修复并重新执行受影响及后续门禁。

**模型：** Sol 极高分析高风险结果；重复执行和数据整理可使用 Terra 极高。

### 12.1 Task 6 实际结果

Ubuntu 26.04、Linux 7.0、Go 1.26.5、8 逻辑 CPU 和约 7.1 GiB 内存作为主环境。服务自调用
RPC 独立 Race 重复 100 次通过；Scheduler、Application、TCP/NATS RPC、Discovery、Broadcast、
Retire/Resume、过载、取消、Deadline、panic、回滚和外部三节点 NATS/etcd 均取得 Race 或真实
协议证据。可观测性共存测试在每轮 5,000 个 Service Task 下完成 Windows 20 次和 Ubuntu Race
20 次，Ubuntu P99 为 `93.415～121.666 µs`，停止后端口和 goroutine 回落。

正式 M22 矩阵完成 24 场景 × 3 轮、每场景 5 秒预热和 15 秒采集，共 72 条结果，耗时
`1,441.50` 秒，`errors/timeouts/pending_end` 全为零。相对 v3.0 有 5/24 场景超过 `-15%` 复核线，
无场景超过 `-25%` 阻断线；Profile 确认分配变化来自 v3.1 已确认语义，不做会增加锁、缓存、
对象池或取消所有权复杂度的过度优化。64 Node × 64 Service 的 Summary+JSON 中位约
`0.682 ms`，Full+JSON 中位约 `8.161 ms`；Full 继续只用于人工排障。

安全扫描发现并关闭 `SEC-001`：`compress v1.18.6` 升级到仅含安全修复的 `v1.18.7`。修复后
`govulncheck` 可达漏洞为零；剩余 `x/crypto/openpgp` 只存在于模块、未进入实际编译依赖。
许可证禁用检查通过，28 条可达库记录均有 Apache-2.0、BSD-3-Clause 或 MIT 分类。

安全补丁后的最终门禁为：Windows Test/Vet/生成检查/Race
`51.25/5.09/3.66/250.11` 秒；Ubuntu `46.93/1.52/1.21/150.21` 秒，全部通过。测试进程、
临时目录和端口无残留，既有 NATS/etcd 容器日志无错误、警告、慢消费者、panic 或 fatal。

完整环境、72 场景中位数、Profile 取舍、安全结论和异常定位见
[Origin 系统级稳定性、容量与性能验收报告](../reports/Origin系统级稳定性容量与性能验收报告.md)。
Task 6 完成门禁满足，生产代码冻结，允许进入 Task 7。

## 13. Task 7：测试、Example 和教程最终收口

- [x] 复核全仓逐包、逐文件、逐函数覆盖率和人工关键分支清单；
- [x] 补齐不要求改变生产行为的残余测试，整理重复、脆弱和无有效断言的测试；
- [x] 残余测试暴露生产问题时退回所属 Task，不在冻结后直接补丁；
- [x] 每个 Example 编译、受控运行、退出和清理，且与当前外观一致；
- [x] Example 的公开类型、执行步骤、生命周期、并发归属、资源释放和易错点注释完整；
- [x] 教程从第一次使用者角度按最短成功路径编写，语言简洁；
- [x] 每章包含前置条件、命令、预期输出、停止清理、常见错误和下一步；
- [x] 删除重复、历史过程和使用者不需要的内部细节；
- [x] 修复重复章节、旧路径、失效链接、配置、输出和脚本不一致；
- [x] 从干净环境按 00～12 学习路径完成主要场景。

**完成门禁：** 测试和覆盖审计关闭；Example 与教程全部可执行；生产代码保持冻结。

**模型：** Terra 极高完成广度和语言检查；关键语义、并发说明和最终结论由 Sol 高复核。

### 13.1 Task 7 实际结果

本批在生产代码冻结后补齐残余测试，并按 00～12 学习路径收口 Example、脚本和教程。公开外观
继续以当前代码与 `tests/contracts` 为准；冻结文档中的旧外观不反向覆盖当前实现。服务自己调用
自己的 RPC 同时纳入关键覆盖清单、Ubuntu 专项重复测试和双平台全仓 Race，覆盖普通 goroutine
`Call`、Task 内 `Await/Async/Notify`、FIFO 回调和 Task 内同步 `Call` 的 Deadline 边界。

最终原始全仓语句覆盖率为 `72.6%`，但包含独立 Example，不能机械作为发布目标。合并七组单元
与真实集成 Profile 后复核 `1,672` 个生产函数；Windows 零覆盖为 17 个，Ubuntu 已覆盖其中两个
文件链接边界，跨平台剩余 15 个均为明确的无操作接口、第三方罕见回调、不可达低层分支或内部
损坏状态防御，没有未知关键路径。新增测试覆盖 Node/Service 委托与状态、RPC Codec/Wire/Deadline、
TCP 心跳、NATS Notify/System Peer、Provider 容量、Origin 过期堆、配置数值边界和日志丢弃计数。

Ubuntu 实际执行 38 个常规当前示例和 9 个 NATS/etcd/性能/排障/诊断场景，共 47 个，全部通过；
NATS 使用测试自有 Broker、独立端口和唯一 namespace，etcd 使用唯一 network，未修改既有容器。
全部 62 个 Shell 脚本已固定 LF、Git 可执行位并通过 Ubuntu `bash -n`。当前 API 索引按正式外观
重写，全部当前交叉链接改到 v3.1 Admin 教程。

最终 Windows Test/Vet/生成检查/Build/Race 均通过，耗时分别为
`72.2/25.9/8.1/21.3/94.8` 秒；Ubuntu 受控并发串行门禁分别为 `32/1/2/9/45` 秒，全部通过。
完整覆盖例外、示例清单和脚本结论见
[Origin 覆盖率、Example 与教程最终验收报告](../reports/Origin覆盖率Example与教程最终验收报告.md)。
Task 7 完成门禁满足，生产代码继续冻结，允许进入 Task 8。

## 14. Task 8：独立终审与发布验收

使用新的任务上下文执行只读终审，不沿用前面批次的实现假设：

- [x] 逐项核对功能矩阵和原始要求追踪；
- [x] 复核全部问题都有实施、保持现状、延期或例外结论；
- [x] 复核当前外观、设计、代码、测试、性能、Example 和教程一致；
- [x] 复核重点功能覆盖目标和全部覆盖例外；
- [x] 重跑全仓 Test、Race、Vet、生成一致性、跨平台和真实协议门禁；
- [x] Windows 与 Ubuntu 分别取得实际测试证据；不得用 Linux 交叉构建替代 Ubuntu 门禁；
- [x] 重跑关键 Benchmark 和系统场景，确认没有未解释退化；
- [x] 确认无已知功能缺陷、竞态、死锁、泄漏、偶发失败或未解释风险；
- [x] 形成 `reports/Origin发布前全面复审验收报告.md`；
- [x] 达到发布门禁后冻结发布候选。

**完成门禁：** 总设计全部门禁满足；验收报告证据完整；没有必需的后续工作。

**模型：** Sol 极高。

### 14.1 Task 8 实际结果

独立终审重新核对功能矩阵、原始要求、问题台账、正式外观契约和高风险生产差异。终审发现
`CODE-001` 的旧关闭记录仍漏 16 个历史 Go 文件，立即退出终审并执行纯 `gofmt`；全部 393 个
Go 文件最终为 0 个未格式化，受影响包在 Windows、Ubuntu 的 Race 通过，台账已更正。除此之外
没有发现新的生产缺陷、外观冲突或未解释例外。

Windows 与 Ubuntu 重新执行 24 场景短矩阵、服务自调用 RPC Race `count=100` 和可观测性共存
Race `count=20`，全部通过。Ubuntu 三组 3 秒公开 Benchmark 的 Local/TCP 32B/NATS 32B 为
`6075/39120/74562 ns/op`，与同环境基线一致。

最终串行发布门禁结果：Windows Test/Vet/生成检查/Build/Race 为
`97.8/4.6/3.6/15.9/111.3` 秒；Ubuntu 为 `15/<1/2/10/37` 秒。全部通过；Ubuntu 另确认
62 个 Shell 脚本无 CRLF 且 `bash -n` 全部通过。完整原始要求追踪、覆盖例外、性能、安全和发布
决定见 [Origin 发布前全面复审验收报告](../reports/Origin发布前全面复审验收报告.md)。

Task 8 完成门禁满足，Origin v3.1 发布候选冻结。

## 15. Task 状态

| Task | 状态 | 备注 |
| --- | --- | --- |
| 0. 现状基线 | 已完成 | 基线、条件跳过和初始问题台账已记录 |
| 1. 功能与外观基线 | 已完成 | 功能无未知项；正式外观已分层并增加编译契约 |
| 2. 全局设计与 L0/L1 复核 | 已完成 | 全部主题已复核；无 L0，唯一 L1 设计已于 2026-08-10 确认 |
| 3. L0/L1 实施 | 已完成 | `TEST-001` 已按确认的最小设计修复并通过全部批次门禁 |
| 4. L2 跨模块优化 | 已完成 | 历史路径已最小删除，保留项理由明确，Windows/Ubuntu 全仓门禁通过 |
| 5. L3 功能闭环 A～G | 已完成 | A～G 全部完成；设计、代码、测试、性能、Example 和教程已形成纵向闭环 |
| 6. 系统级验收 | 已完成 | Ubuntu 正式矩阵、稳定性、安全依赖和双平台全仓门禁通过；生产代码冻结 |
| 7. 测试、Example、教程收口 | 已完成 | 覆盖例外全部有结论；47 个 Ubuntu 场景、62 个 Shell 脚本和双平台全仓门禁通过 |
| 8. 独立终审 | 已完成 | 发现并关闭 gofmt 台账漏项；双平台终审门禁通过，发布候选冻结 |

## 16. 当前门禁

Task 0～8 已满足完成门禁，Origin v3.1 发布候选已经冻结。后续变更受以下门禁约束：

- Task 5 每批按教程功能顺序形成纵向闭环，再横向检查模块遗漏；
- 当前使用者外观、配置、协议和 Schema 继续冻结，发现 L0/L1 问题时退回设计门禁；
- 生产代码已经冻结；若发现发布阻断缺陷，必须退回所属 Task 修复并重跑受影响及后续门禁；
- 发现总设计问题时先退回并更新总设计；
- 每个代码子批次必须取得 Ubuntu 定向测试证据，每个 Task 收口必须取得 Ubuntu 全仓门禁；
- 新功能、外观调整和推测性优化不得进入当前发布候选，必须开始新的设计与实施周期。
