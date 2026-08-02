# Node 与 Application 批量状态切换

当一次维护需要影响多个 Node 时，可由 `Application` 统一退休和恢复。批量 Retire 按 Node 启动顺序倒序执行，Resume 按正序执行，和资源依赖的停止/启动方向保持一致。

## 示例流程

YAML 配置两个 Node；示例从业务 Service 调用 Application 的公开退休/恢复入口并记录每个 Node 的状态变化。它只演示顺序和状态，不替代生产中的发布流程。

## 运行与练习

执行 `run.bat` 或 `./run.sh`，观察两个 Node 的 Retire 顺序与 Resume 顺序。真实系统应由管理命令、受控运维 API 或发布工作流调用这些入口，并在退休后等待连接排空和监控确认。

对应教程：[Retire、Resume 与优雅停止](../../../docs/baseline/v3.0/guides/08-retire-and-resume.md)。
