# 战斗蓝图工作流

这个示例把 `blueprintmodule.Module` 组合进 `BattleBlueprintModule`，业务节点和玩家数据都留在业务
Module 中，不散落到 Service。它依次演示：

1. `Module.Run` 创建并自动释放一次性 Instance；
2. `Create` + `Instance.Run` 复用长期图身份；
3. `Start` + `OnComplete` 非阻塞启动并把完成回调送回 Service；
4. 异步节点 `Yield` 后由模拟 RPC goroutine 调用 `Resume`；
5. `Reload` 在 Await 阶段加载、编译并原子发布图池。

## 运行

从仓库根目录执行：

```bash
go run ./examples/18-blueprint/01-battle-workflow start \
  --app-name blueprint-example \
  --config ./examples/18-blueprint/01-battle-workflow/config \
  --node blueprint-1
```

也可在 Windows 运行 `run.bat`，在 Linux 运行 `./run.sh`。预期依次看到三次 HP 结果和一次热加载
成功日志；按 `Ctrl+C` 后，模拟 RPC Client、长期 Instance 和蓝图引擎会按所有权逆序关闭。

## 改成自己的业务

- 在 `application.yaml` 中把 `node_dir`、`graph_dir` 指向随服务发布的目录；不要让多个进程写同一目录。
- 节点工厂每次必须返回新对象。工厂只做构造；业务数据读写放在 `Exec`，因为 `Exec` 在 Service
  工作协程执行。
- 外部 RPC 回调只保存并调用一次 `YieldHandle.Resume`，不要在外部 goroutine 访问玩家数据。
- 长期 Instance 适合战斗、关卡或任务会话；明确业务所有者并在结束时 `Close`。普通请求优先用
  `Module.Run`，避免忘记释放。
- `Reload` 成功只影响后续 `Run/Start`；已经 Yield、尚未结束的 Execution 继续使用旧编译快照。
