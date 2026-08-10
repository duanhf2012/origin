# 自定义日志 Handler

当项目已经有统一的日志平台、审计落库链路，或必须对接自己的日志库时，可以用
`application.Options.LogHandlerFactory` 替换 Origin 的默认 Zap 输出。本例提供一个完整的
JSON Lines Handler，并保留 Origin 负责的队列、调用位置、Service 归属和关闭顺序。

运行 `run.bat`；Linux/macOS 使用 `./run.sh`。输出类似：

```json
{"time":"...","level":"info","caller":"05-custom-handler/main.go:...","message":"custom handler is ready","fields":{"component":"bootstrap"}}
{"time":"...","level":"info","caller":"05-custom-handler/main.go:...","message":"player service is ready","fields":{"player_id":10001}}
```

## 使用步骤

第一步，在创建 Application 时传入 Factory。Factory 接收已经合并完成的 `log.Config`，在启动时
只调用一次；返回的 Handler 将被 Origin Runtime 持有并在停止时关闭。

```go
var app = application.New(application.Options{
    LogHandlerFactory: newJSONHandler,
})

func newJSONHandler(config log.Config) (log.Handler, error) {
    // 本例只把 console.level 作为自定义 JSON 输出的最低级别。
    return &jsonHandler{output: os.Stdout, minimum: config.Console.Level}, nil
}
```

第二步，实现四个 `log.Handler` 方法：

```go
type Handler interface {
    Enabled(level log.Level) bool
    Write(record log.Record, fields []log.Field) error
    Sync() error
    Close() error
}
```

- `Enabled` 会被多个业务协程并发调用，只回答该级别是否需要处理；本例的最低级别构造后不变，
  所以无需锁。
- `Write` 接收一条完整记录和结构化字段。Origin 只由唯一日志协程串行调用 `Write`；字段切片
  只在本次调用有效，不能保存到异步 goroutine 或长期缓存中。
- `Sync` 在 Runtime 刷新时调用；如果目标库有缓冲，应在此 flush。本例使用 `os.Stdout`，无需
  额外动作。
- `Close` 在停止时只调用一次，用于释放 Handler 自己创建的资源。本例不能关闭进程拥有的
  `os.Stdout`。

第三步，按 Handler 自己的协议映射 `Record` 和 `Field`。本例把 Origin 预定义元数据放在顶层，
把业务字段放入 `fields`，避免业务字段覆盖 `time`、`level`、`message` 等保留键：

```go
document := map[string]any{
    "time":    record.Time.UTC().Format(time.RFC3339Nano),
    "level":   record.Level.String(),
    "caller":  fmt.Sprintf("%s:%d", record.Caller.File, record.Caller.Line),
    "message": record.Message,
}
```

完整的 `FieldKind` 映射见 [`main.go`](./main.go)。实际对接 Zap、slog、审计平台或 HTTP 客户端时，
同样应在 `Write` 内调用目标库的同步写入入口；不要再建立第二条后台日志队列或输出 goroutine，
否则会增加排队、丢失和停止时序的复杂度。

`BytesField` 可能包含非 UTF-8 数据，本例使用 Base64 保存原始字节；不要直接转换为字符串，
否则 JSON 编码会替换无效字节。`AnyField` 已是调用点生成的 JSON 快照，交给异步组件前仍需
复制其字节切片。

## 运行时控制是否可用

本例故意不实现可选的 `log.Controller`。因此 `log.Info`、`service.Logger().Info` 等写日志正常，
但 `log.SetConsoleLevel`、`log.SetFileEnabled` 和 `log.CurrentStatus` 返回
`errs.ErrLogControlUnsupported`（错误码 7003）。这适合输出策略固定、无需 Origin 管理 Console/File
状态的后端。

若项目确实需要这些包级控制函数，再额外实现：

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

`Controller` 的全部方法必须支持并发调用，并且只能调整 Handler 自己已有的输出资源；不能借此
重建 Origin Runtime 或自行创建第二套队列。内置 Zap Handler 已实现该接口。

本例的完整源码为 [`main.go`](./main.go)，配置为
[`config/application.yaml`](./config/application.yaml)。完整规则见
[日志输出与管理教程](../../../docs/maintenance/v3.1/guides/03.logging.md)。
