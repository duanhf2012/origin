# Redis 缓存与会话

这个示例把 Key、Protobuf 编解码、缓存 Miss 和会话策略集中在 `PlayerCacheModule`，并演示：

- PB 玩家缓存的写入、命中、Miss 和损坏数据；
- `MGet` 的 `OptionalString` 如何区分空字符串与不存在；
- `GetEx` 原子读取并滑动续期登录会话；
- `GetDel` 消费一次性登录 Token；
- `PTTL` 的剩余时间检查；
- Service 用 `Await` 执行阻塞 Redis I/O。

运行前启动 Redis，然后执行 `run.bat`（Windows）或 `./run.sh`（Linux）。远程地址通过
`ORIGIN_REDIS_ADDRESS` 设置，不要把密码写进示例或仓库。看到 `Redis cache/session demo completed`
表示全部成功与失败分支都符合预期。

生产中缓存 Miss 应回源数据库；损坏缓存应记录受控指标并删除或回源，不能把原始玩家数据写入日志。
超时不证明写入一定未发生，重试写操作必须依靠幂等 Key 或最终数据库约束。
