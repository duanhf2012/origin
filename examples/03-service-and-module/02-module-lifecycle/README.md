# Module 生命周期

Module 用于把一个 Service 内部的职责拆开，而不是创建独立调度器或配置根。示例的 Service 添加根 Module，根 Module 在自己的 `OnInit` 中再添加 child Module。

Module 归属 Service 后，可以直接使用与 Service 同语义的配置、异步派发、本地事件、Await、安全执行、Timer 和退休/恢复接口；这些调用仍由所属 Service 调度。为避免把生命周期示例写成 API 大杂烩，本例只展示 `AddModule` 和生命周期顺序；配置委托见 [`02-configuration/03-service-module-config`](../../02-configuration/03-service-module-config/README.md)，其余接口的完整用法见第 04、08 章，代码中的 `target` 可以同样是嵌入了 `service.Module` 的 Module。

## 生命周期规则

`GameService` 是根 Module 和 child Module 的生命周期父级。初始化时，`GameService.OnInit`
调用 `AddModule`，被添加 Module 的 `OnInit` 会在注册点同步执行。启动和停止则形成严格的
生命周期栈：

```text
启动：GameService.OnStart → RootModule.OnStart → ChildModule.OnStart
停止：ChildModule.OnStop → RootModule.OnStop → GameService.OnStop
```

因此 `GameService.OnStart` 可以先创建共享资源，root 和 child 启动时直接使用；停止时 child
先释放自身资源，root 随后释放父级 Module 资源，最后由 `GameService.OnStop` 关闭 Service 级
共享资源。

运行后会依次看到：

```text
game service started
root module started
child module started
```

按 `Ctrl+C` 后会依次看到：

```text
child module stopped
root module stopped
game service stopped
```

如果 `GameService.OnStart` 返回错误，两个 Module 都不会进入 `OnStart`。如果 child 的
`OnStart` 返回错误，框架会依次执行 `ChildModule.OnStop`、`RootModule.OnStop` 和
`GameService.OnStop`，然后把启动错误和可能的回滚错误一起返回。

## 运行与练习

执行 `run.bat` 或 `./run.sh`，按 `Ctrl+C` 后核对 Service、root 与 child 的完整逆序停止日志。
可给 Service、root 与 child 各增加一项独立状态，确认 Module 的状态只服务于所属 Service，
不用于跨 Service 通信。

对应教程：[Service 与 Module](../../../docs/baseline/v3.0/guides/03-service-and-module.md)。
