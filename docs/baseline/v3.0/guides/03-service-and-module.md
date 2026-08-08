# 03：组织业务：Service 与 Module

## 我想新增一个 Service

运行：[examples/04-service-and-module/01-first-service](../../../../examples/04-service-and-module/01-first-service)。

一个业务 Service 只需要嵌入 `service.Service` 并在 `app.Setup` 中登记：

```go
type InventoryService struct {
    // 嵌入后该类型成为可由 Application 托管的 Service。
    service.Service
}

func init() {
    // 登记零值模板；配置决定在哪些 Node 创建实例。
    app.Setup(&InventoryService{})
}
```

然后在 Node 配置的 `services` 列表中写入 `InventoryService`。

## 我想向 Service 添加 Module

运行：[examples/04-service-and-module/02-module-lifecycle](../../../../examples/04-service-and-module/02-module-lifecycle)。

```go
func (s *GameService) OnInit() error {
    // Module 只能在所属 Service 的 OnInit 中加入。
    return s.AddModule(&RootModule{})
}
```

Module 只能在所属 Service 或父 Module 的 `OnInit` 中添加。它适合把存档、排行榜、战斗、房间等内部职责从 Service 主类型中拆开；它不是另一个可独立部署或发现的 Service。

## 我想读取当前 Service 身份或同 Node 协作者

`Service` 在完成装配后自动知道自己的实际名称和所属 Node。名称来自 YAML 的实际 ServiceName，而不是 Go 类型名；例如 `player-1:PlayerService` 中，业务 `PlayerService` 的 `Name()` 是 `player-1`，`NodeID()` 是所在 Node 的 ID。

```go
func (s *GatewayService) OnStart(ctx context.Context) error {
    // Logger 已自动附带 node_id 与 service_name；无需每次手写这些字段。
    s.Logger().Info(fmt.Sprintf("started node=%s service=%s state=%s",
        s.NodeID(), s.Name(), s.State(),
    ))

    // 只从当前 GatewayService 所属 Node 的本地 Service 集合中，按实际 ServiceName 查询。
    // 它不会查询服务发现目录、etcd/NATS，也不会发起网络或 RPC 路由。
    // 因此这里的 PlayerService 必须确实配置在同一个 Node；其他 Node 即使发布了它也不会命中。
    player, ok := s.LookupService("PlayerService")
    if !ok {
        return fmt.Errorf("local PlayerService is not configured")
    }
    // 对管理操作使用 IService 的公开能力；不要把它当作跨 Service 业务调用的替代品。
    return player.Retire(ctx)
}
```

`LookupService("PlayerService")` 等价于“从当前 Service 所属 Node 的本地 Service 集合中，查找
实际名称正好为 `PlayerService` 的实例”。它不会遍历任何已发现的远端服务，也不会读取 etcd、
NATS 或发起 RPC；本 Node 没有该实例时，即使其他 Node 已发布同名 Service，也只会返回
`(nil, false)`。

它适合同 Node 的明确管理协作，例如维护 Service 精确退休本地目标；正常业务请求仍应使用生成
RPC 客户端，避免把业务耦合到“必须部署在同一 Node”的假设。返回值是 `IService`，不应依赖不受
控制的具体类型断言。`Name`、`NodeID`、`State` 和 `Logger` 在 Setup 样本或未绑定测试对象上仍可
安全调用，分别返回空值、`created` 或 Nop Logger。

运行期若框架无法证明某个 Service 的调度状态安全，会隔离该 Service 并令其进入 `failed`。此时可从本地诊断代码读取 `Failure()` 获取第一次根因；不要把该 error 原样通过 RPC 暴露给调用方：

```go
if s.State() == service.StateFailed {
    // Failure 只保留首个根因，适合日志、诊断或告警归因。
    s.Logger().Error("service isolated", originlog.Err(s.Failure()))
}
```

