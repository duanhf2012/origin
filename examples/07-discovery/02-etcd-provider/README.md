# etcd Provider

先启动仓库固定版本的 etcd 开发集群，再运行两个 Node。默认 namespace 为 `origin`，这里不需要显式配置。

```text
deps-up.bat
check-deps.bat
run.bat
```

停止依赖使用 `deps-down.bat`；它仅停止容器，不删除 etcd 数据卷。
