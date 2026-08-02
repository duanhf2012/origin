# TCP 跨 Node RPC

该示例在一个进程中启动 Origin Discovery、`player-1` 和 `gateway-1` 三个 Node。网关等待发现和 TCP 连接就绪后，通过 `OnNode("player-1")` 调用 `PlayerService`。

```text
run.bat
```

预期日志：`remote TCP result: player-1001`。

端口 `18080`、`18101`、`18102` 必须未被占用。
