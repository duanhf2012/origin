# 03：组织业务：Service 与 Module

## 我想新增一个 Service

运行：[examples/03-service-and-module/01-first-service](../../../../examples/03-service-and-module/01-first-service)。

一个业务 Service 只需要嵌入 `service.Service` 并在 `app.Setup` 中登记：

```go
type InventoryService struct { service.Service }

func init() {
    app.Setup(&InventoryService{})
}
```

然后在 Node 配置的 `services` 列表中写入 `InventoryService`。

## 我想把一个 Service 拆成 Module

运行：[examples/03-service-and-module/02-module-lifecycle](../../../../examples/03-service-and-module/02-module-lifecycle)。

```go
func (s *GameService) OnInit() error {
    return s.AddModule(&RootModule{})
}
```

Module 只能在所属 Service 或父 Module 的 `OnInit` 中添加。它适合把存档、排行榜、战斗、房间等内部职责从 Service 主类型中拆开；它不是另一个可独立部署或发现的 Service。

## 深入一点：生命周期和归属

一个 Module 只能绑定一个 Service，不能在运行中迁移或共享。启动时 Service 先于 Module；停止时 Module 先于 Service。Module 可委托使用 Service 的 Timer、事件、配置、Await、Retire 与诊断受限 Application 外观。
