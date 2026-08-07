# 01：创建第一个应用

## 我想启动多个 Node

运行两个 Node、两个 Service：

```text
REM 启动包含两个 Node 的示例。
examples\01-first-application\01-application-node-service\run.bat
```

完整源码：[examples/01-first-application/01-application-node-service](../../../../examples/01-first-application/01-application-node-service)。

配置中每个 Node 只声明实际要运行的 Service：

```yaml
nodes:
  # gateway-1 只创建网关 Service 实例。
  - id: gateway-1
    services: [GatewayService]
  # game-1 只创建玩家 Service 实例。
  - id: game-1
    services: [PlayerService]
```

## 我想确认启动和停止顺序

运行：[examples/01-first-application/02-lifecycle-order](../../../../examples/01-first-application/02-lifecycle-order)。按 `Ctrl+C` 后，`SecondService` 会先停止，`FirstService` 最后停止。

## 深入一点：四个对象

```text
Application
  # Application 持有进程级资源与全部 Node。
  └── Node
        # Node 按配置拥有多个 Service。
        └── Service
              # Module 是 Service 内部的生命周期单元。
              └── Module
```

- `Application` 管理本进程中的全部 Node 和共享资源。
- `Node` 是配置、网络身份和 Service 容器。
- `Service` 是串行执行业务的基本单元。
- `Module` 是一个 Service 内部的生命周期组织单元，下一章后再实际使用。

Service 的 `OnInit`、`OnStart`、`OnStop` 分别适合读取配置/登记资源、开始对外工作、释放业务资源。不要在 `OnInit` 发起依赖其他 Service 的 RPC；应在 `OnStart` 或后续任务中进行。
