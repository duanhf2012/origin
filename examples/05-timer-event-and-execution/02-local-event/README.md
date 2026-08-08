# Service 本地事件

本地事件只在一个 Service 内分发，不是跨 Node 消息机制。监听器应在 `OnInit` 注册，这样业务任务开始前监听关系已经固定。示例同时覆盖 `NotifyEventSync`、`NotifyEventAsync` 和 `EventStats`。

## 流程

Timer 先进入 Service 任务上下文，再同步通知玩家 `1001`；监听器又同步通知一个嵌套的审计事件。审计监听器用 `Await` 模拟读取存储：等待期间会释放 Service 执行权，恢复后才完成嵌套事件和外层监听器。随后异步通知玩家 `1002`，它作为普通后续 Service 任务排队执行。异步提交只保存 Event 接口值，提交成功后生产者不能再修改 payload。

同步监听器需要先通过 `SubscribeEvent` 注册，框架才会在通知时调用它。可以这样注册并嵌套另一个同步事件：

```go
func (target *EventService) OnInit() error {
    return target.SubscribeEvent(playerJoinedEvent, target.onPlayerJoined)
}

func (target *EventService) onPlayerJoined(ctx context.Context, event service.Event) error {
    joined := event.(PlayerJoined)
    return target.NotifyEventSync(ctx, PlayerAudit{PlayerID: joined.PlayerID})
}
```

同步监听器可以直接调用通用 `Await` 或生成的 `AwaitXxx` RPC。“同步”表示调用方会等到监听器按注册顺序全部完成，不表示 Await 期间独占 Service；Await 释放执行权后，同 Service 的其他已就绪任务可以插入。

例如，在同步监听器中等待数据库：

```go
func (target *EventService) OnInit() error {
	return target.SubscribeEvent(playerJoinedEvent, target.onPlayerJoined)
}

func (target *EventService) onPlayerJoined(ctx context.Context, event service.Event) error {
	joined := event.(PlayerJoined)
	var extra PlayerExtra
	if err := target.Await(ctx, func(waitCtx context.Context) error {
		var loadErr error
		extra, loadErr = loadPlayerExtraData(waitCtx, joined.PlayerID)
		return loadErr
	}); err != nil {
		return err
	}
	// Await 返回后已恢复 Service 执行权，再更新成员状态。
	target.playerExtra[joined.PlayerID] = extra
	return nil
}
```

生成的 Await RPC 使用同一规则：

```go
func (target *EventService) onPlayerJoined(ctx context.Context, event service.Event) error {
	joined := event.(PlayerJoined)
	player, err := target.players.AwaitGetPlayer(ctx, joined.PlayerID)
	if err != nil {
		return err
	}
	// RPC 等待已结束，现在可在 Service 串行上下文中更新状态。
	target.currentPlayer = player
	return nil
}
```

上面两个片段中，等待函数只操作局部变量，不在已释放执行权时读写 Service 成员。如果业务不需要调用方等到监听器完成，才使用 `NotifyEventAsync`：

```go
// 只保证事件已入队，不等待监听器完成。
if err := target.NotifyEventAsync(PlayerJoined{PlayerID: 1002, Mode: "async"}); err != nil {
	target.Logger().Error("async event submission failed")
}
```

## 运行与练习

执行 `run.bat` 或 `./run.sh`，预期依次看到 sync 玩家日志、audit 日志和 async 玩家日志，随后看到
`sync=2 async=1 failures=0` 的统计（外层和嵌套事件各计一次同步通知）。可增加第二个监听器观察：第一个监听器 Await 恢复并返回后，框架才会继续调用第二个监听器。

对应教程：[Timer、Event 与执行](../../../docs/baseline/v3.0/guides/05.timer-event-and-execution.md)。
