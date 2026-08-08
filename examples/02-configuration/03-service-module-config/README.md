# Module 读取 Service 配置

Module 没有独立配置树：它属于某个 Service，因此读取该 Service 已冻结的有效配置。这避免同一业务参数在 Service 与 Module 之间出现两个来源。示例对比 `ParseServiceConfig`、`GetServiceConfig` 和 `GetConfig`。

## 配置与代码对应

```yaml
services:
  # ConfigService 与其内部 Module 共享的有效业务配置。
  ConfigService:
    # Module 可通过所属 Service 按路径读取此字段。
    region: cn-east
```

`ParseServiceConfig` 解析完整有效业务配置；`GetServiceConfig("region", ...)` 从同一业务配置按相对路径读取单字段；如果 YAML 是 `limits: {max_players: 100}`，对应路径就是 `GetServiceConfig("limits.max_players", ...)`。这里的路径从当前 Service 配置块根部开始，不需要写 `services.ConfigService` 或 Node ID；`GetConfig("nodes", ...)` 才是从 Application 根配置读取显式路径。Module 不需要也不应在 YAML 中另写名字。

## 运行与练习

执行 `run.bat` 或 `./run.sh`，预期输出 `parsed_region="cn-east" path_region="cn-east" first_node="game-1"`。将 `region` 或 Node ID 改为另一值，可验证三种读取方式的路径范围。

对应教程：[配置应用](../../../docs/baseline/v3.0/guides/02-configuration.md)。
