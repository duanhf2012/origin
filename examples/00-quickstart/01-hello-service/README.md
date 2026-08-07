# 第一个 Service

这是最小、无外部依赖的 Origin 应用：一个 `Application`、一个 `hello-1` Node 和一个 `HelloService`。它适合第一次确认 Go 环境、命令行启动、生命周期回调和优雅停止都正常工作。

## 关键文件

- `main.go`：定义 `HelloService`，在 `OnInit`、`OnStart`、`OnStop` 记录不同阶段。
- `config/application.yaml`：将 `HelloService` 配置到 `hello-1`。

## 运行

Windows 执行 `run.bat`；Linux/macOS 执行 `./run.sh`。等价命令：

```bash
# 从仓库根目录启动 hello-1，并读取示例配置目录。
go run ./examples/00-quickstart/01-hello-service start \
  --app-name hello-service \
  --config ./examples/00-quickstart/01-hello-service/config --node hello-1
```

## 观察与练习

依次会看到 `initialized`、`hello, Origin v3`、`stopped`。按 `Ctrl+C` 才会出现最后一条日志，说明停止也经过 Service 生命周期。可把 `HelloService` 改名为自己的业务类型，并同步修改 YAML 的 `services` 项，体会 Go 类型名与配置名必须一致。

对应教程：[快速入口](../../../docs/baseline/v3.0/guides/00-quickstart.md)。
