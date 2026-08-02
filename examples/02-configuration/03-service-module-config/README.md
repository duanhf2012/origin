# Module 读取 Service 配置

Module 没有独立配置树：它属于某个 Service，因此读取该 Service 已冻结的有效配置。这避免同一业务参数在 Service 与 Module 之间出现两个来源。

## 配置与代码对应

```yaml
services:
  ConfigService:
    region: cn-east
```

`ConfigService.OnInit` 添加 `ConfigModule`；随后 `ConfigModule.OnInit` 使用 `ParseServiceConfig` 将相同配置解析到 `settings`。Module 不需要也不应在 YAML 中另写名字。

## 运行与练习

执行 `run.bat` 或 `./run.sh`，预期输出 `module reads region="cn-east"`。将 `region` 改为另一值可验证配置只需修改一处；尝试添加未被结构体接收的字段，理解业务配置的解析边界。

对应教程：[配置应用](../../../docs/baseline/v3.0/guides/02-configuration.md)。
