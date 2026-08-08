# 03 日志输出与管理

> 状态：已实施
> 基线：v3.0
> 目标版本：v3.1.0
> 兼容性：新增 API 和配置字段；既有 v3.0 日志配置仍可加载

本章是 v3.1 起的完整日志教程。v3.0 配置教程中的日志小节只保留历史基线意义；新项目
应以本章和 [`examples/03-logging`](../../../../examples/03-logging/README.md) 为准。

## 先会用：我应该调用哪个入口

业务只需要记住两种来源：

```go
// 普通工具、子模型或没有 Service 引用的代码：不自动附加 Node/Service。
log.Info(
    "config cache refreshed",
    log.Int64("entry_count", 128),
)

// Service 内：自动附加当前配置实例的 node_id 和 service_name。
target.Logger().Info(
    "player loaded",
    log.Int64("player_id", 10001),
)

// Module 内：直接复用所属 Service Logger，归属与 Service 完全相同。
module.Logger().Info(
    "rank module started",
    log.String("component", "RankModule"),
)
```

包级 `log.Xxx` 不是第二套日志系统。Application 在业务生命周期开始前安装唯一默认 Logger，
所有调用继续使用同一 Runtime、队列和 Handler；Application 完全停止后自动清除。初始化前
或停止后调用不会 panic，只是不产生输出。

构造字段本身开销较高时，可以先判断级别，避免在已过滤日志上生成复杂对象：

```go
if log.Enabled(log.DebugLevel) {
    log.Debug("cache detail", log.Any("snapshot", buildDebugSnapshot()))
}
```

`log.Default()` 与 `log.SetDefault()` 是 Application 集成及高级替换入口；常规业务不需要
调用。手工替换默认 Logger 会同时改变包级日志和包级运行时控制的归属，必须由调用方明确
管理 Runtime 生命周期。

推荐使用的级别和方法：

| 级别 | 方法 | 适合内容 |
| --- | --- | --- |
| Debug | `Debug` | 临时排障、细粒度流程，不应默认大量开启 |
| Info | `Info` | 启停、关键业务状态和正常流程摘要 |
| Warn | `Warn` | 可以继续运行但需要关注的退化或异常输入 |
| Error | `Error` | 已经失败的操作，不自动采集完整堆栈 |
| Error + stack | `ErrorStack` | 需要定位调用链的重要异常；成本高于普通 Error |

完整可运行代码见
[`01-global-and-service`](../../../../examples/03-logging/01-global-and-service/README.md)。

## 配置 Console 和 File

下面是一份可直接修改的完整配置：

```yaml
log:
  # async 是生产默认值；sync 让调用等待单条写完，适合本地验证。
  mode: async

  console:
    # console 整段或 enabled 省略时默认开启。
    enabled: true
    # 最低接收级别；可选 debug、info、warn、error，默认 info。
    level: info
    # text 适合人工阅读；json 适合采集，默认 text。
    format: text
    context_fields:
      # 两个开关省略时都默认 true，可以分别关闭。
      node_id: true
      service_name: true

  file:
    # file 整段或 enabled 省略时默认关闭；要生成文件必须显式开启。
    enabled: true
    # File 与 Console 的级别、格式和字段开关完全独立。
    level: debug
    format: json
    # 相对路径以启动工作目录为基准；目录不存在时自动创建。
    path: logs/origin.log
    context_fields:
      node_id: true
      service_name: true
    rotation:
      # 下一条完整日志会使活动文件超限时先滚动；0B 关闭大小滚动。
      max_size: 512M
      # 跨指定时区的自然日时也滚动；目前不支持按小时滚动。
      by_date: true
      timezone: Local
    retention:
      # 删除超过 14 天的归档；0s 表示不按时间限制。
      max_age: 14d
      # 最多保留 30 个归档，不含活动文件；0 表示不按数量限制。
      max_files: 30
      # 由唯一维护协程把普通归档压缩为 gzip。
      compress: true
```

默认规则容易混淆的地方只有两个：Console 默认开启，File 默认关闭。两者不能同时关闭，
否则启动时会返回配置错误。Console/File 的 `context_fields` 省略、写成 `{}` 或显式写两个
`true` 完全等价。

`max_size` 除 `0B` 外必须是 1 MiB 的整数倍，因为底层滚动阈值使用整数 MiB。框架拒绝
`512KB` 等会被截断的值，让配置值和实际阈值保持一致。

## 输出长什么样

### 文本格式

`format: text` 固定为一行：

```text
TIME LEVEL [NODE/SERVICE] CALLER MESSAGE key=value
```

