# Origin v3 Service 优雅停止设计

## 1. 文档状态与范围

- 状态：本文范围内的方案已确认
- 确认日期：2026-07-24
- 适用版本：Origin v3

本文设计 Service 收到 Stop 信号后的任务准入、排空、最终收尾、`OnStop` 和 `Stopped` 语义。

本文不展开：

- Service 退休状态和服务发现摘流语义；
- Node、Application 以及跨进程编排器的完整停止流程；
- Service 之间停止顺序的最终配置外观；
- 默认 Service 关闭 Deadline 和超时后的进程级强制退出策略；
- Module 独立生命周期接口。

退休状态见 [Origin v3 Service 退休设计](./2026-07-24-service-retirement-design.md)，Application 与 Node 的顺序规则见 [Origin v3 Application 与 Node 生命周期设计](./2026-07-22-application-node-lifecycle-design.md)。

## 2. 设计目标

1. Stop 前已经接收的任务在有界时间内完成，不因停止信号被无条件丢弃。
2. Stop 后不再接收新的普通业务工作，排空集合能够收敛。
3. `OnStop` 可以用顺序代码等待 RPC、Redis 或数据库存档完成。
4. 收尾期间不重新开放普通业务调度，也不创建第二个 Service 用户代码执行者。
5. 资源真正释放完成后才对外显示 `Stopped`。
6. 任意时刻只有一个 finalizer，`OnStop` 和资源释放最多执行一次。
7. RPC Runtime、Transport 和 TimerEngine 按正确顺序保活，使收尾等待可以完成或超时。

## 3. 与 Origin v2 的区别

Origin v2 的 `Service.Stop()` 关闭 `closeSig`，Service 运行协程收到信号后调用 `Release()`，执行 `OnRelease()` 并退出。该流程没有定义“在停止钩子中等待 RPC 返回后再退出”的统一协议。

v3 保留“由 Service 自己的执行协程完成收尾”的直观行为，但增加：

- 明确的任务准入边界；
- 有界排空阶段；
- 支持 `context.Context` 和 `Await` 的 `OnStop`；
- finalizer 唯一性；
- `Stopped` 的严格完成语义；
- 依赖服务和 Node 基础设施的保活顺序。

## 4. 状态与内部阶段

公开状态保持简单：

```text
Running  -> Stopping -> Stopped
Retired  -> Stopping -> Stopped
```

`Stopping` 内部划分两个阶段，但不增加公开状态：

```text
Stopping
  ├─ Draining
  └─ Finalizing
```

- `Draining`：停止接收新的普通业务工作，并完成 Stop 前已经接收的任务；
- `Finalizing`：不再执行普通业务任务，只运行 `OnStop` 和框架资源清理；
- `Stopped`：`OnStop`、框架清理和调度器退出都已经完成。

收尾协程仍在运行时，Service 必须保持 `Stopping`。不能先标记 `Stopped` 再异步释放资源。

## 5. Stop 准入边界

ServiceScheduler 在一个原子状态边界把 Service 切换到 `Stopping/Draining`。

切换完成后立即：

- 拒绝新的入站请求—响应 RPC，返回 `CodeServiceStopping`；
- 拒绝新的 Notify 和 Broadcast 本地投递，并按 `CodeServiceStopping` 记录指标；
- 拒绝新的普通本地业务事件；
- 拒绝注册新的 `AfterFunc`、`TickerFunc` 和 `CronFunc`；
- 不再把尚未到期的业务 Timer 转成新的 Ready 任务；
- 拒绝其他会创建独立根业务任务的入口。

切换前已经被 ServiceScheduler 接收的任务属于排空集合：

- 当前正在执行的任务；
- 已经进入 Ready 队列的 RPC、普通事件和 Timer 回调；
- 已经进入 Waiting 状态、尚待恢复的任务；
- 上述任务所必需的完成、超时和取消信号。

已经到期并进入 Ready 队列的 Timer 属于已接收任务；尚未到期的 Timer 不属于排空集合，在进入 `Stopping` 后取消。

## 6. Draining 调度规则

`Draining` 期间只为排空集合调度 Runner：

- 不接受新的根业务任务；
- Stop 前已接收的 Ready 任务仍按原 FIFO 和单执行槽规则运行；
- Waiting 任务的完成、超时和取消可以恢复原任务；
- 没有排空任务时不为了轮询状态持续创建或唤醒 Runner；
- 新提交入口即使持有旧 Service 引用，也不能重新激活已经收敛的调度器。

`Draining` 结束条件至少包括：

- 没有正在执行的普通任务；
- Ready 队列中没有排空任务；
- 没有尚未完成的 Waiting 任务；
- 不存在尚未处理的必要完成信号。

Stop 前已接收任务在排空期间能否继续发起新的 RPC、Redis 或数据库等待，尚未最终确认。该问题会影响排空集合是否允许派生“任务延续”，后续单独确定。

## 7. finalizer 选举

