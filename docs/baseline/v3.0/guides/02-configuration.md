# 02：配置应用

## 我想先使用最小 YAML

每个 Application 都需要 `nodes`。最小写法如下：

```yaml
nodes:
  - id: game-1
    services: [ConfigService]
```

可运行完整示例：[examples/02-configuration/01-minimal-yaml](../../../../examples/02-configuration/01-minimal-yaml)。

```text
examples\02-configuration\01-minimal-yaml\run.bat
```

## 我想为 Service 设置业务参数

业务公共配置放在 `services.<实际ServiceName>`，Node 专属配置放在 `node_services.<NodeID>.<实际ServiceName>`：

```yaml
services:
  ConfigService:
    welcome: hello
    max_players: 100

node_services:
  game-1:
    ConfigService:
      welcome: hello-game-1
      max_players: 50
```

运行：[examples/02-configuration/02-default-and-override](../../../../examples/02-configuration/02-default-and-override)。它展示 Node 专属配置整体替换公共 Service 配置，以及 Go 结构体预设默认值如何保留。

## 我想在 Module 中读取相同配置

运行：[examples/02-configuration/03-service-module-config](../../../../examples/02-configuration/03-service-module-config)。Module 通过所属 Service 使用同一份有效配置。

## 深入一点：默认值与覆盖

框架字段缺失时使用框架默认值；业务结构体字段缺失时保留调用者预先写入的 Go 默认值。`node_services` 存在时不会递归合并 `services`，它表示该 Node/Service 的完整有效业务配置。

例如业务代码先写入 `serviceConfig{Welcome: "default welcome", MaxPlayers: 10}`，配置如下：

```yaml
services:
  ConfigService:
    welcome: hello-from-common
    max_players: 100

node_services:
  game-1:
    ConfigService:
      welcome: hello-from-game-1
```

`game-1` 的最终值是 `welcome="hello-from-game-1"` 和 `max_players=10`：专属块整体替换公共块，缺失字段保留 Go 初始值，并不会回退读取公共块的 `100`。完整可运行验证见 [默认值与 Node 专属配置](../../../../examples/02-configuration/02-default-and-override/README.md)。

在 `OnInit` 中读取并保存强类型配置，不要把配置读取放在每次业务请求的热路径。
