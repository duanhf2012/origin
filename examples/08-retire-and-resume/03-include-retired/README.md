# 显式包含 Retired 实例

默认自动路由只选择 Running 实例。这个示例先退休同 Node 的 `PlayerService`，随后由调用方显式派生 `IncludeRetired()` 客户端进行调用。

```text
run.bat
```

仅在业务确实定义了 Retired 服务仍可处理该操作时使用该选项。
