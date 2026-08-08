# Application、Node 和 Service

此示例在同一进程内创建 `gateway-1` 与 `game-1` 两个 Node。它说明 `Application` 管理整体生命周期，Node 承载 Service 实例，而同一个 Service Go 类型可以在不同 Node 上创建不同实例。

## 关键文件

- `main.go`：登记两个 Service 类型并记录启动、停止日志。
- `config/application.yaml`：定义两个 Node 及各自装载的 Service。

## 运行

执行 `run.bat` 或 `./run.sh`，也可使用：

```bash
# 同时启动 YAML 中声明的 gateway-1 和 game-1。
go run ./examples/01-first-application/01-application-node-service start \
  --app-name first-application \
  --config ./examples/01-first-application/01-application-node-service/config \
  --node gateway-1,game-1
```

## 观察与练习

日志会先显示 `gateway-1`，再显示 `game-1`；停止时顺序相反。尝试交换命令中 Node ID 的顺序，或将一个 Service 加到另一个 Node，观察实例归属和生命周期顺序的变化。

对应教程：[创建第一个应用](../../../docs/baseline/v3.0/guides/01.first-application.md)。
