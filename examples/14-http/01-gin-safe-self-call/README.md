# Gin Safe 路由与 HTTP 自调用

本示例不依赖 NATS、etcd 或其他进程。`HTTPService` 只装配 `PlayerHTTPModule`；业务 Module 匿名嵌入
`ginmodule.Module`，并集中保存路由、鉴权、玩家状态和可复用 HTTP Client。

启动后，Service Timer Task 使用 `Await` 调用自己的 `SafePOST /api/players/42`。原 Task 在等待 HTTP
期间释放 Service 执行权，入口 Safe Task 因而可以创建玩家并返回，不会形成同 Service 自调用死锁。

从仓库根目录运行：

```bash
go run ./examples/14-http/01-gin-safe-self-call start \
  --app-name gin-safe-self-call \
  --config ./examples/14-http/01-gin-safe-self-call/config --node http-1
```

Windows 可执行 `run.bat`，Linux/macOS 可执行 `./run.sh`。预期看到：

```text
HTTP self-call status=201 body={"id":"42","name":"Origin"} service_player=Origin
```

程序会继续运行，方便手动验证。查询已经创建的玩家：

```bash
curl -H "Authorization: Bearer demo" http://127.0.0.1:19093/api/players/42
```

去掉或修改 Authorization Header 会在请求 goroutine 返回 `401`，不会进入 Service 队列。按 `Ctrl+C`
可验证 Gin Server 优雅停止和 HTTP Client 空闲连接关闭。

## 协程怎么选择

| 示例函数 | 执行位置 | 原因 |
| --- | --- | --- |
| `health`、`authenticateToken` | HTTP 请求 goroutine | 不读取 Service 串行业务状态，尽早完成请求级检查 |
| `authorizePlayer`、`createPlayer`、`getPlayer` | Service 工作协程 | 直接读取或修改 `permissions`、`players` |
| `callSelf` | Service 工作协程 | Timer Task，调用前持有 Service 执行权 |
| `Await` 的等待函数与 `DoBytes` | 原 Task goroutine，但已释放 Service 执行权 | 等待 I/O 时允许入口 Safe Task 执行 |
| `SafeContext.JSON` | Service 工作协程编码；请求 goroutine 提交 | Service 侧只写私有缓冲，不接触 ResponseWriter |

完整 Server 说明见 [Gin HTTP Module 使用指南](../../../docs/maintenance/v3.2/guides/Gin%20HTTP%20Module使用指南.md)，
Client 说明见 [HTTP Client 使用指南](../../../docs/maintenance/v3.2/guides/HTTP%20Client使用指南.md)。
