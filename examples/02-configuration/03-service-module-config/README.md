# Module 读取 Service 配置

Module 没有独立配置树：它属于某个 Service，因此读取该 Service 已冻结的有效配置。这避免同一业务参数在 Service 与 Module 之间出现两个来源。示例对比 `ParseServiceConfig`、`GetServiceConfig` 和 `GetConfig`。

## 配置与代码对应

```yaml
services:
  ConfigService:
    region: cn-east
```

`ParseServiceConfig` 解析完整有效业务配置；`GetServiceConfig("region", ...)` 从同一业务配置按相对路径读取单字段；`GetConfig("nodes", ...)` 从 Application 根配置读取显式路径。Module 不需要也不应在 YAML 中另写名字。

## 运行与练习

执行 `run.bat` 或 `./run.sh`，预期输出 `parsed_region="cn-east" path_region="cn-east" first_node="game-1"`。将 `region` 或 Node ID 改为另一值，可验证三种读取方式的路径范围。

对应教程：[配置应用](../../../docs/baseline/v3.0/guides/02-configuration.md)。
