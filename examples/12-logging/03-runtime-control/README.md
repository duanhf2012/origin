# 运行时控制日志

运行 `run.bat` 或 `./run.sh`。示例模拟管理命令完成以下动作：临时打开 Console Debug、
恢复配置级别、临时提高 File 级别、分别暂停和恢复 Console/File，最后读取当前状态。

公开接口按输出端保持对称：

```go
// 修改当前级别；Reset 恢复启动配置中的级别。
err := log.SetConsoleLevel(log.DebugLevel)
err = log.ResetConsoleLevel()
err = log.SetFileLevel(log.WarnLevel)
err = log.ResetFileLevel()

// false 暂停接收新日志，true 恢复；底层资源不会反复创建。
err = log.SetConsoleEnabled(false)
err = log.SetConsoleEnabled(true)
err = log.SetFileEnabled(false)
err = log.SetFileEnabled(true)

// 读取 Available、Enabled、当前 Level 和 ConfigLevel。
status, err := log.CurrentStatus()
```

这些包级函数只控制当前默认 Application，不需要传 Output 或 Application。Service、普通
goroutine 和已有 RPC Handler 都可以调用；如果通过远程 RPC 暴露，鉴权、审计和访问范围由
业务管理面负责，框架不会自动开放未鉴权端点。

重要边界：

- 启动配置 `enabled: false` 表示没有创建该输出资源，运行时不能凭空开启，会返回
  `ErrLogOutputUnavailable`；暂停只能作用于启动时已经创建的输出。
- 自定义 `LogHandlerFactory` 若未实现可选的 `log.Controller`，控制接口返回
  `ErrLogControlUnsupported`，普通写日志不受影响。
- Application 未启动或已经关闭时返回 `ErrLogClosed`。
- `mode: async` 下，已经排队但尚未写出的记录会按处理当时的最新控制状态过滤。需要严格
  观察“控制调用前后”的测试或管理脚本可使用 `mode: sync`，生产环境通常继续使用 async。

完整实现见 [`main.go`](./main.go)，配置见
[`config/application.yaml`](./config/application.yaml)。文件实际写入
`logs/runtime-control-origin.log`。
