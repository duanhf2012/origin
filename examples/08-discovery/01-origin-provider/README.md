# Origin 内置发现

两个 Node 使用内置 Origin Provider 发布并互相发现。`DiscoveryService` 与普通 `Service` 同时配置在 `discovery-1`，说明发现服务不需要独占 Node。

## 配置重点

顶层 `discovery.type: origin` 选择内置 Provider；`origin.server.node` 指出承载 `DiscoveryService` 的 Node，`listen/address` 是客户端连接地址。示例使用端口 `18090`，运行前确保未被占用。

## 运行与观察

执行 `run.bat` 或 `./run.sh`，预期在 `tutorialwatcher` 日志中看到对方 Node 的 `discovered` 事件。停止进程后可以观察失去发现的状态变化。

## 可修改实验

可在 `discovery-1` 的 `services` 增加自己的普通 Service，验证它仍能共存；生产环境应将地址设为受限内网地址，并用网络策略保护发现端口。

对应教程：[服务发现](../../../docs/baseline/v3.0/guides/08.discovery.md)。
