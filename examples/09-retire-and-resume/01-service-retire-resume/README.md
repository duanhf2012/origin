# 用命令退休和恢复 Application

`retire` 会把指定 Application 中全部 Node 的 Service 从 `Running` 切换为 `Retired`，但不会停止进程、释放资源或调用 `OnStop`。`resume` 在同一进程中把它们恢复为 `Running`。

本示例的 `MaintenanceService` 只注册 `ServiceStateChanged` 监听器，不会自行切换状态。因此终端 A 中的状态日志完全由终端 B 的控制命令触发。

## 双终端运行

在仓库根目录打开终端 A，启动并保持进程运行：

```bat
examples\09-retire-and-resume\01-service-retire-resume\run.bat
```

```sh
./examples/09-retire-and-resume/01-service-retire-resume/run.sh
```

再打开终端 B，依次执行：

```bat
examples\09-retire-and-resume\01-service-retire-resume\retire.bat
examples\09-retire-and-resume\01-service-retire-resume\resume.bat
examples\09-retire-and-resume\01-service-retire-resume\stop.bat
```

```sh
./examples/09-retire-and-resume/01-service-retire-resume/retire.sh
./examples/09-retire-and-resume/01-service-retire-resume/resume.sh
./examples/09-retire-and-resume/01-service-retire-resume/stop.sh
```

终端 A 会依次看到 `running -> retired` 和 `retired -> running`。只有真实状态变化才产生事件；重复执行 `retire` 或 `resume` 会幂等成功，不重复触发事件。

`--timeout 30s` 是控制命令从定位目标、等待控制锁，到目标处理并返回结果的总等待上限。超时不会强杀目标，也不会回滚已经完成的状态变化。

对应教程：[Retire、Resume 与优雅停止](../../../docs/baseline/v3.0/guides/09.retire-and-resume.md)。
