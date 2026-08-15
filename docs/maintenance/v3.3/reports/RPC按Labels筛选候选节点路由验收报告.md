# RPC 按 Labels 筛选候选节点路由验收报告

> 日期：2026-08-15
>
> 状态：Windows 实现与验收完成
>
> 基线：v3.2.0 发布候选
>
> 目标：v3.3.0

## 1. 实现结果

生成 RPC Client 与底层 `rpc.Client` 已增加 `WhereLabels(map[string]string)`。条件在客户端
派生时冻结并按 AND 合并，Prepare 在现有契约和生命周期检查之后、Transport 与连接检查
之前直接读取候选快照 Labels；过滤结果继续交给默认 RoundRobin、显式 RoundRobin、Random、
稳定 Key或自定义 RouteSelector。

`OnNode` 与 Labels 可以统一组合且不扩大精确范围。无匹配返回 `ErrRPCNoRoute`；标签匹配但
连接断开继续保留 Await 等待分类。带 Labels 的 Broadcast 首期返回 `ErrInvalidArgument`，
没有 Labels 的现有广播范围不变。服务发现模型、Provider SPI、错误码、生成 ABI 3、Payload
和 TCP/NATS Wire 均未修改。

## 2. Benchmark

环境：Windows amd64，Go 1.27rc2，AMD Ryzen 7 7840HS；命令：

```text
go test ./rpc -run '^$' \
  -bench 'Benchmark(ClientWhereLabels|PrepareWhereLabels)$' \
  -benchmem -benchtime=1s -count=3
```

Prepare 样本为 1000 个已连接 TCP 候选：

| 场景 | 三轮范围 | B/op | allocs/op |
| --- | ---: | ---: | ---: |
| `WhereLabels` 双条件派生 | 152.2～153.4 ns | 64 | 1 |
| Prepare 无过滤 | 154.4～156.2 µs | 0 | 0 |
| Prepare 2 个匹配条件 | 174.3～175.6 µs | 0 | 0 |
| Prepare 32 个匹配条件 | 535.9～543.5 µs | 0 | 0 |
| Prepare 额外条件无匹配 | 58.1～58.6 µs | 0 | 0 |

逃逸分析确认条件 Slice 只在 `WhereLabels` 派生时逃逸；`matches` 与有界排序可内联。Prepare
基准没有新增堆分配、过滤结果 Slice、锁或缓存。上述微基准记录单次平均耗时，不冒充网络
端到端 P95/P99；尾延迟风险通过零分配、无新增锁、最大 32 条件和真实 TCP Race 用例约束。

## 3. 测试与覆盖率

新增测试覆盖：

- Map 冻结、稳定顺序、空值、重复、合并、冲突和 32 项容量；
- 默认、RoundRobin、Random、稳定 Key、自定义 Selector；
- OnNode 两种链式顺序、无匹配、错误 Value 和缺少 Key；
- 标签匹配但断开时的 Await waitable 分类；
- Broadcast 对有效和不可满足 Labels 都快速失败；
- 生成客户端公共方法与真实双 Node TCP 服务发现 Labels 路由。

`go test ./rpc -coverprofile=...` 总语句覆盖率为 59.9%；本次新增核心函数覆盖率：

| 函数 | 覆盖率 |
| --- | ---: |
| `routeLabelFilter.active` | 100% |
| `routeLabelFilter.find` | 100% |
| `routeLabelFilter.matches` | 100% |
| `Client.WhereLabels` | 92.6% |
| `Client.withImpossibleLabels` | 100% |
| `sortRouteLabels` | 100% |
| `candidateSet.scanEligible` | 95.7% |
| `candidateSet.routeError` | 100% |

## 4. 通过的门禁

```text
go test ./rpc ./internal/rpcgen ./tests/contracts ./tests/integration/rpcfixture
go test -race ./rpc ./internal/rpcgen
go test -race ./tests/integration/rpcfixture \
  -run TestM19TCPGeneratedBindingRoutesAcrossRunningInstances -count=1
go run ./cmd/origingen rpc --check ./...
go test ./...
go vet ./...
go build ./...
GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build ./...
git diff --check -- <本次变更路径>
```

全部通过。Windows 交叉构建 Linux 时启用 `CGO_ENABLED=0`；真实 Linux 运行与网络尾延迟
复测未在本轮 Windows 工作区执行，后续发布门禁仍应在 Linux 原生环境重复全仓测试。

## 5. 工作树边界

RPC 实施阶段只修改 RPC、rpcgen、正式生成物、公共契约/真实 TCP 测试和 v3.3 文档。
开始前已有的 `go.mod`、Kafka Module 与 Service 测试变更当时保持隔离；用户随后要求把
当前全部修改作为 v3.3 RC 统一审查和提交，其复核、修正与最终门禁记录见
[v3.3 RC 发布审查报告](v3.3%20RC发布审查报告.md)。
