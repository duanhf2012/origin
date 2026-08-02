# 路由与广播

当一个 Service 有多个实例时，客户端可按业务键稳定路由、显式轮询、随机或自定义 Selector 选择；当必须通知全部实例时，可使用生成的 `Broadcast...` 方法。

## 关键代码

`Route(playerID)` 把同一业务键稳定映射到一个候选实例；`RouteRoundRobin` 用于轮询；`RouteRandom` 使用低竞争随机选择；`RouteBy(firstCandidateSelector{})` 展示同步、快速、无状态的自定义选择器；`BroadcastRefresh` 向全部候选提交通知。

- [`../../_support/tutorialrpc/player_service.go`](../../_support/tutorialrpc/player_service.go)：多实例共用的 RPC 契约。
- [`../../_support/tutorialrpc/player_service.rpc.gen.go`](../../_support/tutorialrpc/player_service.rpc.gen.go)：生成路由客户端、Dispatcher 和冷启动描述符。
- [`player_service.go`](player_service.go)：`player-1`、`player-2` 共用的业务模板实现，不生成适配文件。
- [`config/application.yaml`](config/application.yaml)：同一 `PlayerService` 模板部署到两个 Node。

两个实例都按模板名 `PlayerService` 关联同一契约，候选选择仍使用发现目录中的 NodeID 和
实际 ServiceName，不在调用时重复做接口识别。

## 运行与观察

执行 `run.bat` 或 `./run.sh`。示例启动多个 Player 实例，并记录路由结果与广播的成功/失败统计。部分广播失败不会被吞掉，应检查 `*rpc.BroadcastError` 的成功数和失败明细。

## 可修改实验

重复使用同一 player ID，观察稳定路由；更换 ID，观察可能选择另一实例。不要对需要严格单目标语义的业务使用广播。

对应教程：[跨节点 RPC](../../../docs/baseline/v3.0/guides/06-remote-rpc.md)。
