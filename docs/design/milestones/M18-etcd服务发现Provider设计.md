# Origin 第三版 M18 etcd 服务发现 Provider 设计

> 文档类型：里程碑设计
> 创建日期：2026-07-30
> 最后更新：2026-07-30
> 当前状态：最终 Review 已通过，允许实施

## 1. 里程碑目标

M18 在 M17 已冻结的公共 Provider SPI 之上增加生产级 etcd 服务发现：

1. 每个 Node 使用一个独占 etcd Client 连接一个逻辑 etcd 集群；
2. 通过 Lease 和 Txn/CAS 原子维护本 Node 的发布记录与 Session 所有权；
3. 通过线性化 Range、确定 revision 和后续 Watch 建立完整权威镜像；
4. 支持最多 64 个网络范围、多 Endpoint、认证、TLS、Watch 恢复和 Compaction 重建；
5. 保持 etcd revision、LeaseID、认证信息和恢复细节只存在于 Provider 内部；
6. 复用 M17 的快照校验、状态、Readiness、一个 TTL 旧快照保留和业务事件语义；
7. 通过真实 etcd 集群、故障、权限、TLS、容量、Race 和性能验收。

etcd 的配置、Key、记录、Lease、Watch、恢复与安全细节统一定义在
[服务发现提供者设计](../details/2026-07-26-服务发现提供者设计.md)第 6 章。本文只冻结
M18 的范围、依赖和开工门禁，不建立第二套后端协议正文。

## 2. 已确认的里程碑边界

### 2.1 M18 实现

- 固定依赖 `go.etcd.io/etcd/client/v3 v3.7.1`，生产代码不依赖 etcd Server 包；
- 兼容并集成验证 etcd Server `3.6.14+` 与 `3.7.x`；
- 一个 Provider 连接一个逻辑集群，复用多个 Endpoint，不聚合独立集群；
- `local_network` 必填，读取本地网络与 `watch_networks` 的并集，最多 64 个网络；
- 确定性 Protobuf `EtcdNodeRecordV1` 与 256 KiB 单记录上限；
- 基于 `Version`、`ModRevision` 和 SessionID 的 Txn/CAS 发布、更新与撤销；
- 每个正在发布的 Node 一个惰性 Lease，以及 KeepAlive 失败后的重新授予和重发；
- 线性化分页 Range、固定 revision、逐网络 Watch、进度检查和 Compaction 重建；
- 多 Endpoint 校验与 ClusterID 一致性保护；
- 无认证、用户名/密码、Token、自定义 CA、mTLS 和 ServerName；
- M17 公共 Provider 一致性测试以及 etcd 专属集成、故障和资源测试。

### 2.2 M18 不实现

- 不修改 M17 的 Factory、Provider、Context、Host、DTO、状态或错误码；
- 不修改 Node、Directory、RPC、路由、Await 或业务发现 API；
- 不实现 Consul Provider、多后端热切换或多个独立 etcd 集群聚合；
- 不自动创建 etcd 用户、角色、证书或集群；
- 不开放 gRPC KeepAlive、Endpoint AutoSync、DialOption、Watch progress、分页和消息大小等
  etcd Client 高级调优字段；
- 不实现 TLS 文件监听热更新；Client 后续重建时重新读取文件；
- 不允许用户配置任意 Key 模板或 Value Codec。

## 3. 前置依赖与复用边界

M18 必须直接复用：

- M17 的公共 Provider SPI、严格配置联合、完整快照 Host、状态机、旧快照 TTL 和
  `discovery/providertest`；
- M14 的 Directory、稳定 Diff、关注筛选、业务事件和 TCP 路由；
- M16 的基础设施持续恢复、HealthStatus、启动 Context、Stop Context 和资源反序释放；
- M3 的 JSON/YAML 严格解码、时间字段、环境变量值替换和 TLS 相对路径解析；
- M0/M1 的稳定错误码、结构化日志和秘密信息保护；
- 已固定的 `google.golang.org/protobuf` 生成与运行时能力。

M18 只能新增 etcd Provider 包、内部 Protobuf 记录、配置映射和测试夹具。若实现中发现必须
修改公共契约，必须停止实施并重新 Review M17，不能在 etcd 适配器中建立旁路。

## 4. 最终 Review 结论

开发者于 2026-07-30 确认：

- 第一轮第 1、2、4～18 项采用推荐方案；
- 有效网络上限由建议的 32 调整为 64；
- 第 3 项最终采用  
  `${namespace}/v1/networks/<network>/nodes/<nodeID>`；
- `namespace` 改为可选，默认 `/origin`，共享集群时允许显式覆盖。

保留 Network 层。`namespace` 表示部署或租户发现域，Network 表示同一发现域内的可见
网络。前缀分层使 Provider 只读取和 Watch 选中的网络，并能把写权限限制到
`local_network`；只把 Network 放入 Value 会要求所有 Node 读取整个 namespace、接收无关
网络变化，并扩大读取权限。

最终 Review 同时补齐 M17 `Host.SetTTL`，让 Origin、etcd 和第三方 Provider 都能以一个
最小入口把 TTL 交给框架公共过期状态机，不在 Node 层增加后端判断。未发现需要修改 Node
业务 API、Directory、RPC 或公共发现查询的冲突，允许在 M17 提交后实施 M18。
