# 最小 YAML

这是框架配置的起点。除了日志设置外，唯一必需的框架顶层字段是 `nodes`；每个 Node 需要一个合法 ID 和要装载的实际 Service 名。

## 配置

`config/application.yaml` 的有效最小部分如下：

```yaml
nodes:
  - id: game-1
    services: [ConfigService]
```

`ConfigService` 必须是 `main.go` 中已经通过 `app.Setup` 登记的 Go 类型名。

## 运行与练习

执行 `run.bat` 或 `./run.sh`，应看到 `minimal YAML loaded`。可先删除 `log` 块确认其为可选项；不要删除 `nodes` 或写成不存在的 Service 名，它们会使启动失败。

对应教程：[配置应用](../../../docs/baseline/v3.0/guides/02-configuration.md)。
