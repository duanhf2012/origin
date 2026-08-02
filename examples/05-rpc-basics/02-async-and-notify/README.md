# Async 与 Notify

本示例复用 [`examples/_support/tutorialrpc/player.go`](../../_support/tutorialrpc/player.go) 的 `PlayerRPC`：先异步请求 `GetPlayer`，再发送不等待结果的 `Refresh` 通知。

```text
run.bat
```

需要重新生成共享合约时执行：

```text
generate.bat
```

对应教程：[RPC 基础](../../../docs/baseline/v3.0/guides/05-rpc-basics.md)。
