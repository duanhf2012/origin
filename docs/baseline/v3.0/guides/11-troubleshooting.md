# 11：故障排查

## 排查顺序

1. 先看启动错误和稳定错误码。
2. 再读取 `Application.Diagnostics()` 或诊断 HTTP 快照。
3. 对 RPC 检查目标 Service、Node、发现状态、Transport 和超时。
4. 对服务发现检查 Provider 连接、TTL、网络、TLS/认证和关注规则。
5. 必要时短时开启 pprof；完成后关闭。

超时应按稳定错误码进入业务降级或重试分支：

```go
if errs.CodeOf(err) == errs.CodeDeadlineExceeded {
    // 只对稳定超时错误执行有限降级、重试或返回可识别的业务错误。
}
```

## 可控故障练习

- [配置错误](../../../../examples/11-troubleshooting/01-config-error)：未知字段和缺失 Node。
- [RPC 超时](../../../../examples/11-troubleshooting/02-rpc-timeout)：调用方 Context 与目标延迟。
- [发现 Lost](../../../../examples/11-troubleshooting/03-discovery-lost)：Provider 断线后的状态事件。
- [诊断收集](../../../../examples/11-troubleshooting/04-diagnostics-collect)：将快照保存为 JSON。

每个练习都会标记“故意失败”或“需要外部依赖”，并给出恢复方式。不要为了通过示例而吞掉超时、错误码或 Lost 事件；它们是业务恢复、告警和降级的输入。