更适合跨对象的冷路径状态查询见 [09：Diagnostics 与 pprof](./09-diagnostics-and-pprof.md) 的
`Node.ServiceStatus` 与 `Application.Diagnostics`。

## Module 如何取得所属 Service

通常 Module 直接调用自己已委托的 `Timer`、事件、配置、`Await` 与 `Retire/Resume` 方法，不需要先取得 owner。只有确实需要把所属 Service 的公开能力传给受控内部协作者时，才使用 `module.Service()`：

```go
func (m *RankModule) OnInit() error {
    if m.Service() == nil {
        return errors.New("module is not attached")
    }
    // Module 自己订阅事件，框架才会在 Module 停止时自动移除该监听。
    return m.SubscribeEvent(rankChangedEvent, m.onRankChanged)
}
```

Module 只能归属于一个 Service，不能复制、迁移或在多个 Service 间复用。Module 自己创建的
Timer 与订阅的本地事件监听器会在 Module 停止时自动取消/失效；不要再为这些归属资源维护第二套
清理表。由 `GoSafe` 创建的 goroutine、文件句柄和外部连接仍是业务资源，必须在 `OnStop` 自行停止。

## 深入一点：生命周期和归属

一个 Module 只能绑定一个 Service，不能在运行中迁移或共享。Module 可委托使用 Service 的
Timer、事件、配置、Await、Retire 与诊断受限 Application 外观，因此所属 Service 是 Module
的生命周期父级：父级先启动并最后停止。

假设 `GameService` 注册了以下静态树：

```text
GameService
├─ RootModuleA
│  ├─ ChildModuleA1
│  └─ ChildModuleA2
└─ RootModuleB
   └─ ChildModuleB1
```

完整启动顺序是：

```text
1. GameService.OnStart
2. RootModuleA.OnStart
3. ChildModuleA1.OnStart
4. ChildModuleA2.OnStart
5. RootModuleB.OnStart
6. ChildModuleB1.OnStart
```

完整停止顺序严格相反：

```text
1. ChildModuleB1.OnStop
2. RootModuleB.OnStop
3. ChildModuleA2.OnStop
4. ChildModuleA1.OnStop
5. RootModuleA.OnStop
6. GameService.OnStop
```

这个顺序带来两条业务约束：

- `Service.OnStart` 可以创建数据库连接、RPC Client 和共享状态；Module 随后启动，可以使用
  这些 Service 级资源。`Service.OnStart` 不应调用尚未启动的 Module 执行业务操作。
- `Module.OnStop` 执行时 Service 仍未停止，可以完成 Await、RPC、存档和 Worker 回收；最后的
  `Service.OnStop` 汇总 Module 已提交的结果并关闭共享资源，不应再次要求已停止 Module 工作。

失败回滚也遵守同一个生命周期栈。判断标准是“对象是否已经进入 `OnStart`”，不是
“`OnStart` 是否返回成功”：

- `Service.OnInit` 失败时，Service 和 Module 都没有进入 `OnStart`，因此都不调用 `OnStop`。
- `Service.OnStart` 返回错误时，不会调用任何 `Module.OnStart` 或 `Module.OnStop`；但 Service
  已经进入过 `OnStart`，框架仍调用一次 `Service.OnStop`，清理启动到一半时已经交给 Service
  持有的资源。
- `Module.OnStart` 返回错误时，失败 Module 自身和此前进入过 `OnStart` 的 Module 都会严格
  逆序停止，然后再调用 `Service.OnStop`。

因此，`OnStop` 应允许字段仍处于“只初始化了一部分”的状态，例如关闭连接前先判断连接是否
已经创建。`OnStart` 中尚未赋给 Service/Module 字段的局部临时资源，则应在返回错误前自行
清理。某个 Module 的 `OnStop` 返回错误或 panic 不会跳过其余 Module，也不会跳过最后的
`Service.OnStop`。

运行 [完整生命周期示例](../../../../examples/04-service-and-module/02-module-lifecycle)，按
`Ctrl+C` 即可直接核对以上启动和停止日志。
