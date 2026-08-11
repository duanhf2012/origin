# Redis Module 纵向切片变更记录

> 目标版本：Origin v3.2
> 状态：已完成

## 实现内容

- 新增 `sysmodule/redismodule`，支持 Standalone、Sentinel、Cluster、RESP2/3、TLS/CA、ACL 和有界连接池。
- 一个 Module 固定拥有一个逻辑 Redis 部署；启动在 Ping 成功后原子发布 Client，失败清理，停止幂等。
- 新增 Key、String、Hash、List、Set、整数 Sorted Set 和 Bitmap 高频便利层。
- 新增 `Client`、`WithClient`、`Do`、Pipeline、TxPipeline、Watch、Lua 组合入口。
- 新增不泄漏第三方类型的 `TryLock`、`Lock`、`WithLock` 和可显式刷新 Lease；不创建自动续租 goroutine。
- 多 Key API 在 Cluster 中校验 Hash Slot；普通 Pipeline 允许 Driver 按节点拆批。
- Sorted Set 公共分数统一为 `int64`，Lua 在 Redis 内完成十进制整数加减，避免 Go/Lua 双精度在 `2^53` 边界丢失。
- 新增参数、配置、生命周期、协议行为、并发、错误分支、真实拓扑、池耗尽、TLS/ACL 和 Sentinel Failover 测试。
- 新增可编译 GoDoc Example、性能 Benchmark 和四组可运行游戏场景 Example。
- 新增完整使用指南，并更新根 README、v3.2 和示例索引。

## Review 中的修正

1. 实际 Redis 测试发现 Lua 数值运算无法安全覆盖 `2^53-1` 边界，改为字符串十进制加减并补回归测试。
2. Hook 增加 typed nil 拒绝，防止接口值非 nil、底层指针为 nil 的运行时故障。
3. 地址校验从非空扩展为严格 `host:port` 和端口范围校验。
4. List 批量数量在转成平台 `int` 前增加溢出校验。
5. 设计回查明确：通用 TxPipeline 无法安全推断每种官方命令的 Key 位置，CROSSSLOT 由 Redis/Driver 返回；Watch 根据显式 Key 提前拒绝。
6. 性能基准夹具最初包含协议不会返回的类型，保留实现的严格类型校验并修正夹具。
7. 最终并发审查发现启动探活与停止之间存在竞态；增加启动取消和转换完成信号，停止会等待清理或遵守自身 Context，禁止停止后再发布 Client。
8. `dial_attempts` 保持“包含首次”的使用者语义；复核 go-redis v9.22.0 连接池循环后确认其 `DialerRetries` 实际也是总尝试数，采用同值映射并补 `1/5/9` 边界测试。
9. 停止关闭改为只执行一次的清理转换；首个和并发调用者都按自己的 Context 等待，清理不因调用者超时而中止，且所有同时等待者收到同一个关闭错误。

## 明确未加入

- v2 兼容 API、动态 Client Registry、缓存框架、Repository、JSON/PB 自动序列化；
- 复合排行、可靠队列、业务重试、Key 规范和缓存回源；
- Redlock、自动续租、后台任务或 Module 自建连接池/对象池。

这些能力缺少跨业务一致语义，加入公共层会扩大误用面。业务可通过官方 Client、Lua 和自己的业务 Module 组合实现。
