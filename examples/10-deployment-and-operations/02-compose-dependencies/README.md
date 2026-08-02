# 开发依赖编排

使用仓库固定版本的三节点 etcd 与三节点 NATS 开发集群：

```text
deps-up.bat
check-deps.bat
deps-down.bat
```

`deps-down` 只停止容器，不执行 `down -v`，不会删除 etcd 数据卷。生产部署请使用独立的基础设施配置、TLS 和最小认证。
