# 故意失败：非法 Node ID

这是一个可控的启动失败练习。`config/application.yaml` 中的 `Game-1` 不满足小写 kebab-case 规则，框架必须在启动前拒绝该配置。

## 运行与恢复

先执行 `run.bat` 或 `./run.sh`，脚本返回非零退出码是预期行为。阅读错误中指出的字段和规则，然后把配置改为：

```yaml
nodes:
  - id: game-1
    services: [ConfigService]
```

再次运行应正常启动。

## 排错原则

不要通过吞掉启动错误继续运行；先修复配置源，再重启。Node ID、实际 Service 名和端口等框架配置错误应在部署阶段尽早暴露。

对应教程：[故障排查](../../../docs/baseline/v3.0/guides/12-troubleshooting.md)。
