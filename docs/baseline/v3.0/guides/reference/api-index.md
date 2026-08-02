# 公开 API 索引

| 包 | 首选入口 |
| --- | --- |
| `application` | `application.New`、`Setup`、`Start`、`Diagnostics`、`StartPprof` |
| `service` | `Service`、`Module`、Timer、Event、`Await`、`GoSafe`、`RunSafe`、`Retire` |
| `rpc` | 生成客户端、`OnNode`、`Route`、`IncludeRetired`、`BroadcastError` |
| `discovery` | 服务目录的事件和值类型；通过 `Service` 使用 `AwaitService`、`AwaitNodeService`、查询和监听 |
| `discovery/provider` | 自定义 Provider SPI |
| `diagnostics` | 诊断快照和值类型 |
| `errs` | 稳定错误码和 `CodeOf` |

业务代码使用这些公开包；不要导入 `internal/`，也不要依赖测试夹具。
