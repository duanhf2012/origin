# Origin 内置发现

两个 Node 使用内置 Origin Provider 发布并互相发现。`DiscoveryService` 与普通 `Service` 配置在同一 `discovery-1` Node，证明它不要求独占 Node。

```text
run.bat
```

端口 `18090` 必须未被占用。预期看到对方 Node 的 `discovered` 日志。
