# RPC 超时练习

这个示例运行真实生成 RPC 的超时集成测试，验证调用方 Deadline、远端未完成调用以及 Async 回调“恰好一次”的边界，而不是模拟一个字符串错误。

## 运行

执行 `run.bat` 或 `./run.sh`，它会运行带 `-v` 的指定集成测试。通过表示超时行为符合契约；这不是故意失败的示例。

## 业务处理方式

```go
if errs.CodeOf(err) == errs.CodeDeadlineExceeded {
    // 只对稳定超时码执行降级、有限重试或告警。
}
```

超时不等同于目标一定没有执行，因此写操作应设计幂等键或可查询结果，不能盲目无限重试。

对应教程：[故障排查](../../../docs/baseline/v3.0/guides/12.troubleshooting.md)。
