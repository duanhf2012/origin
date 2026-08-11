# Redis Module 纵向切片实施计划

> 状态：已完成
> 基线：Origin v3.0，目标版本：v3.2
> 设计依据：`../design/Origin Redis Module核心设计.md`
> 兼容性：不保留 Origin v2 Redis API、命名或行为兼容层

## 1. 实施目标

按一个完整纵向切片交付 `sysmodule/redismodule`：配置与生命周期、高频基础命令、官方 Client 组合入口、
Pipeline/事务/Lua、分布式 Lease Lock、单元与真实 Redis 测试、四组游戏场景 Example、使用指南和入口索引。

范围只覆盖已经确认且跨游戏业务稳定复用的 Redis 基础能力。JSON/PB 编解码、Key 规则、缓存回源、
复合排行、可靠队列、业务重试和最终一致性继续由业务 Module 决定。

## 2. 顺序与防遗漏策略

1. **冻结事实**：复核设计、go-redis/redislock 最新稳定版本、Origin Module 生命周期和 v2 能力边界。
2. **先大后小实现**：先配置、拓扑、运行时所有权和启动回滚；再实现普通命令；最后加入组合入口和锁。
3. **同步测试**：每类实现同时补正常、边界、错误、Context、停止、并发和真实协议测试，不在结尾集中补测。
4. **使用者验证**：先写可编译 Go Example，再用四个业务 Module 形式的完整 Example 验证外观是否直接。
5. **教程收口**：按快速开始、业务选择、配置、三种拓扑、三层用法、goroutine、场景、排障和 API 编排。
6. **双平台门禁**：Windows 完成全仓门禁；Ubuntu 完成 Redis 7.2/8.x Standalone、Sentinel、Cluster、竞态、
   覆盖率和 Example 实跑；所有测试环境均保持有界且可清理。
7. **最终 Review**：逐项对照核心设计、公共 GoDoc、教程代码、Example 和真实行为，修正偏差后独立提交。

## 3. 验收重点

- 三种拓扑的唯一配置源、默认值、TLS/ACL、池硬上限和错误脱敏正确；
- 启动只在全部 Ping 成功后发布 Client，失败逆序清理，停止幂等；
- Miss、空字符串、空集合、TTL 特殊值、整数 Score 和 Cluster Slot 边界无歧义；
- Pipeline/事务/Watch/Lua 的原子性和回调重入风险在 GoDoc、教程和示例一致；
- 锁等待有界、无自动续租 goroutine、释放使用有界清理 Context，并保留双错误；
- 所有导出内容具有完整中文 GoDoc，复杂接口有可编译 Example；
- 重点公共行为尽量 100% 覆盖，无法稳定触发的系统或拓扑分支记录真实集成证据和剩余风险。

## 4. 依赖复核

2026-08-11 通过 Go Module 元数据再次确认：

- `github.com/redis/go-redis/v9 v9.22.0`，Go 1.24+；
- `github.com/bsm/redislock v0.10.0`，Go 1.25+；
- Origin 当前使用 Go 1.26.5，满足两项依赖要求。

只把这两项加入直接依赖。Origin 不自建连接池、消息队列、对象池或后台续租器。
