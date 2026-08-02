# 公开 API 索引

| 包 | 首选入口 |
| --- | --- |
| `application` | `application.New`、`Setup`、`Start`、`Diagnostics`、`StartDiagnosticsServer`、`StartPprof`、`Retire`、`Resume` |
| `service` | `Service`、`Module`、`AfterFunc`、`NewTicker`、`CronFunc`、`NotifyEventSync`、`NotifyEventAsync`、`DispatchAsync`、`Await`、`GoSafe`、`RunSafe`、`Retire`、`Resume` |
| `rpc` | 生成客户端、`OnNode`、`Route`、`RouteRoundRobin`、`RouteRandom`、`RouteBy`、`IncludeRetired`、广播调用与 `BroadcastError` |
| `discovery` | 服务目录的事件和值类型；通过 `Service` 使用 `AwaitService`、`AwaitNodeService`、精确查询、列表查询和监听 |
| `discovery/provider` | 自定义 Provider SPI |
| `diagnostics` | 诊断快照和值类型 |
| `errs` | 稳定错误码和 `CodeOf` |

业务代码使用这些公开包；不要导入 `internal/`，也不要依赖测试夹具。

Timer、事件与执行接口都提供对应统计快照。`Module` 上的同名方法会委托给所属 `Service`，因此不需要维护第二套使用方式。
