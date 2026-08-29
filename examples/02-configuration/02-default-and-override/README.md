# 默认值与 Node 专属覆盖

`ConfigService` 先在 Go 中设定业务默认值，再调用 `ParseServiceConfig` 解析“有效配置”。有效配置优先使用当前 Node 的 `node_services.<node>.<Service>`；它存在时整体替换公共 `services.<Service>`，不会逐字段合并。

## 配置对照

```yaml
services:
  # 当 Node 没有专属块时使用的公共业务配置。
  ConfigService:
    welcome: hello-from-common
    max_players: 100

node_services:
  # game-1 的完整专属配置，会整体替换公共 ConfigService 块。
  game-1:
    ConfigService:
      # 未写 max_players，保留 Go 结构体默认值 10。
      welcome: hello-from-game-1
```

`game-1` 的专属块存在，因此公共块不参与合并。专属块缺少 `max_players`，于是保留 `main.go` 中 `serviceConfig{MaxPlayers: 10}` 的 Go 默认值，而不是得到公共值 `100`。

`main.go` 中的 `Welcome` 和 `MaxPlayers` 分别声明 `json:"welcome"` 与
`json:"max_players"`，因此 JSON/YAML 都使用这里展示的小写字段名。若不声明 Tag，配置必须
使用完整 Go 字段原名；Origin 不会把 `MaxPlayers` 自动转换成 `max_players`。

## 运行与练习

执行 `run.bat` 或 `./run.sh`，预期日志为 `welcome="hello-from-game-1" max_players=10`。删除 `node_services` 块后会看到公共值；在专属块补上 `max_players: 50` 后会看到 `50`。

对应教程：[配置应用](../../../docs/baseline/v3.0/guides/02.configuration.md)。
