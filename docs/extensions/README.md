# 组件与第三方扩展

本目录是框架基础教程之外的按需入口。先完成根 README 的 `00`～`06`；只有项目确实需要某项能力时，
再选择对应教程。

## 怎么区分

- **Origin 组件**：随 Origin 发布并使用统一生命周期和公共外观，使用者不需要直接适配其底层库；
- **第三方集成**：依赖独立部署、认证、升级和监控的外部基础设施；
- **自定义扩展点**：Origin 只定义接口，由业务项目或独立包实现适配。

底层使用开源依赖不等于使用者在接入第三方组件。例如 WebSocket 使用成熟协议库实现，但公开的是
Origin 的 `Server`、`Client`、`Dialer` 和 `Session`，因此属于 Origin 网络组件。

## Origin 组件

| 组件 | 使用场景 | 教程与示例 |
| --- | --- | --- |
| TCP 网络模块 | 游戏客户端、自定义长度帧和二进制长连接 | [TCP 使用指南](../maintenance/v3.2/guides/TCP网络模块使用指南.md) · [示例](../../examples/13-network/01-tcp-raw-self-call/README.md) |
| WebSocket 网络模块 | 浏览器、网关和 HTTP Upgrade 长连接 | [WebSocket 使用指南](../maintenance/v3.2/guides/WebSocket网络模块使用指南.md) · [示例](../../examples/13-network/02-websocket-raw-self-call/README.md) |

后续 KCP、Gin 等系统模块完成验收后，再按相同规则加入本表；尚未交付的能力不提前写入使用教程。

## 第三方基础设施

| 集成 | 选择条件 | 教程 |
| --- | --- | --- |
| NATS RPC | 已有 NATS 集群，希望由 Broker 管理连接和恢复 | [使用 NATS 承载跨节点 RPC](./nats-rpc.md) |
| etcd 服务发现 | 已有 etcd 集群，需要跨进程共享服务目录 | [使用 etcd 进行服务发现](./etcd-discovery.md) |

开发示例中的依赖脚本只用于本机验证。生产环境仍需单独设计集群拓扑、认证、TLS、容量、监控、升级和
备份，不应把示例 Compose 当作部署模板。

## 自定义扩展点

| 扩展点 | 入口 |
| --- | --- |
| 日志输出后端 | [自定义日志 Handler](../../examples/03-logging/05-custom-handler/README.md) |
| 服务发现系统 | [自定义 Provider](../../examples/08-discovery/04-custom-provider/README.md) |
| 监控系统 | [Metrics Adapter](../../examples/10-admin-diagnostics-and-pprof/06-metrics-adapter/README.md) |
| 网络业务协议 | [PB、JSON 与自定义 Codec](../maintenance/v3.2/guides/TCP网络模块使用指南.md#4-pbjson-与自定义-codec) |

自定义适配应保持边界小：只转换配置、生命周期、错误和数据，不复制 Origin 已有的调度、队列、重连或
路由实现。需要接入具体产品时，建议在独立包中实现并独立测试，避免让框架基础教程绑定某个供应商。
