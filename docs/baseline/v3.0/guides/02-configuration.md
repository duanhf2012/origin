# 02：配置应用

## 我想先使用最小 YAML

每个 Application 都需要 `nodes`。最小写法如下：

```yaml
nodes:
  # Node 的稳定配置标识。
  - id: game-1
    # 必须是 app.Setup 已登记的实际 Service 名。
    services: [ConfigService]
```

可运行完整示例：[examples/02-configuration/01-minimal-yaml](../../../../examples/02-configuration/01-minimal-yaml)。

```text
# Windows：从仓库根目录使用最小 YAML 启动。
examples\02-configuration\01-minimal-yaml\run.bat
```

## 我想为 Service 设置业务参数

业务公共配置放在 `services.<实际ServiceName>`，Node 专属配置放在
`node_services.<NodeID>.<实际ServiceName>`：

```yaml
services:
  # 所有未提供 Node 专属块的 ConfigService 使用该完整配置。
  ConfigService:
    welcome: hello
    max_players: 100

node_services:
  # game-1 使用这一个完整替代块。
  game-1:
    ConfigService:
      welcome: hello-game-1
      max_players: 50
```

运行：[examples/02-configuration/02-default-and-override](../../../../examples/02-configuration/02-default-and-override)。它展示 Node 专属配置整体替换公共 Service 配置，以及 Go 结构体预设默认值如何保留。

## 我想在 Module 中读取相同配置

运行：[examples/02-configuration/03-service-module-config](../../../../examples/02-configuration/03-service-module-config)。Module 通过所属 Service 使用同一份有效配置：

```go
// 在 Module.OnInit 中解析所属 Service 已选定的完整业务配置块。
if err := module.ParseServiceConfig(&module.config); err != nil {
    return err
}
```

## 我该使用哪一种 Service 配置读取方法

`Service` 与 `Module` 都提供相同的三种只读配置方法。它们读取的是启动时已经合并并冻结的快照，不会触发磁盘 I/O，也不支持运行期热更新：

| 方法 | 读取范围 | 常见用途 |
| --- | --- | --- |
| `ParseServiceConfig(&dst)` | 当前 Node 与当前实际 ServiceName 最终选中的**完整**业务配置 | 在 `OnInit` 一次性解码本 Service 的 settings 结构体，最常用。 |
| `GetServiceConfig("path", &dst)` | 同一份有效业务配置中的相对路径 | 只读取一个小字段或嵌套子块，例如 `limits.max_players`。 |
| `GetConfig("path", &dst)` | Application 的完整根配置中的绝对路径 | 少量需要读取共享框架配置或其他已知配置块的场景。 |

已有完整可运行代码：[Service 与 Module 配置](../../../../examples/02-configuration/03-service-module-config)。它同时演示这三种调用：

```go
func (s *ConfigService) OnInit() error {
    // 1. 最常用：一次解析当前 Service 的完整有效配置。
    if err := s.ParseServiceConfig(&s.settings); err != nil {
        return err
    }

    // 2. 相对当前 Service 配置读取一个字段。
    if err := s.GetServiceConfig("limits.max_players", &s.maxPlayers); err != nil {
        return err
    }

    // 3. 根路径只用于明确需要的跨配置读取，不要把它当作业务 Service 间通信。
    var nodes []struct {
        ID string `json:"id"`
    }
    return s.GetConfig("nodes", &nodes)
}
```

路径使用点号分隔，例如 `limits.max_players`；空路径、空分段、通配符和数组下标不属于这套
稳定 API。没有业务配置时，`ParseServiceConfig` 保留目标结构体预填的默认值；读取不存在路径
则返回错误。推荐只在 `OnInit` 解析并保存强类型结果，避免在 RPC、Timer 和事件热路径重复解码。

## 我想配置控制台和滚动文件日志

运行：[examples/02-configuration/04-log-output-and-rolling](../../../../examples/02-configuration/04-log-output-and-rolling)。常用完整配置为：

```yaml
log:
  # async 不等待普通日志写盘；sync 适合测试和本地即时观察。
  mode: async
  console:
    # 控制台输出 info 及以上的人类可读文本。
    enabled: true
    level: info
    format: text
  file:
    # 文件额外保留 debug，并使用一行一个对象的 JSON Lines。
    enabled: true
    level: debug
    format: json
    path: logs/origin.log
    rotation:
      # 下一条完整日志会使活动文件超过 512 MiB 时先滚动。
      max_size: 512M
      # 跨本地自然日时也滚动；可改为 UTC。
      by_date: true
      timezone: Local
    retention:
      # 删除超过 14 天的归档，并最多保留最新 30 个。
      max_age: 14d
      max_files: 30
      # 归档由一个维护协程压缩为 gzip。
      compress: true
```

