# 以 Retired 启动以及运行期 Resume

Retire 是临时改变 Service 可路由/可发现状态的控制操作，不等同于停止进程。它不会调用 `OnStop`，因此适合让现有工作完成、拒绝新的自动路由请求后再恢复。

## 示例流程

`run.bat` 和 `run.sh` 在 `start` 命令中传入 `--retired`。全部 Service 仍会正常执行
`OnInit`、`OnStart`，但在首次开放入站和发布发现快照前直接进入 `Retired`，不会先向其他
Node 暴露短暂的 `Running`。该参数作用于本次选中的全部 Node 和 Service。

```text
# 以 Retired 作为全部选中 Service 的初始状态。
go run ./examples/09-retire-and-resume/01-service-retire-resume start --app-name service-retire --config ./examples/09-retire-and-resume/01-service-retire-resume/config --node game-1 --retired
```

启动后的第一次 `Retire(ctx)` 是幂等调用，随后 `Resume(ctx)` 回到 `Running`。若不传
`--retired`，默认初始状态仍是 `Running`；运行期切换继续使用 `Retire/Resume`，而不是重新
执行启动命令。

## 运行与练习

执行 `run.bat` 或 `./run.sh`，预期依次看到 `retired`、`running`。可在两次调用之间加入自己的“停止接收新任务”逻辑；不要把 Retire 当作释放不可恢复资源的机会，那应放在 `OnStop`。

对应教程：[Retire、Resume 与优雅停止](../../../docs/baseline/v3.0/guides/09.retire-and-resume.md)。
