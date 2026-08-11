# MongoDB 游戏存储

这个示例把 MongoDB 能力组合进 `GameMongoModule`，业务数据访问不会散落到 Service。它完整演示：

- 配置、启动 Ping、停止 Disconnect 与启动阶段索引；
- 官方 `Collection` CRUD、两类 Upsert、条件扣金币、乐观锁；
- 有上限且稳定排序的多行查询、`BulkWrite`；
- 基于唯一奖励 ID 的幂等发奖、事务转账；
- 精确过滤的安全删除方法；
- Service 使用 `Await` 等待阻塞 I/O，期间释放 Service 执行权。

## 前置条件

需要 Go `1.26.5` 和支持事务的 MongoDB Replica Set。默认 URI 是：

```text
mongodb://127.0.0.1:27017/?replicaSet=rs0&directConnection=true
```

使用远程或带认证的实例时，只设置环境变量，不要把凭证写入仓库：

```bash
export ORIGIN_MONGODB_URI='mongodb://user:password@host1,host2/game?replicaSet=rs0&authSource=admin'
```

## 运行

Windows：

```text
examples\15-mongodb\01-game-store\run.bat
```

Linux：

```bash
./examples/15-mongodb/01-game-store/run.sh
```

看到以下日志表示全部场景执行成功：

```text
MongoDB demo completed: players=2
```

程序会继续运行以便观察连接；按 `Ctrl+C` 触发 Origin 优雅停止。

## 三个使用层面

| 层面 | 示例位置 | 何时使用 |
| --- | --- | --- |
| 官方 Driver | `Collection("players").UpdateOne/Find/BulkWrite` | 普通 CRUD 和全部官方高级能力，Origin 不重复包装 |
| Module 便利层 | `EnsureIndex`、`WithTransaction`、`Client` | 生命周期、索引不变量、Session/事务与未包装高级 API |
| Origin Service | `GameStoreService.OnStart` 中的 `Await` | Service 工作协程发起慢 I/O 时释放执行权，避免阻塞后续任务 |

`WithTransaction` 回调可能被 Driver 重试。回调内不要发送 RPC、Kafka、邮件或修改不可回滚的内存状态；需要跨系统一致性时使用 Outbox。完整配置、DocumentDB URI、协程语义和生产注意事项见 [MongoDB Module 使用指南](../../../docs/maintenance/v3.2/guides/MongoDB%20Module使用指南.md)。
