# 公开 API 索引

| 包 | 首选入口 |
| --- | --- |
| `application` | `application.New`、`Setup`、`Start`、`State`、`Node`、`Nodes`、`Logger`、`Diagnostics`、`Start/StopDiagnosticsServer`、`Start/StopPprof`、`Retire`、`Resume` |
| `command` | `start`、`stop`、`help`、`version` 的 `Runner`、`Command` 和 `StartRequest`；`start --retired`、`--diagnostics`、`--pprof` |
| `config` | `LoadDir`、`LoadSnapshot`、`View`、`Duration`、`ByteSize` |
| `log` | `Logger`、`Field`、`Runtime`、`Flush`、`Stats`；默认 Zap Handler 位于 `log/zaplog` |
| `node` | `Node`、`State`、`Service`、`Services`、`Diagnostics`、`Retire`、`Resume` |
| `service` | `Service`、`Module`、`Application`、`AfterFunc`、`NewTicker`、`CronFunc`、`NotifyEventSync`、`NotifyEventAsync`、`DispatchAsync`、`Await`、`GoSafe`、`RunSafe`、`Retire`、`Resume` |
| `rpc` | 生成客户端、`OnNode`、`Route`、`RouteRoundRobin`、`RouteRandom`、`RouteBy`、`IncludeRetired`、广播调用与 `BroadcastError` |
| `discovery` | 服务目录的事件和值类型；通过 `Service` 使用 `AwaitService`、`AwaitNodeService`、精确查询、列表查询和监听 |
| `discovery/provider` | 自定义 Provider SPI |
| `diagnostics` | 诊断快照和值类型 |
| `errs` | 稳定错误码和 `CodeOf` |

业务代码使用这些公开包；不要导入 `internal/`，也不要依赖测试夹具。

Timer、事件与执行接口都提供对应统计快照。`Module` 上的同名方法会委托给所属 `Service`，因此不需要维护第二套使用方式。

`Await`、`Call`、`Async`、`Notify` 和 `Broadcast` 的 `context.Context` 可以传入 nil、
`context.Background()`、`context.TODO()` 或自定义 Deadline；前三者在没有可用 Deadline 时按
当前 Service 的默认 15 秒预算处理。需要更长的启动或迁移操作时显式传入
`context.WithTimeout`，不要依赖隐式预算。Service Task 内可能阻塞的 Application 停止操作
使用 `Await` 释放执行权；`Diagnostics`、地址查询和启动操作是短操作，可直接调用。