例如：

```text
2026-08-08T18:20:31.123 INFO [game-1/PlayerService] player/player.go:42 player loaded player_id=10001 player_name="Boyce Duan"
```

文本时间使用操作系统本地时间，不输出 `+0800`。作用域按开关组合成 `[game-1]`、
`[PlayerService]`、`[game-1/PlayerService]` 或完全省略。普通字段使用 `key=value`；带空白或
歧义的字符串自动加引号，复杂值使用紧凑 JSON，不再在行尾追加整段 JSON。

### JSON 格式

`format: json` 是一条记录一行的紧凑对象：

```json
{"time":"2026-08-08T10:20:31.123Z","level":"info","node_id":"game-1","service_name":"PlayerService","caller":"player/player.go:42","msg":"player loaded","player_id":10001}
```

JSON 时间统一为 UTC、毫秒精度并以 `Z` 结尾。日志平台应按字段筛选，不依赖 JSON 对象的
字段顺序。

`app_name` 不写入文本或 JSON 内容。`node_id` 与 `service_name` 是框架保留字段，业务传入
同名 Field 会被忽略；记录远端目标时应使用 `target_node_id`、`remote_service_name` 等明确
业务 Key。

格式和四种开关组合见
[`02-formats-and-context`](../../../../examples/03-logging/02-formats-and-context/README.md)。

## 文件名、滚动和磁盘占用

Application 名称只作为文件名前缀。启动参数：

```text
--app-name game
```

配置 `path: logs/origin.log` 后，实际文件为：

```text
logs/game-origin.log
logs/game-origin-2026-08-08T18-30-00.123.log
logs/game-origin-2026-08-08T18-30-00.123.log.gz
logs/game-origin.crash.log
```

显式写 `logs/server.log` 时得到 `logs/game-server.log`。若路径已经以 `game-` 开头，框架不会
重复添加。归档名冲突时追加序号。

`max_files` 只统计归档，不包含活动文件；`max_age` 与 `max_files` 同时配置时，任一条件超限
都会清理。默认极端上限约为 `512 MiB × 30` 再加活动文件，磁盘较小的部署应主动降低
`max_size` 或 `max_files`。按小时滚动暂不提供，通常由日志采集系统二次切分，或者使用更小
的大小阈值。

完整示例见
[`03-file-rotation`](../../../../examples/03-logging/03-file-rotation/README.md)。

## 运行时临时打开 Debug 或暂停输出

不需要通过 Application 或 Output 枚举。Console 与 File 使用对称函数：

```go
// 只把 Console 的最低输出级别临时调到 Debug；File 的级别不会随之改变。
if err := log.SetConsoleLevel(log.DebugLevel); err != nil { // 调整 Console 失败时返回错误。
    return err // 将控制器或输出端返回的错误交给上层处理。
}
// 把 Console 级别恢复为 YAML/JSON 中 log.console.level 的启动配置值。
if err := log.ResetConsoleLevel(); err != nil { // 恢复失败时返回错误。
    return err // 不把启动级别写死在业务代码中。
}
// 只把 File 的最低输出级别临时调到 Warn；Console 仍使用自己的级别。
if err := log.SetFileLevel(log.WarnLevel); err != nil { // 调整 File 失败时返回错误。
    return err // 例如 File 未启用或自定义 Handler 不支持控制时会失败。
}
// 把 File 级别恢复为 log.file.level 的启动配置值。
if err := log.ResetFileLevel(); err != nil { // 恢复失败时返回错误。
    return err // Reset 不会把 File 级别恢复成固定的默认值。
}

// 暂停 Console 接收新日志；这不会关闭 stdout/stderr，也不会影响 File。
if err := log.SetConsoleEnabled(false); err != nil { // 暂停失败时返回错误。
    return err // 只有启动时已创建的输出端才能运行时暂停。
}
// 恢复 Console 接收新日志；恢复后继续使用当前 Console 级别。
if err := log.SetConsoleEnabled(true); err != nil { // 恢复失败时返回错误。
    return err // 不会重新创建另一套 Runtime、队列或输出资源。
}
// 暂停 File 接收新日志；活动文件和滚动资源仍由原 Runtime 持有。
if err := log.SetFileEnabled(false); err != nil { // 暂停失败时返回错误。
    return err // 若启动配置 enabled=false，则这里会返回不可用错误。
}
// 恢复 File 接收新日志；它不会凭空创建启动时没有的 File 输出端。
if err := log.SetFileEnabled(true); err != nil { // 恢复失败时返回错误。
    return err // 业务可据此记录管理操作失败并告警。
}
```

