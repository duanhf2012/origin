# 开发依赖编排

此目录只管理本仓库固定版本的三节点 etcd 与三节点 NATS 开发集群，为相关教程提供可重复的本机依赖；它不代表生产基础设施方案。

## 命令顺序

```text
deps-up.bat       # 启动依赖
check-deps.bat    # 检查端口和健康状态
deps-down.bat     # 停止本次依赖
```

Linux/macOS 使用同名 `.sh` 脚本。只有 `check-deps` 成功后再运行 NATS 或 etcd 教程，可以避免把依赖不可用误判为 RPC/发现代码错误。

## 数据与安全边界

`deps-down` 只停止容器，不执行 `down -v`，不会删除 etcd 数据卷。生产请使用独立基础设施配置、TLS、最小认证、备份和容量策略，不能直接复用本地 compose 默认值。

对应教程：[部署与运维](../../../docs/baseline/v3.0/guides/10-deployment-and-operations.md)。
