# Redis 分布式 Lease Lock

示例覆盖缓存重建、匹配结算、跨服排行刷新、长任务显式 Refresh、锁竞争和 Lease 过期。运行成功会输出
`Redis distributed-lock demo completed`。

锁方法都在调用方 goroutine 同步执行；示例由 Service 的 Await Worker 调用。`TryLock` 被占用返回
`acquired=false` 而不是系统错误，`Lock/WithLock` 的等待同时受 `waitTimeout` 和 Context 限制。

Redis Lock 是会过期的 Lease：进程暂停、网络抖动或任务超时都可能使多个执行者先后认为自己持有锁。
金币、背包、奖励、支付和结算必须额外使用数据库唯一键、事务、Lua 原子状态或幂等记录。自动续租未被
隐藏在后台；长任务必须显式 Refresh，并在 Refresh 失败后停止或补偿。
