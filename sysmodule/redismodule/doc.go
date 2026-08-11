// Package redismodule 提供由 Origin Module 管理生命周期的 Redis 基础能力。
//
// 一个 Module 对应一个 Standalone、Sentinel 或 Cluster 逻辑部署。普通游戏业务优先使用
// 高频便利方法；复杂命令通过 Client、WithClient、Pipeline、事务和 Lua 组合。所有网络方法
// 都在调用方 goroutine 同步执行，在 Service 串行协程中应配合 Origin Await 使用。
package redismodule
