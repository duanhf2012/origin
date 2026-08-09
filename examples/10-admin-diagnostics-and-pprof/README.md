# Admin、Diagnostics 与 pprof 示例

本章把运维能力分成三个清晰边界：Application 拥有通用 Admin HTTP 控制面；Diagnostics 是
Admin 的内置只读路由，也可在进程内直接采样；pprof 使用独立 Listener，并可在运行中开关。

建议按顺序学习：

1. [Service Admin Endpoint](./01-admin-service-endpoints/README.md)：GET/POST、严格 JSON、Await、202 和串行并发语义。
2. [Application Endpoint 与内置控制](./02-admin-application-control/README.md)：进程级扩展，以及 App/Node/Service retire/resume。
3. [本地 Diagnostics Snapshot](./03-diagnostics-snapshot/README.md)：不经过 HTTP 的 Full 快照与所有权层级。
4. [Admin Diagnostics](./04-admin-diagnostics/README.md)：默认 Summary、按需 Full 和真实请求成本。
5. [动态 pprof](./05-pprof-toggle/README.md)：`--pprof` 初态与关闭—重开—查询—关闭。
6. [Metrics Adapter](./06-metrics-adapter/README.md)：一次 Summary 采样供多个监控消费者复用。

端口均只绑定回环地址，避免示例默认暴露管理数据：

| 示例 | Admin | pprof |
| --- | --- | --- |
| 01 | `127.0.0.1:6061` | 未启用 |
| 02 | `127.0.0.1:6062` | 未启用 |
| 03 | 未启用 | 未启用 |
| 04 | `127.0.0.1:6063` | 未启用 |
| 05 | `127.0.0.1:6064` | `127.0.0.1:6060` |
| 06 | 未启用 | 未启用 |

同一时间运行多组时使用了不同 Admin 端口。生产如果需要非回环绑定，必须先设置 Admin Guard，
并配套 TLS、网络访问控制、审计和限流；pprof 仍建议只在受控排障窗口短时开启。
