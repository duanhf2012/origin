# MongoDB Module 纵向切片实施计划

> 状态：已完成
> 基线：Origin v3.0，目标版本：v3.2
> 设计依据：`../design/Origin MongoDB Module核心设计.md`
> Driver：`go.mongodb.org/mongo-driver/v2 v2.8.0`

## 1. 目标与边界

本计划只实施已经确认的 `sysmodule/mongodbmodule`、对应单元/集成测试、一个完整游戏存储 Example、
使用指南和文档入口。MySQL、Redis、Kafka、ORM、Repository、自动迁移、业务重试和后台健康任务不进入
本切片。

实现按“配置与所有权先行、便利层后置、真实环境最后验收”的顺序进行。每一步先增加能够失败的测试，
再补最小实现；公共签名、错误、GoDoc 和 Example 同步完成，避免功能完成后集中补文档。

## 2. 实施步骤

### 2.1 依赖与包骨架

1. 固定官方 Driver v2.8.0 为直接依赖；
2. 建立 `sysmodule/mongodbmodule`；
3. 定义 Config、封闭 Option、Module 状态和最小 Driver Runtime 边界；
4. Runtime 测试替身保持包内私有，不增加公共 ClientFactory。

验收：包可以构建，nil、空 URI/Database、nil Option 和重复 Setup 测试先通过。

### 2.2 URI、TLS 与 Driver Options

1. 从 Config.URI 构造唯一基础 ClientOptions；
2. 验证 URI Scheme、TLS 开关、CA/证书材料和不安全选项；
3. 加载系统 CA 并追加 `TLSCAFile`；
4. 克隆 `WithTLSConfig`；
5. 按设计顺序合并 Driver Options，拒绝二次 URI 和 TLS 来源冲突；
6. 所有配置错误使用脱敏阶段信息，不回显完整 URI。

验收：有效/无效 PEM、URI 凭证脱敏、TLS 冲突、InsecureSkipVerify、Options 合并顺序与输入快照测试通过。

### 2.3 生命周期与 Handle

1. OnInit 只验证冻结配置；
2. OnStart 创建唯一 Client、使用启动 Context Ping，失败逆序 Disconnect；
3. OnStop 使用停止 Context Disconnect 并幂等清理状态；
4. 实现 Client、Database、Collection、Ping；
5. 未运行和停止后的 Handle 返回 nil，I/O 方法返回稳定错误。

验收：成功启动、Ping 失败、Disconnect 失败、重复 Stop、并发读取 Handle 和状态迁移测试通过。

### 2.4 索引便利层

按测试顺序实现：

1. EnsureIndex；
2. EnsureUniqueIndex，并最后强制 Unique；
3. EnsureTTLIndex，验证非负、整秒和范围；
4. EnsureIndexes，按输入顺序 CreateOne，返回部分成功名称。

验收：空参数、nil Option、强制选项不可被覆盖、顺序、部分失败和错误链测试通过。

### 2.5 Session 与事务

1. WithSession 创建真实 Session、使用 Session Context 并保证 EndSession；
2. WithTransaction 复用 Session.WithTransaction，保留 Driver 重试与错误 Label；
3. 回调 nil、Context nil、未运行和 Session 创建错误都可稳定测试；
4. GoDoc 明确回调可能重入、禁止外部副作用和 Service 状态修改。

验收：回调 Context、释放、回调错误、事务结果和 Driver 错误链测试通过。

### 2.6 GoDoc Example 与完整游戏 Example

1. 为所有导出类型、函数、方法、字段补完整中文 GoDoc；
2. 对 EnsureIndex、TTL、WithSession、WithTransaction 提供可编译 Go Example；
3. 建立 `examples/15-mongodb/01-game-store`；
4. 业务 `GameMongoModule` 集中实现索引、CRUD、两类 Upsert、条件扣金币、乐观锁、稳定多行查询、
   BulkWrite、幂等奖励、事务转账和安全删除；
5. Service 只通过业务方法和 Await 调用；
6. README/YAML 说明运行、预期输出、错误分支和清理。

### 2.7 使用指南与入口

新增 `docs/maintenance/v3.2/guides/MongoDB Module使用指南.md`，按以下顺序组织：

1. 十分钟最小接入；
2. 单集群与多集群组合；
3. 标准 MongoDB/Replica Set/Atlas/DocumentDB URI；
4. URI 高频生产参数表；
5. TLS 与安全；
6. Client/Database/Collection 与 Await；
7. 索引、Session、事务、原子更新；
8. 每个接口/回调的 goroutine、参数、返回值和错误；
9. 游戏场景 Example；
10. 连接池、超时、慢查询和故障排查。

同步根 README 和 v3.2 文档索引，不调整基础教程章节。

### 2.8 Windows 与 Ubuntu 验收

Windows：

- 单元测试、GoDoc Example、覆盖率、`go vet`、全仓构建和 Example 编译；
- 不依赖本地 MongoDB 的全部错误与 TLS 测试。

Ubuntu：

- 先只读检查 Docker 与既有 MongoDB；没有可用 Replica Set 时安装隔离的单节点 Replica Set；
- 真实 CRUD、索引、并发唯一键、Session、事务、取消、停止和 Example；
- `go test -race`、覆盖率与 goroutine 泄漏检查。

真实 URI 和凭证只放环境变量。MongoDB Docker 是否保留按现有环境所有权处理：本任务不删除既有环境；
若为本切片新建，验收报告记录状态，不执行破坏性清理。

## 3. 完成门禁

1. 设计冻结的公共 API 全部实现且无额外兼容别名；
2. 重点公开行为和所有稳定错误分支尽量达到 100% 覆盖；
3. Windows 全仓测试/构建与 Ubuntu Replica Set/`-race` 通过；
4. Example 可以独立构建运行并包含失败路径；
5. 指南从使用者角度完整说明三个层面：普通 CRUD、Origin Await、官方 Driver 高级能力；
6. 代码 Review 不存在凭证泄漏、无所有者 goroutine、隐藏 Context 或未解释的低覆盖；
7. 设计、计划、代码、测试、Example、指南、变更记录和验收报告一致；
8. 独立提交 MongoDB 切片后才进入 Redis。
