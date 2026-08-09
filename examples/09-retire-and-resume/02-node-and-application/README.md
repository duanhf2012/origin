# Node 与 Application 批量状态切换

当一次维护需要影响多个 Node 时，可由 `Application` 统一退休和恢复。批量 Retire 按 Node 启动顺序倒序执行，Resume 按正序执行，和资源依赖的停止/启动方向保持一致。也可通过 `Application.Node(id)` 精确取得一个 Node，再调用 Node 自己的 `Retire`/`Resume`。

## 示例流程

YAML 配置两个 Node；示例先调用 Application 的批量入口，再单独退休和恢复 `upstream-1`。它只演示顺序和状态，不替代生产中的发布流程。

这里保留进程内 `Node.Retire/Resume` 与 `Application.Retire/Resume` API 编排。若要从另一个进程控制运行中的整个 Application，请使用 [01-service-retire-resume](../01-service-retire-resume/README.md) 的正式 `retire/resume` 命令。

## 运行与练习

执行 `run.bat` 或 `./run.sh`，观察两个 Node 的 Retire 顺序与 Resume 顺序。真实系统可由正式命令或进程内受控编排调用这些入口，并在退休后等待连接排空和监控确认。

对应教程：[Retire、Resume 与优雅停止](../../../docs/baseline/v3.0/guides/09.retire-and-resume.md)。
