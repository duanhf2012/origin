# 错误与排错入口

Origin 使用 `errs.Code` 提供稳定错误分类：

```go
if errs.CodeOf(err) == errs.CodeDeadlineExceeded {
    // 超时降级或重试
}
```

常见情况：

| 现象 | 首先检查 |
| --- | --- |
| Application 启动失败 | YAML 字段、Node ID、Service 类型名、外部端口 |
| RPC 超时 | 调用 Context、目标是否 Running、Transport、发现目录 |
| 无候选实例 | ServiceName、`allow_discovery`、Retired 状态、Provider 快照 |
| Provider 不可用 | etcd/NATS 网络、TLS、认证、TTL 和日志 |

详细可复现路径见[故障排查](../12-troubleshooting.md)。
