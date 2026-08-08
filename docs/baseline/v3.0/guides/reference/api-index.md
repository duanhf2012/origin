# 公开 API 索引

| 包 | 首选入口 |
| --- | --- |
| `application` | `application.New`、`Setup`、`RegisterCommand`、`RegisterDiscoveryProvider`、`Start`、`Stop`、`State`、`Node`、`Nodes`、`Logger`、`Diagnostics`、`Start/StopDiagnosticsServer`、`Start/StopPprof`、`Retire`、`Resume`；使用位置与 Options 见[第 01 章](../01-first-application.md) |
| `command` | `start`、`stop`、`help`、`version` 的 `Runner`、`Command` 和 `StartRequest`；`start --retired`、`--diagnostics`、`--pprof` |
| `buildinfo` | `Version`、`Commit`、`BuildTime`；编译期注入方式见[第 01 章](../01-first-application.md) |
| `config` | `LoadDir`、`LoadSnapshot`、`View`、`Duration`、`ByteSize` |
| `log` | `Logger`、`Field`、`Runtime`、`Flush`、`Stats`；默认 Zap Handler 位于 `log/zaplog` |
| `node` | `ID`、`Private`、`State`、`Logger`、`Service`、`Services`、`ServiceStatus`、`HealthStatus`、`TransportStatus`、`DiscoveryStatus`、`Diagnostics`、`Retire`、`Resume`；状态读取与生命周期边界见[第 09 章](../09-diagnostics-and-pprof.md) |
| `service` | `Name`、`NodeID`、`State`、`Logger`、`LookupLocalService`、`Failure`、三种配置读取、`Module.Service` 见[第 02/03 章](../02-configuration.md)；Timer、事件、执行、统计见[第 04 章](../04-timer-event-and-execution.md)；发现见[第 07 章](../07-discovery.md)；Retire/Resume 见[第 08 章](../08-retire-and-resume.md) |
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
