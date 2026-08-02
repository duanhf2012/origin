# 第一个 Service

这个示例启动一个 Node 和一个 `HelloService`，并在初始化、启动、停止时分别打印日志。

## 运行

Windows：

```text
run.bat
```

Linux：

```bash
./run.sh
```

等价命令：

```bash
go run ./examples/00-quickstart/01-hello-service start --app-name hello-service --config ./examples/00-quickstart/01-hello-service/config --node hello-1
```

按 `Ctrl+C` 停止。预期依次看到 `initialized`、`hello, Origin v3`、`stopped`。

对应教程：[快速入口](../../../docs/baseline/v3.0/guides/00-quickstart.md)。
