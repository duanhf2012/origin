# Origin 第三版 M18 etcd 服务发现 Provider 实施计划

> 当前状态：已完成
>
> 创建日期：2026-07-30
>
> 对应设计：[M18 etcd 服务发现 Provider 设计](../design/milestones/M18-etcd服务发现Provider设计.md)

## 1. 目标与边界

M18 在 M17 已冻结的公共 Provider SPI 上新增生产级 etcd 适配器。实现只接入
Application 私有内置注册表，不修改 Node、Directory、RPC、业务发现 API 或第三方 Provider
契约；Consul、多后端合并、任意 Key 模板和 etcd Client 高级调优仍不进入本里程碑。

## 2. 分阶段任务

### Task 1：严格配置与 Client

- [x] 固定官方 etcd Client v3.7.1，并保持生产代码不导入 Server 包；
- [x] 实现 Endpoint、默认 `/origin` namespace、网络并集及 64 网络上限；
- [x] 实现 TTL、Dial/Request Timeout、用户名密码或 Token 认证；
- [x] 实现系统 CA、自定义 CA、mTLS、ServerName、TLS 1.2 和开发态显式跳过验证；
- [x] TLS 相对路径按配置根目录解析，并在每次 Client 重建时重新读取。

### Task 2：记录、Key 与容量

- [x] 固定 Network 前缀 Key 和 `EtcdNodeRecordV1` Protobuf；
- [x] 实现确定性编码、严格重复字段/排序/枚举/身份校验及未知兼容字段；
- [x] 实现单记录 256 KiB 与公共 Node、Service、快照容量限制；
- [x] Range 累积过程中提前拒绝超过 Node、Service 或快照字节上限的数据。

### Task 3：Lease、Txn/CAS 与 Session 所有权

- [x] 惰性 Grant Node 独占 Lease，并在首个有效 KeepAlive 后发布；
- [x] 使用 Version 与 ModRevision CAS 创建、幂等更新和并发重试；
- [x] 不同 Session 占用相同 NodeID 时稳定返回 DuplicateNode；
- [x] Withdraw 只删除精确 Session，并尽力 Revoke 自有 Lease；
- [x] 恢复时授予新 Lease，并按最新期望重新发布。

### Task 4：Range、Watch 与恢复

- [x] 使用线性化分页 Range，并让全部网络固定在同一 revision；
- [x] 使用严格后继 Key、逐网络 Prefix Watch、RequireLeader、Fragment 和 CreatedNotify；
- [x] 使用显式 progress 屏障闭合 Range/Watch 窗口，并按 revision 原子合并跨网络事务；
- [x] 使用单 owner、有界 Watch 队列、活性检测、ClusterID 保护和带抖动退避；
- [x] Watch 断流、Compaction、Lease 失效或运行期鉴权异常后冻结提交并完整重建。

### Task 5：Application 与公共契约

- [x] 将内置 `etcd` Factory 接入 Application，保持公开 SPI 不变；
- [x] 每个 Node 创建独占 Provider 与官方 Client，一个 Provider 只连接一个逻辑集群；
- [x] 在资源创建前严格校验 etcd 配置，在业务 OnStart 前完成首次权威同步；
- [x] 复用 M17 Host TTL、状态、旧快照过期、发布屏障和反序停止；
- [x] 通过 `discovery/providertest` 公共一致性套件。

### Task 6：集成测试与质量门禁

- [x] 覆盖真实 etcd 分页、多网络 Watch、跨网络事务原子性和非法快照；
- [x] 覆盖 Lease 到期、重复发布、冲突、主动撤销和到期接管；
- [x] 覆盖 Server 重启、恢复期间发布收敛、用户名密码、Token 和 HTTPS；
- [x] 验证 etcd Server 3.6.14+ 与 3.7.x 兼容性；
- [x] 完成全仓单测、Race、Vet、Fuzz、Benchmark 和跨平台构建；
- [x] 回写设计状态、复核清单、路线图、索引和验收记录；
- [x] 提交 `feat: 完成 M18 etcd 服务发现 Provider`。

## 3. 完成底线

M18 只有在 etcd 的同步、发布、恢复、安全、容量和退出场景通过真实 Server 与公共契约测试，
且 M17 的公开 SPI、Node/Directory/RPC/业务 API 均无需修改时才算完成。

## 4. 验收记录

2026-07-30 已完成：

- 真实 etcd Server 3.6.14 独立进程及 3.7.1 嵌入式 Server 兼容验证；
- 分页、多 Endpoint、多网络 Watch、跨网络事务原子性、Lease 到期、Session 冲突/接管、
  Server 重启和恢复期间 Publish 收敛；
- 用户名密码、Token、最小前缀 RBAC、HTTPS 显式开发模式、自定义 CA 与客户端证书加载；
- Windows 全仓单测、全仓 Race、`go vet ./...`；
- `FuzzDecodeEtcdNodeRecord` 5 秒共执行 129512 次，未发现失败；
- `BenchmarkEncodeEtcdNodeRecord`：3785～4160 ns/op、976 B/op、16 allocs/op；
- Windows 原生构建及 `CGO_ENABLED=0` 的 Linux/Darwin amd64 交叉构建；
- `git diff --check`、公共 `providertest` 和 M17 全量回归。

最终实现的生产包只依赖 etcd API/Client；`go.etcd.io/etcd/server/v3` 仅由真实集成测试导入。
M18 没有改变 M17 公共契约，联合 Review 中发现的同 revision Range、Watch progress 屏障和
跨网络事务合并均已实现并验证。
