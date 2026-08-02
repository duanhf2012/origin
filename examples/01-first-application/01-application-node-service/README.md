# Application、Node 和 Service

这个示例在一个 Application 中启动 `gateway-1` 与 `game-1` 两个 Node。每个 Node 创建自己的 Service 实例；停止时 Node 和 Service 按反向顺序停止。

```text
run.bat
```

或：

```bash
./run.sh
```

等价命令：

```bash
go run ./examples/01-first-application/01-application-node-service start --app-name first-application --config ./examples/01-first-application/01-application-node-service/config --node gateway-1,game-1
```

对应教程：[创建第一个应用](../../../docs/baseline/v3.0/guides/01-first-application.md)。
