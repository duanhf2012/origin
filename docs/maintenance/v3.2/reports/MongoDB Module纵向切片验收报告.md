# MongoDB Module 纵向切片验收报告

> 日期：2026-08-11
> Windows：Go 1.26.5，windows/amd64
> Ubuntu：Go 1.26.5，linux/amd64，Linux 7.0.0-28-generic，Docker 29.6.2
> MongoDB：`mongo:8` 单节点 Replica Set `rs0`
> 结论：MongoDB Module、教程与完整游戏场景 Example 在当前验证范围内通过

## 已验证能力

- URI、Database、TLS CA、封闭 Option 和 Driver Options 的校验、归一化、合并顺序与输入快照；
- URI/PEM/TLS 错误不回显凭证，拒绝多 TLS 材料来源与跳过证书/主机名校验；
- Connect、Primary Ping、启动失败 Disconnect 回滚、运行 Handle 发布、停止清理和重复停止；
- 普通、唯一、TTL 和顺序批量索引，包含强制最终选项、非法边界和部分成功结果；
- 官方 Collection CRUD、条件原子更新、并发唯一键、Session、事务、Context 取消和停止；
- 完整 Example 的配置加载、启动索引、两类 Upsert、条件扣金币、乐观锁、BulkWrite、幂等奖励、事务转账和有界多行查询；
- 普通 Driver、Module 便利层和 Origin Await 三层教程，以及全部公开函数和回调的 goroutine 说明。

## Windows 结果

以下全仓与定向门禁通过：

```text
go test ./... -count=1 -timeout=300s
go vet ./...
go run ./cmd/origingen rpc --check ./...
go build ./...
go test -race ./sysmodule/mongodbmodule
go test ./examples/15-mongodb/...
go vet ./sysmodule/mongodbmodule ./examples/15-mongodb/...
git diff --check
```

不依赖外部 MongoDB 的单元测试覆盖率为 `85.4%`。重点公开方法中 `New`、`EnsureIndex`、`EnsureUniqueIndex`、`EnsureTTLIndex`、`Client`、`Database`、`Collection`、`Ping`、`WithSession` 及主要 OnStart 路径达到 100%；未覆盖主体是必须使用真实服务端的 Driver Runtime，以及只执行机械字段复制的输入快照分支。

## Ubuntu 结果

保留原有带认证 standalone 容器 `origin-mongo` 不变；新增隔离容器：

```text
名称：origin-mongodb-rs
镜像：mongo:8
拓扑：单节点 Replica Set rs0
端口：仅 Ubuntu 回环 127.0.0.1:27018
重启策略：unless-stopped
状态：running / PRIMARY
```

以下全仓与真实 MongoDB 定向门禁通过：

```text
ORIGIN_MONGODB_TEST_URI='mongodb://127.0.0.1:27018/?replicaSet=rs0&directConnection=true' \
  go test -race -count=1 ./sysmodule/mongodbmodule
go test ./... -count=1 -timeout=300s
go vet ./...
go run ./cmd/origingen rpc --check ./...
go build ./...
go test -count=1 ./examples/15-mongodb/...
go vet ./sysmodule/mongodbmodule ./examples/15-mongodb/...
```

启用真实 Replica Set 测试后的语句覆盖率为 `90.3%`。真实测试包含：索引创建、CRUD、八个 goroutine 争抢唯一键、Session、跨文档事务、取消 Ping 和 Disconnect。

完整 Example 实际启动并得到：

```text
MongoDB demo completed: players=2
```

随后使用 SIGINT 进入 Origin 停止流程。测试完成后未删除 `origin-mongodb-rs`，便于后续复用。

## 设计与代码 Review

Review 对照核心设计逐项核对公共签名、配置来源、TLS 冲突、生命周期、Handle 所有权、索引不变量、事务重入、协程位置、教程和 Example。发现并修正两项实现期问题：

1. 归一化配置最初只用于构造 ClientOptions，默认 Database 仍可能保留首尾空格；现已在冻结前统一归一化并增加回归测试。
2. 官方 `MergeClientOptions` 对多数标量使用指针；现已为普通字段、切片、认证 Map 和 BSON 配置建立独立快照，防止调用方在 `New/Setup` 后修改输入污染 Module。

当前没有引入兼容层、动态 Client Map、CRUD 转发方法、自有 goroutine、无界队列、对象池或未证实的性能优化。

## 剩余边界

- AWS DocumentDB、Atlas、Cosmos DB 等服务没有本次可用的真实账号，因此教程只给连接结构和能力边界，不声称完成服务商兼容认证；
- TLS 单元测试覆盖有效/无效 PEM、冲突和安全拒绝，但未建立真实双向 TLS MongoDB 集群；
- `x509.SystemCertPool` 的操作系统失败分支无法稳定注入，已保留创建空 Pool 再追加私有 CA 的确定性回退；
- 覆盖率与当前测试通过只表示已验证范围内没有已知缺陷，不代表数学意义上的绝对无 Bug。

## 发布候选复验补充

2026-08-12 发布候选教程实跑发现：重复执行游戏存储 Example 时，幂等奖励的重复键错误曾在事务回调内
被映射成 `nil`，导致已中止事务继续提交并耗尽默认 Await Deadline。现已改为先退出并结束事务，再在事务
外映射为幂等成功；Example 同时显式使用 60 秒完整演示预算并补充步骤错误上下文。

新增真实 Replica Set 回归测试连续执行两次，验证同一奖励只增加一次金币；完整 Example 也连续运行两次
并输出 `MongoDB demo completed: players=2`。修复后的 Ubuntu 全仓 Test/Race/Vet/Build 与 Windows 全仓
Test/Vet/Build 均通过。
