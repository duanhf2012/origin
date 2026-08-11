// Package mongodbmodule 提供归属于 Origin Service 的 MongoDB Client 生命周期与高频便利外观。
//
// 一个 Module 只拥有一个 MongoDB Client 和一个默认 Database。业务需要访问多个集群时，
// 应分别创建并组合多个 Module；普通 CRUD 直接通过 Collection 使用官方 Driver，避免 Origin
// 重复包装并限制 Driver 能力。
package mongodbmodule
