# Service Retire 与 Resume

示例在启动后退休当前 Service，再恢复为 Running。Retire 不调用 `OnStop`，只是改变路由与发现状态。

```text
run.bat
```

预期依次看到 `retired`、`running` 状态。
