# 路由与广播

当一个 Service 有多个实例时，客户端可按业务键稳定路由、显式轮询或随机选择；当必须通知全部实例时，可使用生成的 `Broadcast...` 方法。

## 关键代码

`Route(playerID)` 把同一业务键稳定映射到一个候选实例，适合玩家/房间归属；`RouteRoundRobin` 用于均衡的无状态请求；`BroadcastRefresh` 向所有候选提交通知，并返回聚合错误。

## 运行与观察

执行 `run.bat` 或 `./run.sh`。示例启动多个 Player 实例，并记录路由结果与广播的成功/失败统计。部分广播失败不会被吞掉，应检查 `*rpc.BroadcastError` 的成功数和失败明细。

## 可修改实验

重复使用同一 player ID，观察稳定路由；更换 ID，观察可能选择另一实例。不要对需要严格单目标语义的业务使用广播。

对应教程：[跨节点 RPC](../../../docs/baseline/v3.0/guides/06-remote-rpc.md)。
