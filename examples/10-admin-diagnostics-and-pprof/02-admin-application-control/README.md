# 02 Application Endpoint 与内置控制

Application Endpoint 适合进程级、并发安全的状态；Service Endpoint 则适合必须进入某个
Service 串行槽的操作。本示例注册 `routing-status` GET 与 `reload-routing` POST，并提供
一个可被内置控制路由精确定位的 `game-1/ControlService`。

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

Application Handler 可并发执行，本例用原子变量保护共享状态。外部绑定必须先设置 Guard；
即使仅绑定回环地址，也应限制主机权限，并避免在响应或日志中泄漏凭证与业务隐私。
