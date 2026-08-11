# Gin 与 HTTP Client 纵向切片变更记录

> 日期：2026-08-11
> 状态：实现与 Windows/Ubuntu 验收完成

本切片按已经确认的外观实现 Gin HTTP Server、代码持有的 HTTP Client 和同 Service HTTP 自调用，
没有把 HTTP 强行并入长连接 Session，也没有建立 v2 兼容层。

主要变更：

- 新增 `sysmodule/ginmodule`，业务 Module 可匿名嵌入 `ginmodule.Module`，从当前 Module 直接完成配置、
  普通路由、Safe 路由、Group/SafeGroup、监听、停止和统计；私有 `gin.Engine` 不对外暴露；
- 普通 Gin Handler 与 Middleware 保持在 HTTP 请求 goroutine；Safe Handler 与 Safe Middleware 通过
  请求快照、Service Task 和私有响应缓冲在所属 Service 串行上下文执行；
- Safe 链支持请求级鉴权与 Service 状态授权分层、`Next/Abort`、请求取消、Deadline、过载、panic、
  响应大小/Header 校验和稳定错误映射；取消在 Safe 链返回后再次检查，取消后的结果不会进入冻结提交；
- Server 配置提供请求、Header、Body、响应、在途请求、可信代理和读写/空闲超时边界；TLS 等运行期
  安全对象只从代码注入；
- 新增 `sysmodule/httpclient`，提供可复用 Client、私有或显式共享 Transport、`Do` 流式语义、
  `DoBytes` 有界完整读取、TLS 校验、代理、重定向、Cookie Jar 和连接池控制；
- HTTP Client 不读取 YAML、不创建 Module、不增加业务重试、熔断、Base URL 或默认鉴权 Header；
- 新增 `examples/14-http/01-gin-safe-self-call`，由业务 `PlayerHTTPModule` 集中持有 Server、Client、
  鉴权、路由和状态，展示 `Service Task → Await → HTTP Client → 自身 SafePOST`；
- 新增 Gin 与 HTTP Client 使用指南，并在 TCP、WebSocket、KCP 指南和回环 Example 中补齐公开函数、
  函数参数、Handler、Codec、Origin/TLS、BlockCrypt 和 Dialer 的实际执行协程与所有权规则；
- 根 README 仅在扩展组件表新增第 14 章，不改变 `00`～`12` 基础教程结构。

依赖记录：

| 依赖 | 版本 | 许可证 | 用途 |
| --- | --- | --- | --- |
| `github.com/gin-gonic/gin` | `v1.12.0` | MIT | HTTP 路由、Middleware 和请求上下文 |

性能检查只建立了复用 Client/Transport 的基线，没有发现每请求创建 Client、无界 Body、无界队列或辅助
goroutine。当前证据不支持增加对象池、自定义 HTTP 调度器或更多配置字段，因此没有扩大优化范围。
