# Origin 第三版 M17 公共服务发现 Provider 与 Origin 内置发现实施计划

> 当前状态：已完成
>
> 创建日期：2026-07-30
>
> 对应设计：[M17 公共服务发现 Provider 与 Origin 内置发现设计](../design/milestones/M17-公共服务发现Provider与Origin内置发现设计.md)

## 1. 目标与边界

M17 在 M14 Directory、M15 RPC Transport 和 M16 完整生命周期之上交付正式服务发现扩展面
及不依赖中间件的 Origin Provider。实现不包含 etcd、Consul、Origin TLS、多发现源合并、
动态插件加载或独立 static Provider；这些边界不得通过临时 API 绕过。

## 2. 分阶段任务

### Task 1：公共 Provider 契约与稳定错误

- [x] 新增 `discovery/provider` 的 Factory、Provider、Context、Config、Host 和 DTO；
- [x] 冻结 `Host.SetTTL`、完整快照、状态上报、严格配置解码和深复制边界；
- [x] 冻结 Node、Service、Label、记录和完整快照容量；
- [x] 增加 5002～5004 稳定错误码与哨兵。

### Task 2：Application 配置与第三方注册

- [x] 实现顶层 `discovery.type + 同名块` 严格联合；
- [x] 实现 Application 私有 `RegisterDiscoveryProvider`，保留 `origin/etcd`；
- [x] 未配置 discovery 时保持本地模式和空远端目录；
- [x] 校验 NodeID、保留 DiscoveryService、共置顺序和 TCP 监听冲突。

### Task 3：每 Node 公共 Provider Runtime

- [x] 每 Node 创建独占 Provider、Host 和状态快照；
- [x] 在业务 OnStart 前完成首次权威同步，在全部 OnStart 后发布；
- [x] 框架统一拥有快照规范化、Directory 差异、Readiness 和旧快照 TTL；
- [x] 实现 Recovering 立即降级、一个 TTL 后清空及恢复后完整对账；
- [x] 正常停止先 Withdraw，再排空业务并关闭 Provider。

### Task 4：Origin Wire 与客户端

- [x] 复用 M5 四字节分帧和 Buffer 所有权；
- [x] 实现 Hello、Full、Upsert、Delete、Publish、Withdraw、Heartbeat 和 Resync；
- [x] 实现 Epoch/Revision、严格解码、规范排序和容量校验；
- [x] 实现单发布请求等待 Ack、私有镜像、心跳、指数退避、抖动和持续恢复。

### Task 5：DiscoveryService

- [x] 实现保留 Service、基础设施 Prepare/Close 和独立控制 Listener；
- [x] 使用单 Actor、32768 命令队列、16384 连接和 M5 每连接 64 帧队列；
- [x] 实现 8192 Node/65536 Service 容量、重复会话保护和幂等更新；
- [x] 使用 generation 最小堆、单系统 Timer 和每轮最多 1024 个有效过期项；
- [x] 实现新 Epoch Warming、完整快照和有序增量。

### Task 6：生命周期、状态与发布边界

- [x] Provider 在 RPC 基础设施后、业务 OnStart 前启动；
- [x] DiscoveryService 可与任意业务 Service 共置，但不进入业务发布记录；
- [x] 私有或零公开 Service Node 同步但不发布空记录；
- [x] Transport 整体失效立即撤销，恢复后按最新完整状态重新发布；
- [x] 暴露无锁 `Node.DiscoveryStatus()` 并接入 `HealthStatus`。

### Task 7：测试与契约夹具

- [x] 新增 `discovery/providertest` 公共一致性套件；
- [x] 覆盖 Config/Host/DTO、Wire round-trip、严格尾随、Fuzz 种子和 Benchmark；
- [x] 使用真实 TCP 覆盖 Warming、同步、发布、幂等、冲突、撤销和接管；
- [x] 使用最小 Consul 风格第三方 Provider 证明公共 SPI 不泄漏内部类型；
- [x] 覆盖公共 TTL 到期、保留 Service 不发布和错误码稳定性。

### Task 8：质量门禁、文档回写与提交

- [x] `gofmt`、`go vet ./...`、`go test ./... -count=1`；
- [x] `go test -race ./... -count=1`；
- [x] Origin Fuzz、Benchmark、Windows 构建及 Linux/Darwin 交叉构建；
- [x] 检查 `git diff --check`、Markdown 链接和工作树范围；
- [x] 回写设计状态、复核清单、路线图、索引和本计划；
- [x] 提交 `feat: 完成 M17 公共服务发现与 Origin Provider`。

## 3. 完成底线

M17 只有在公共 SPI 足以直接新增 M18 etcd Provider、不修改 Node/Directory/RPC/业务 API，
并且 Origin 的同步、发布、恢复、容量、停止和资源所有权通过全部门禁后才算完成。

## 4. 验收记录

2026-07-30 已完成：

- Windows 全量单测与全量 Race；
- Origin Wire Fuzz 3 秒共执行 384611 次，未发现失败；
- `BenchmarkEncodeOriginNode`：7197 ns/op、8008 B/op、13 allocs/op；
- Windows 原生构建及 `CGO_ENABLED=0` 的 Linux/Darwin amd64 交叉构建；
- `go vet ./...`、`git diff --check`、配置/生命周期/真实 TCP/公共 Provider 契约回归。

当前 Windows 主机没有可用 WSL 或 Docker Linux Runtime，因此 Linux 侧执行验证以交叉构建
完成；代码未引入平台专属实现。
