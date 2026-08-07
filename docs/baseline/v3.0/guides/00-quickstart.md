# 00：快速入口

## 先运行一个 Application

无需配置 NATS、etcd 或 Docker。在仓库根目录执行：

```text
REM 从仓库根目录启动最小示例。
examples\00-quickstart\01-hello-service\run.bat
```

Linux：

```bash
# Linux/macOS：执行同一示例的启动脚本。
./examples/00-quickstart/01-hello-service/run.sh
```

完整源码：[examples/00-quickstart/01-hello-service](../../../../examples/00-quickstart/01-hello-service)。

你会看到 `HelloService` 的初始化、启动和停止日志。按 `Ctrl+C` 后观察 `OnStop` 输出。

最小 Service 只需匿名嵌入 `service.Service`：

```go
type HelloService struct {
    // 嵌入框架 Service，获得日志与生命周期能力。
    service.Service
}

func (s *HelloService) OnStart(context.Context) error {
    // Service 已进入 Running 前的启动阶段，记录一条业务日志。
    s.Logger().Info("hello, Origin v3")
    return nil
}
```

## 接下来学什么

不要立即接入 RPC 或服务发现。下一章先自己创建 Application、Node 和 Service，理解日志中的对象来自哪里。

## 深入一点

`app.Setup(&HelloService{})` 登记的是零值类型模板；框架会按配置为每个 Node 创建独立实例。`app.Start()` 统一处理命令行、配置、Node 生命周期和优雅停止。
