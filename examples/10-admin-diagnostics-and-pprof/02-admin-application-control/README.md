# 02 Application Endpoint 与内置控制

Application Endpoint 适合进程级、并发安全的状态；Service Endpoint 则适合必须进入某个
Service 串行槽的操作。本示例注册 `routing-status` GET 与 `reload-routing` POST，并提供
一个可被内置控制路由精确定位的 `game-1/ControlService`。

这里的 `RegisterAdminEndpoint` 是 Application 冷启动 API：它把端点加入通用 Admin 路由表，
不是启动另一个 HTTP Server。`admin.Get` 只声明查询，`admin.Post` 声明修改/动作；两者的
Handler 都是在收到请求后执行。Application Handler 不自动拥有某个 Service 的执行权，必须使用
原子类型或其他并发安全对象；如果操作的是 Service 普通字段，应改为 Service Provider Endpoint。

```bash
curl -s http://127.0.0.1:6062/admin/v1/application/endpoints/routing-status
curl -i -X POST -H "Content-Type: application/json" -d '{"routing_revision":2}' http://127.0.0.1:6062/admin/v1/application/endpoints/reload-routing
```

内置生命周期控制均为 POST，成功返回 `204 No Content`：

```bash
curl -i -X POST -H "Content-Type: application/json" -d '{}' http://127.0.0.1:6062/admin/v1/nodes/game-1/services/ControlService/retire
curl -i -X POST -H "Content-Type: application/json" -d '{}' http://127.0.0.1:6062/admin/v1/nodes/game-1/services/ControlService/resume
curl -i -X POST -H "Content-Type: application/json" -d '{}' http://127.0.0.1:6062/admin/v1/nodes/game-1/retire
curl -i -X POST -H "Content-Type: application/json" -d '{}' http://127.0.0.1:6062/admin/v1/nodes/game-1/resume
curl -i -X POST -H "Content-Type: application/json" -d '{}' http://127.0.0.1:6062/admin/v1/application/retire
curl -i -X POST -H "Content-Type: application/json" -d '{}' http://127.0.0.1:6062/admin/v1/application/resume
```

Retire/Resume 是幂等状态操作，不等同于 Stop/Start。Retired 目标退出自动发现和普通流量，
但仍保留精确 Admin 路由，所以可以恢复。所有修改都应进入 POST 审计；不要用 GET 产生副作用。

`204 No Content` 表示本次状态变更已经被处理且不返回 JSON；它不是 HTTP Server 已关闭，也不
表示进程退出。`retire` 和 `resume` 分别作用于 Application、Node 或指定 Service，路径中的
目标名称必须是框架已经冻结的真实实例；未知目标返回 404，目标不在可操作生命周期时返回明确
的状态错误。

Application Handler 可并发执行，本例用原子变量保护共享状态。外部绑定必须先设置 Guard；
即使仅绑定回环地址，也应限制主机权限，并避免在响应或日志中泄漏凭证与业务隐私。
