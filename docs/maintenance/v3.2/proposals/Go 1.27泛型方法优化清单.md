# Go 1.27 泛型方法优化清单

## 结论

对外只新增一个泛型方法：

```go
func (service *Service) DispatchAsyncCompletion[T any](
    ctx context.Context,
    wait func(context.Context) (T, error),
    callback func(context.Context, T, error),
) error
```

业务 Service 通过嵌入 `service.Service` 直接调用。它以当前 Service 为 owner，省去显式传入 `IService`；`T` 将异步等待结果传给回调，避免闭包外结果变量。除该 API 外，不增加泛型便利方法，以控制用户的理解成本。

Go 1.27 不允许 interface 方法声明自己的类型参数，因此该方法不加入 `service.IService`。包级 `service.DispatchAsyncCompletion(owner, ...)` 保持不变，继续服务于持有 `IService` 的 RPC 与系统模块内部代码。

## 当前与目标对比

```go
// 当前：owner 显式传入，异步结果经闭包外变量传递。
var player Player

err := service.DispatchAsyncCompletion(
    s,
    ctx,
    func(ctx context.Context) error {
        var err error
        player, err = repo.Load(ctx, id)
        return err
    },
    func(ctx context.Context, err error) {
        if err == nil {
            s.players[player.ID] = player
        }
    },
)
```

```go
// Go 1.27：当前接收者就是 owner，Player 由类型系统传给回调。
err := s.DispatchAsyncCompletion(
    ctx,
    func(ctx context.Context) (Player, error) {
        return repo.Load(ctx, id)
    },
    func(ctx context.Context, player Player, err error) {
        if err == nil {
            s.players[player.ID] = player
        }
    },
)
```

等待阶段仍会释放 Service 的串行执行权；回调阶段仍会在同一 Service 的 FIFO 串行执行上下文中运行。泛型版只改变结果传递与 owner 表达方式，不新建队列、goroutine、超时或取消逻辑。

## 不新增的方法

| 现有能力 | 决定 | 原因 |
| --- | --- | --- |
| `Await` | 保持原样 | 只有 `error` 的调用清晰且常见；结果可继续由闭包变量承接。新增 `AwaitValue[T]` 不值得增加一套用法。 |
| 配置读取 | 保持原样 | `GetServiceConfig(path, &dst)` 已直观；泛型返回值只是少写一个临时变量。 |
| 本地 Service 查询 | 保持原样 | 泛型断言节省有限，动态 Service 查询本身依赖 `IService`。 |
| 事件、发现、Module、Timer、`GoSafe` | 保持原样 | 没有明显的强类型异步结果传递问题；泛型会增加 API 数量。 |
| `PrepareAwaitContext`、`PrepareOperationContext`、`AwaitTimeoutOf` | 仅保留内部函数 | 它们是 RPC 预算与生命周期基础设施；不传递业务结果，接收者化收益不足。 |

## 实施与验证

1. 新方法复用包级 `DispatchAsyncCompletion` 的预约、FIFO、取消、Deadline 和 panic 语义。
2. 保留包级函数，避免 RPC、Kafka、Blueprint 等当前只持有 `IService` 的框架代码被迫改变。
3. 覆盖成功结果、无效参数、等待错误、取消、Deadline、Service 停止、队列满和 panic 的测试；新增泛型方法至少验证强类型结果和回调执行上下文。
4. Go 1.27 RC 可用后执行 `go test ./...`、`go vet ./...`、race 和基准流程；本机 Go 1.26.5 无法编译泛型方法语法。
