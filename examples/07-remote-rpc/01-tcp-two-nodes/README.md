# TCP 跨 Node RPC

同一个 Application 启动 `discovery-1`、`player-1`、`gateway-1` 三个 Node。网关等待目标被发现和 TCP 连接可用后，通过 `OnNode("player-1")` 精确调用远端 `PlayerService`。

## 配置重点

`config/application.yaml` 同时配置 Origin Discovery 和两个 TCP 监听地址。`advertise` 是发布给其他 Node 的可连接地址；本机示例使用回环地址和端口 `18080`、`18101`、`18102`，运行前需确保未被占用。

## 契约与业务实现

- [`../../_support/tutorialrpc/player_service.go`](../../_support/tutorialrpc/player_service.go)：共享契约声明。
- [`../../_support/tutorialrpc/player_service.rpc.gen.go`](../../_support/tutorialrpc/player_service.rpc.gen.go)：生成客户端、静态 Dispatcher 和冷启动描述符。
- [`player_service.go`](player_service.go)：运行在 `player-1` Node 的普通业务 Service，不生成业务侧文件。
- [`main.go`](main.go)：网关使用同一个生成客户端执行跨 Node Await。

Node 根据 Service 模板名 `PlayerService` 自动关联契约。TCP 只改变请求的传输路径，不改变
业务实现识别和静态 Dispatcher。

## 运行与观察

执行 `run.bat` 或 `./run.sh`。预期日志为 `remote TCP result: player-1001`。示例对 `ErrTransportUnavailable` 做有限重试，说明“已经发现实例”与“连接已经可用”是不同状态。

## 可修改实验

把 `OnNode` 去掉可改为从 Running 实例自动选择；修改 `player-1` 为不存在的 Node 则会观察到调用失败。生产环境应把 `listen/advertise` 改为真实内网地址。

对应教程：[跨节点 RPC](../../../docs/baseline/v3.0/guides/07.remote-rpc.md)。
