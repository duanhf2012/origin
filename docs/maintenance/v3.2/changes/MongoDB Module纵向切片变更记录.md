# MongoDB Module 纵向切片变更记录

> 日期：2026-08-11
> 状态：实现与 Windows/Ubuntu 验收完成

本切片按已确认的核心设计实现 MongoDB 生命周期、薄便利层、教程和完整游戏场景 Example，没有复制 v2 Session 外观，也没有建立 ORM、Repository 或一套重复 CRUD API。

主要变更：

- 新增 `sysmodule/mongodbmodule`，一个 Module 独占一个官方 Client 和一个默认 Database；多集群通过多个命名 Module 组合；
- `New` 与 `Setup` 共享配置冻结逻辑，OnStart 创建 Client 并 Ping Primary，失败时 Disconnect 回滚，OnStop 幂等清理；
- 配置只保留 URI、默认 Database 和可选 TLS CA 文件；连接池、超时、认证、Replica Set 和重试参数统一由 URI 表达；
- 拒绝重复 URI/Hosts/TLS 来源、无效 PEM、`InsecureSkipVerify` 及 URI 中允许无效证书/主机名的选项；配置错误不回显 URI 或凭证；
- `WithDriverOptions` 只服务于官方高级能力，普通值、切片、认证 Map 与 BSON 配置形成快照；Monitor、Registry、HTTPClient 等官方高级对象保留有说明的共享引用；
- 公开 `Client`、`Database`、`Collection` 和 `Ping`，普通 CRUD 直接使用官方 Driver；调用方不得关闭借用的 Client；
- 新增普通、唯一、TTL 和顺序批量索引便利方法；唯一与 TTL 不变量最后强制应用，批量失败返回部分成功名称；
- 新增 `WithSession` 和 `WithTransaction`，保证 Session 释放并保留 Driver 事务重试语义；GoDoc 明确事务回调必须幂等且禁止外部副作用；
- 新增包内单元测试、真实 Replica Set 集成测试及 GoDoc Example，覆盖配置、TLS、生命周期、失败回滚、索引、Session、事务、取消与并发唯一键；
- 新增 `examples/15-mongodb/01-game-store`，集中在 `GameMongoModule` 中演示两类 Upsert、条件扣款、乐观锁、稳定多行查询、BulkWrite、幂等奖励、事务转账和安全删除；
- 新增 MongoDB Module 使用指南，并同步根 README、v3.2 文档入口与 Example 索引。

依赖记录：

| 依赖 | 版本 | 许可证 | 用途 |
| --- | --- | --- | --- |
| `go.mongodb.org/mongo-driver/v2` | `v2.8.0` | Apache-2.0 | 官方 MongoDB Client、BSON、索引、Session 与事务 |

性能 Review 的结论是继续使用官方 Client 连接池与 BSON 实现。Module 不缓存轻量 Collection Handle，不增加 CRUD 对象池、后台 Ping、自有重连或业务重试；当前没有 Profile/Benchmark 证据支持扩大优化范围。
