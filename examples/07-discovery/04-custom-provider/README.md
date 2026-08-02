# 自定义 Provider SPI

本示例不连接真实 Consul，而是展示替换 Provider 所需的最小接口：Factory 解码配置，Provider 实现 `Start`、`Publish`、`Withdraw`、`Close`，通过受限 `Host` 提交 TTL、快照和健康状态。

```text
run.bat
```

真实 Consul Provider 应放在独立包中，使用同一个 `app.RegisterDiscoveryProvider("consul", factory)` 注册点；不需要依赖 Origin 的内部目录或 RPC 实现。
