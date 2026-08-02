# Node 与 Application 批量状态切换

`Application.Retire` 按 Node 启动顺序倒序退休；`Application.Resume` 按正序恢复。示例使用两个 Node 观察批量控制入口。

```text
run.bat
```

在真实业务中，通常由管理命令、运维 API 或受控工作流调用这些入口，而不是由普通请求自动触发。
