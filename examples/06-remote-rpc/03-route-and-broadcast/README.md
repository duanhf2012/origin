# 按 Key 路由与广播

两个 `PlayerService` 实例同时发布。`Route(1001)` 对同一业务 Key 做稳定选择；`BroadcastRefresh` 向所有可用实例发送无返回通知。

```text
run.bat
```

如需检查部分失败，使用 `errors.As(err, &rpc.BroadcastError)` 读取失败目标；本示例的正常路径不制造失败。
