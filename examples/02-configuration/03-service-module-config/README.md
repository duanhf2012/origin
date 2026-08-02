# Module 读取 Service 配置

Module 不拥有独立配置树。它通过 `ParseServiceConfig`、`GetServiceConfig` 或 `GetConfig` 读取所属 Service 的同一份冻结配置。

```text
run.bat
```

预期日志：`module reads region="cn-east"`。
