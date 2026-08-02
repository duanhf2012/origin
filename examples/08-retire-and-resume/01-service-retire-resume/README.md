# Service Retire 与 Resume

Retire 是临时改变 Service 可路由/可发现状态的控制操作，不等同于停止进程。它不会调用 `OnStop`，因此适合让现有工作完成、拒绝新的自动路由请求后再恢复。

## 示例流程

启动后，Service 先调用 `Retire(ctx)`，记录 `retired` 状态；随后调用 `Resume(ctx)` 回到 `running`。整个过程发生在同一进程，便于把状态切换与生命周期停止区别开。

## 运行与练习

执行 `run.bat` 或 `./run.sh`，预期依次看到 `retired`、`running`。可在两次调用之间加入自己的“停止接收新任务”逻辑；不要把 Retire 当作释放不可恢复资源的机会，那应放在 `OnStop`。

对应教程：[Retire、Resume 与优雅停止](../../../docs/baseline/v3.0/guides/08-retire-and-resume.md)。