排空条件满足后，ServiceScheduler 原子选出唯一 finalizer：

1. 优先由最后一个完成排空工作的 Runner 接管；
2. 该 Runner 不再从 Ready 队列取得普通业务任务；
3. 如果 Stop 到来时没有活动 Runner 且排空集合为空，由 Stop 调用方取得 finalizer 执行权；
4. 如果 Stop 调用方不适合直接执行，则最多创建一个临时生命周期协程，不创建普通业务 Runner；
5. 并发或重复 Stop 只能等待同一停止结果，不能再次执行 finalizer。

finalizer 负责：

- 调用一次 `OnStop(ctx)`；
- 执行框架资源和引用清理；
- 发布最终停止结果；
- 把状态切换为 `Stopped`；
- 退出最后一个执行协程。

## 8. OnStop 接口

业务 Service 提供：

```go
type IService interface {
    OnStop(ctx context.Context) error
}
```

`OnStop` 在 `Draining` 完成、业务状态不再被普通任务修改后，由 finalizer 调用一次。

典型存档代码：

```go
func (s *PlayerService) OnStop(ctx context.Context) error {
    players := s.snapshotPlayers()

    for _, player := range players {
        if err := s.dbClient.AwaitSavePlayer(
            ctx,
            player.ID,
            player.Data,
        ); err != nil {
            return err
        }
    }

    return nil
}
```

`OnStop` 可以：

- 读取和整理当前 Service 的最终状态；
- 发起 RPC、Redis、数据库等收尾操作；
- 使用生成的 `AwaitXxx` 或通用 `Await` 顺序等待结果；
- 返回收尾错误。

`OnStop` 不能：

- 注册 Timer；
- 提交普通本地业务事件；
- 重新接受入站 RPC；
- 创建新的普通 Service Runner；
- 把必须完成的存档交给未受框架跟踪的 fire-and-forget goroutine。

需要确保完成的异步工作必须在 `OnStop` 返回前通过 `Await` 或其他明确等待方式结束。`OnStop` 返回即表示业务收尾已经完成，框架不自动猜测未登记的后台 goroutine。

## 9. Finalizing 中的 Await

`OnStop` 中的 `Await` 使用与普通业务相同的生成接口外观，但采用 finalizer 等待路径：

1. finalizer 发起 RPC 或其他异步 I/O；
2. 当前 finalizer goroutine 等待 Future 或 `ctx.Done()`；
3. Node 级 RPC Runtime、Transport、Redis 或数据库适配器在自己的协程中完成 Future；
4. Future 唤醒同一个 finalizer goroutine；
5. 原 goroutine 从 `Await` 返回并继续顺序执行 `OnStop`。

该路径不释放执行权给新的业务 Runner，也不在等待期间处理普通 Service 任务。它会占用一个正在停止的 finalizer goroutine，但不会产生 Service 状态并发访问。

普通 Service `Await` 的 Runner 交接规则见 [Origin v3 Service 协作式调度设计](./2026-07-23-service-cooperative-scheduling-design.md)。finalizer 是停止阶段的特殊等待上下文，不能把该特殊行为扩散到正常业务调度。

## 10. Deadline 与取消

`OnStop(ctx)` 必须携带关闭 Deadline，所有收尾操作继承该 Context。

`OnStop` 的关闭 Context 取 Node/Application 总体关闭 Deadline 与 Service 级关闭 Deadline 中的较早值。`OnStop` 内的 RPC 和外部调用不能突破该父 Context：

- 单次调用显式 Deadline 比父 Context 更早时，使用单次 Deadline；
- 单次调用显式 Deadline 更晚时，仍以父 Context 为上限；
- 没有显式 RPC Deadline 时，继续按 RPC 设计应用 Service 默认值、Node 默认值和内置 `15s` 兜底；
- Redis、数据库等适配器必须遵守同一父 Context 上限。

关闭 Context 取消后：

- pending RPC 和外部等待收到取消；
- `Await` 返回对应的取消或超时错误；
- `OnStop` 应尽快返回；
- 框架继续执行必要的资源清理，并汇总停止错误。

Go 不能安全强杀一个忽略 Context 且永久阻塞的 goroutine。默认关闭 Deadline、超时后等待还是结束进程，以及如何诊断不响应 Context 的业务代码，后续单独确定。

## 11. 依赖服务与基础设施保活

`OnStop` 可能需要调用 `DBService` 存档，因此停止顺序必须保证：

- `DBService` 在调用方 `OnStop` 完成前仍处于 `Running` 且可路由；
- 不能在所有 Service 上同时进入退休状态后，再让调用方通过普通 RPC 访问已经退休的 DBService；
- 不为 `Retired` 增加“OnStop RPC 特权绕过”，避免破坏统一准入语义；
- 依赖方先完成 `OnStop`，被依赖方后停止；
- 跨 Node 时由 Application 配置的 Node 停止顺序和部署编排共同保证；
- 同一 Node 内的 Service 停止顺序需要在独立生命周期设计中明确配置或推导规则。

