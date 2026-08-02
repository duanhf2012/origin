# 默认值与 Node 专属配置

`ConfigService` 先在 Go 中设置默认值，再解析有效 Service 配置。这里的 `node_services.game-1.ConfigService` 存在，因此它整体替换公共 `services.ConfigService`；缺失的 `max_players` 保留 Go 默认值 `10`。

```text
run.bat
```

预期日志：`welcome="hello-from-game-1" max_players=10`。
