# 01 Service Admin Endpoint

这个示例把管理能力直接声明在 `LogicService.AdminEndpoints` 中。Application 启动时冻结路由，
HTTP 请求随后按真实 Node 和 Service 定位，并进入该 Service 与业务任务共用的串行执行槽。

启动后可直接复制执行：

```bash
curl -s http://127.0.0.1:6061/admin/v1/nodes/game-1/services/LogicService/endpoints/summary
curl -i -X POST -H "Content-Type: application/json" -d '{"version":"v2"}' http://127.0.0.1:6061/admin/v1/nodes/game-1/services/LogicService/endpoints/reload-logic
curl -i -X POST -H "Content-Type: application/json" -d '{"player_id":"player-7"}' http://127.0.0.1:6061/admin/v1/nodes/game-1/services/LogicService/endpoints/refresh-player
```

初始查询稳定返回类似：

```json
{"version":"v1","reloads":0,"refreshes":0}
```

`reload-logic` 使用 `Request.DecodeJSON`：未知字段、多个 JSON 值或缺失版本都会返回 `400`。
真正的加载工作放进 `Await`；Await 回调只写局部变量，恢复 Service 串行执行权后才提交字段，
因此其他任务不会看到半完成状态。成功返回 `204 No Content`。

`refresh-player` 先把任务放入有界 Service 队列，再返回 `202 Accepted`。202 表示通知已接受，
不是“刷新已完成”；队列已满或 Service 正在停止时会得到明确错误。并发 POST 仍由同一槽串行化。

安全提示：示例只绑定 `127.0.0.1`。生产环境若要暴露到非回环地址，必须先配置
`Admin Guard`，并在反向代理或网络策略层继续做认证、TLS、限流与审计。
