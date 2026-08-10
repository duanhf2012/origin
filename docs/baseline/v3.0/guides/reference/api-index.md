# 当前公开 API 索引

本页只列教程和业务项目直接使用的 v3.1 外观。框架包间装配接口不属于教程入口；若文档与
代码不一致，以当前代码及 `tests/contracts` 的编译期契约为准。

| 包 | 首选入口 |
| --- | --- |
| `admin` | `Get`、`Post`、`JSON`、`Empty`、`Endpoint`、`Guard`；注册、鉴权和错误响应见[第 10 章](../../../../maintenance/v3.1/guides/10.admin-diagnostics-and-pprof.md) |
| `application` | `New`、`Setup`、`RegisterCommand`、`RegisterDiscoveryProvider`、`Start`、`Stop`、`State`、`Node`、`Nodes`、`Logger`、`Retire`、`Resume`；管理与诊断使用 `RegisterAdminEndpoint`、`SetAdminGuard`、`Start/StopAdminServer`、`AdminAddress`、`Diagnostics`、`DiagnosticsSummary`、`Start/StopPprof`、`PprofAddress` |
| `command` | `New`、`Runner.Register`、`Runner.Run`、`Command`；内置 `start`、`retire`、`resume`、`stop`、`help`、`version`，管理监听使用 `--admin`，pprof 使用 `--pprof` |
| `buildinfo` | `Version`、`Commit`、`BuildTime`；编译期注入方式见[第 01 章](../01.first-application.md) |
| `config` | `LoadDir`、`LoadSnapshot`、`View`、`Duration`、`ByteSize` |
| `log` | `Logger`、`Field`、`Runtime`、`Flush`、`Stats`；默认 Zap Handler 位于 `log/zaplog` |
| `node` | `ID`、`Private`、`State`、`Logger`、`Service`、`Services`、`ServiceStatus`、`HealthStatus`、`TransportStatus`、`DiscoveryStatus`、`Diagnostics`、`Retire`、`Resume`；状态读取与生命周期边界见[第 10 章](../../../../maintenance/v3.1/guides/10.admin-diagnostics-and-pprof.md) |
| `service` | `Name`、`NodeID`、`State`、`Logger`、`Failure`、`Application`、`GetNode`、`LookupLocalService`；三种配置读取见[第 02 章](../02.configuration.md)，Module 委托见[第 04 章](../04.service-and-module.md)，Timer/Event/Await 见[第 05 章](../05.timer-event-and-execution.md)，发现见[第 08 章](../08.discovery.md)，Retire/Resume 见[第 09 章](../09.retire-and-resume.md) |
| `rpc` | 使用 `origingen` 生成的强类型客户端；`AwaitXxx` 用于 Service 执行链，`CallXxx` 用于普通 goroutine，另有 `AsyncXxx`、`NotifyXxx`、`BroadcastXxx`、`OnNode`、`RouteRoundRobin`、`RouteRandom`、`RouteBy`、`IncludeRetired` 与 `BroadcastError` |
| `discovery` | 服务目录的事件和值类型；通过 `Service` 使用 `AwaitService`、`AwaitNodeService`、精确查询、列表查询和监听 |
| `discovery/provider` | 自定义 Provider SPI |
| `diagnostics` | 诊断快照和值类型 |
| `errs` | 稳定错误码和 `CodeOf` |

业务代码使用这些公开外观；不要导入 `internal/`，也不要绕过生成客户端直接依赖
`rpc.Runtime`、Reader/Writer/Sizer 或测试夹具。

Timer、事件与执行接口都提供对应统计快照。`Module` 上的同名方法会委托给所属 `Service`，因此不需要维护第二套使用方式。

`Await`、`Call`、`Async`、`Notify` 和 `Broadcast` 的 `context.Context` 可以传入 nil、
`context.Background()`、`context.TODO()` 或自定义 Deadline；前三者在没有可用 Deadline 时按
当前 Service 的默认 15 秒预算处理。需要更长的启动或迁移操作时显式传入
`context.WithTimeout`，不要依赖隐式预算。Service Task 内可能阻塞的 Application 停止操作
使用 `Await` 释放执行权；`Diagnostics`、地址查询和启动操作是短操作，可直接调用。
