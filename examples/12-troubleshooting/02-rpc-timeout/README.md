# RPC 超时练习

运行真实生成 RPC 的超时集成测试：

```text
run.bat
```

测试验证调用方 Deadline、远端未完成调用和 Async 回调恰好一次的边界。业务处理中应按 `errs.CodeOf(err)` 对超时执行降级、重试或告警。