未配置 `log` 时的默认值与上面相同，但文件输出默认是 `enabled: false`，控制台默认开启。
`level` 支持 `debug`、`info`、`warn`、`error`；`format` 支持 `text`、`json`。控制台和文件
可以使用不同级别和格式，但不能同时关闭。

`max_size` 使用 Origin 二进制容量单位 `B/KB/M/G/T`，且日志滚动值必须是 1 MiB 的整数倍；
`0B` 关闭大小滚动。`max_age` 使用 `ns/us/ms/s/m/h/d`，日志保留要求整天，`0s` 表示不按
时间删除；`max_files: 0` 表示不按数量删除。相对 `path` 以程序启动工作目录为基准。

`mode: async` 使用固定有界队列，队列满时普通日志按级别累计丢弃而不是无限占内存；该容量
不是业务配置项。`ErrorStack` 会附加有界调用栈并使用可靠写路径，但仍不应替代正常错误返回。
启用文件日志时还会安装同路径派生的 `.crash.log`，用于 Go Runtime 无法恢复的进程崩溃。

业务代码使用类型化字段，保留日志平台可筛选的数值和字符串类型：

```go
// Logger 已自动带 app_name、node_id 和 service_name。
s.Logger().Info("player entered",
    originlog.Int64("player_id", playerID),
    originlog.String("region", region),
)
```

## 我想混用 JSON、YAML 并拆分文件

运行：[examples/02-configuration/05-json-and-split-files](../../../../examples/02-configuration/05-json-and-split-files)。`--config` 接收目录，框架会递归扫描 `.json`、`.yml`、`.yaml`，因此可以一个 Node 一个文件：

```text
config/
  00-log.yaml          # Application 公共配置
  10-service.json      # Service 业务配置
  nodes/
    20-game-1.yaml     # 只声明 game-1
    30-game-2.json     # 只声明 game-2
```

两个 Node 文件都写顶层 `nodes` Sequence。框架按文件相对路径稳定排序并追加 Sequence，最终
与写在同一个 `nodes` 列表中的语义相同。Mapping 会跨文件递归补充；同一路径标量、`null`
或不一致类型重复定义会报告两处文件位置，不允许后文件静默覆盖。

JSON 与 YAML 使用同一字段结构。`.json` 必须是严格 JSON，不支持注释、尾随逗号、JSONC
或 JSON5；YAML 每个文件只允许一个 Mapping 根文档。Sequence 不按元素 `id` 自动合并，
因此可以一个 Node 一个文件，但不能把同一个 Node 拆成两个列表元素。

## 我想从环境变量注入部署值

同一个拆分示例还演示：

```yaml
nodes:
  - id: game-1
    labels:
      # 运行脚本先设置 ORIGIN_TUTORIAL_REGION=cn-east。
      region: "${ORIGIN_TUTORIAL_REGION}"
    services: [SplitConfigService]
```

环境变量只替换字符串值，不能生成字段名、Mapping 或 Sequence。变量缺失会带文件、行、列
启动失败；错误只显示变量名，不回显可能敏感的变量值。配置快照在启动时一次加载并冻结，
修改文件或环境变量后需要重启，不会运行期热更新。

## 深入一点：默认值与覆盖

框架字段缺失时使用框架默认值；业务结构体字段缺失时保留调用者预先写入的 Go 默认值。
`node_services` 存在时不会递归合并 `services`，它表示该 Node/Service 的完整有效业务配置。

例如业务代码先写入 `serviceConfig{Welcome: "default welcome", MaxPlayers: 10}`，配置如下：

```yaml
services:
  # 公共块仅在没有 Node 专属块时生效。
  ConfigService:
    welcome: hello-from-common
    max_players: 100

node_services:
  # 此块整体替换上面的 ConfigService 公共块。
  game-1:
    ConfigService:
      # 未提供 max_players，会保留 Go 结构体中的默认值 10。
      welcome: hello-from-game-1
```

`game-1` 的最终值是 `welcome="hello-from-game-1"` 和 `max_players=10`：专属块整体替换公共
块，缺失字段保留 Go 初始值，并不会回退读取公共块的 `100`。完整验证见
[默认值与 Node 专属配置](../../../../examples/02-configuration/02-default-and-override/README.md)。

在 `OnInit` 中读取并保存强类型配置，不要把配置解析放在每次业务请求的热路径。