暂停只停止接收新日志，不关闭 Console/File 资源，也不会重建 Runtime 或队列。启动时
`enabled: false` 的输出根本没有资源，运行中尝试开启会返回
`errs.ErrLogOutputUnavailable`（错误码 7004）。Application 未运行返回
`errs.ErrLogClosed`；非法级别返回参数错误。

运行状态可以查询：

```go
status, err := log.CurrentStatus()
if err != nil {
    return err
}

// Available：启动时是否创建资源；Enabled：当前是否接收。
// Level：当前最低级别；ConfigLevel：Reset 将恢复的启动级别。
log.Info(
    "logging status",
    log.Bool("console_available", status.Console.Available),
    log.Bool("console_enabled", status.Console.Enabled),
    log.String("console_level", status.Console.Level.String()),
)
```

Service、普通 goroutine、管理命令和 RPC Handler 都能调用这些并发安全的包级函数。远程
管理必须由业务层先完成鉴权、授权和审计；Origin 不自动开放可修改日志状态的无认证端点。

异步模式下，已经排队但尚未处理的记录按处理时的最新状态过滤，因此调试脚本若要求严格
观察控制调用前后顺序，可临时使用 `mode: sync`。完整示例见
[`04-runtime-control`](../../../../examples/03-logging/04-runtime-control/README.md)。

## Diagnostics 中的日志状态

前面的 `log.CurrentStatus()` 只返回日志控制所需的 Console/File 状态；
`Application.Diagnostics()` 则采集一份更完整的进程级只读快照，除了 Application、Runtime、
BufferPool 和 Node 状态，也把同一份日志状态放在快照的 `Log` 字段中。它不会修改日志配置，
也不会因为读取快照而创建新的日志 Runtime。

在持有 `*application.Application` 的管理代码中，调用入口是：

```go
snapshot := app.Diagnostics() // app 是当前 Application，snapshot 是这个时间点的只读快照。
```

如果需要把快照交给诊断 HTTP、文件或监控适配层，可以按标准 JSON 序列化：

```go
// 本段需要导入标准库 encoding/json 和 fmt。
data, err := json.MarshalIndent(snapshot, "", "  ") // 将 Go 快照编码为可读 JSON。
if err != nil {
    return err // 序列化失败时不要输出不完整的诊断数据。
}
fmt.Println(string(data)) // 这里只是示例；HTTP 服务通常直接写入 ResponseWriter。
```

下面的 JSON 不是另一套日志 API，而是上面 `snapshot` 序列化后的示意片段：

```json
{
  "log": {
    "console": {
      "available": true,
      "enabled": true,
      "level": "debug",
      "config_level": "info"
    },
    "file": {
      "available": true,
      "enabled": false,
      "level": "warn",
      "config_level": "debug"
    }
  }
}
```

字段对应关系是：`snapshot.Log.Console` → `log.console`，`snapshot.Log.File` → `log.file`；
每个输出端的 `Available`、`Enabled`、`Level`、`ConfigLevel` 则分别对应 JSON 中的
`available`、`enabled`、`level`、`config_level`。已有诊断 HTTP 服务可以把同一个快照原样
导出；它只读，不会自动提供修改日志状态的 HTTP API。

## 自定义 Handler

项目仍通过 `application.Options.LogHandlerFactory` 替换输出端。固定日志后端只需要实现
`log.Handler`；若还要支持包级运行时控制，再可选实现 `log.Controller`：

```go
type Controller interface {
    SetConsoleLevel(level log.Level) error
    ResetConsoleLevel() error
    SetFileLevel(level log.Level) error
    ResetFileLevel() error
    SetConsoleEnabled(enabled bool) error
    SetFileEnabled(enabled bool) error
    Status() log.Status
}
```

第三方 Handler 未实现 Controller 时，写日志仍正常，控制与状态接口返回
`errs.ErrLogControlUnsupported`（错误码 7003）。实现 Controller 的方法必须并发安全，且不应
自行建立第二套 Runtime 或队列。

## 选择建议

- 业务默认使用异步模式；本地测试、严格核对控制顺序时使用同步模式。
- 工具代码用 `log.Xxx`，有明确业务归属时用 Service/Module Logger。
- 人工终端用 text，采集文件通常用 JSON。
- Console 可以隐藏 `service_name` 保持简洁，File 保留完整 Node/Service 便于检索。
- 至少为文件保留设置一个有限条件，避免日志长期撑满磁盘。
- 线上临时 Debug 后使用 Reset 恢复配置值，不要在业务代码里猜测原级别。