Node 在全部 Service 的 `OnStop` 完成前必须保持：

- RPC Runtime；
- TCP/NATS Transport；
- pending call 管理；
- 用于 RPC 和关闭 Deadline 的 TimerEngine；
- 收尾操作依赖的 Redis、数据库等共享 Client。

这些基础设施最后停止。业务 Timer 虽然已经停止接收，但 TimerEngine 仍为系统 Deadline 提供能力。

## 12. 错误与清理

`OnStop` 返回错误时：

- Service 记录错误及统一错误码；
- Node/Application 汇总该停止错误；
- 框架仍继续清理 Timer、Module、Future、RPC Client 引用和其他资源；
- 一个 Service 的停止错误不能跳过其他 Service 或 Node 的停止；
- 清理完成后状态仍进入 `Stopped`，停止结果单独保存错误。

`OnStop` panic 时：

- finalizer 在生命周期边界恢复 panic；
- 记录堆栈和 Service 名称；
- 转换为停止错误参与汇总；
- 继续执行框架能够安全完成的资源清理；
- 不能再次调用 `OnStop`。

错误码规则见 [Origin v3 统一错误码设计](./2026-07-24-unified-error-code-design.md)。

## 13. 可观测性

至少记录：

- `Draining` 和 `Finalizing` 开始、结束时间；
- Running、Ready、Waiting 排空数量和耗时；
- Stop 后被拒绝的新 RPC、事件和 Timer 数量；
- finalizer 执行者来源：最后 Runner、Stop 调用方或临时生命周期协程；
- `OnStop` 执行时间、Await 次数和外部调用耗时；
- `OnStop` 返回错误、panic 和 Deadline 超时；
- 每个依赖服务的收尾 RPC 错误；
- Service 从 Stop 信号到 `Stopped` 的总耗时。

## 14. 测试要求

后续实现至少验证：

1. Stop 原子关闭新的 RPC、普通事件和 Timer 准入；
2. Stop 前已经进入 Ready 队列的任务可以完成；
3. Waiting 任务可以通过完成、超时或取消恢复并退出；
4. 尚未到期的业务 Timer 被取消，已进入 Ready 的 Timer 被排空；
5. 排空期间不会接受新的根业务任务；
6. 空闲 Service 收到 Stop 时不创建普通业务 Runner；
7. 最后一个 Runner 可以接管 finalizer；
8. 没有 Runner 时 Stop 调用方可以完成 finalizer；
9. 并发 Stop 只执行一次 `OnStop` 和一次资源清理；
10. `OnStop` 可以 `Await` RPC 存档并在同一 goroutine 恢复；
11. `OnStop` 等待期间不执行普通 Service 任务；
12. `OnStop` 不能创建 Timer 或重新开放业务调度；
13. RPC Runtime、Transport 和 TimerEngine 在 `OnStop` 返回前保持可用；
14. DBService 后停止时，调用方可以完成存档 RPC；
15. DBService 已退休或提前停止时，错误能够返回并参与停止结果；
16. `OnStop` 返回错误后仍执行资源清理；
17. `OnStop` panic 被恢复且不会重复调用；
18. 资源释放完成前状态不会变成 `Stopped`；
19. `Stopped` 后不存在 Runner，任何提交或完成信号都不能重新唤醒 Service。

## 15. 已确认结论

Origin v3 Service 优雅停止采用：

- 有界排空方案；
- Stop 后拒绝新的 RPC、普通本地事件、业务 Timer 和独立根任务；
- Stop 前已接收的 Running、Ready、Waiting 任务属于排空集合；
- `Stopping` 内部使用 `Draining` 和 `Finalizing`，不增加公开状态；
- 最后一个完成排空的 Runner 优先接管 finalizer；
- 没有 Runner 时由 Stop 调用方或唯一临时生命周期协程完成收尾；
- finalizer 调用一次 `OnStop(ctx) error`；
- `OnStop` 可以通过 `Await` 顺序等待 RPC、Redis 和数据库存档；
- finalizer Await 不创建替代业务 Runner，也不处理普通业务任务；
- `OnStop` 完成后才执行框架资源清理并进入 `Stopped`；
- 被依赖服务和 Node 基础设施必须晚于调用方 `OnStop` 停止；
- 不通过特殊 RPC 绕过退休服务的准入规则；
- `OnStop` 错误或 panic 不阻断必要清理和其他服务关闭。

## 16. 后续讨论

1. Draining 中已接收任务能否继续派生新的 RPC、Redis、数据库和 Await；
2. Service 默认关闭 Deadline；
3. Deadline 到期后的进程级处理；
4. 同一 Node 内 Service 启停顺序的配置外观和默认规则；
5. `OnStop` 与 Module 清理钩子的关系；
6. Stop API 的同步、异步和重复调用外观；
7. `Stopping` 状态发布到服务发现以及从远端路由摘除的时序。
