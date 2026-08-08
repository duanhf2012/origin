# 运行时控制日志

运行 `run.bat` 或 `./run.sh`。示例模拟管理命令完成以下动作：临时打开 Console Debug、
恢复配置级别、临时提高 File 级别、分别暂停和恢复 Console/File，最后读取当前状态。

公开接口按输出端保持对称：

```go
// 只把 Console 的最低输出级别临时调到 Debug；File 的级别不会随之改变。
if err := log.SetConsoleLevel(log.DebugLevel); err != nil {
    return err // 业务管理命令应把控制失败返回给调用方。
}
// 把 Console 级别恢复为配置文件中的 log.console.level。
if err := log.ResetConsoleLevel(); err != nil {
    return err // Reset 恢复启动配置，不是恢复到固定的 info。
}
// 只把 File 的最低输出级别临时调到 Warn；Console 仍使用自己的级别。
if err := log.SetFileLevel(log.WarnLevel); err != nil {
    return err // File 未创建或 Handler 不支持控制时会返回错误。
}
// 把 File 级别恢复为配置文件中的 log.file.level。
if err := log.ResetFileLevel(); err != nil {
    return err // 业务可以据此记录管理失败并触发告警。
}

// false 让 Console 暂停接收新日志；不会关闭 stdout/stderr 或影响 File。
if err := log.SetConsoleEnabled(false); err != nil {
    return err // 只有启动时已创建的 Console 才能运行时暂停。
}
// true 恢复 Console 接收日志；不会重新创建另一套 Runtime 或队列。
if err := log.SetConsoleEnabled(true); err != nil {
    return err // 恢复失败时把错误交给管理调用方。
}
// false 让 File 暂停接收新日志；活动文件和滚动资源仍保持原状。
if err := log.SetFileEnabled(false); err != nil {
    return err // 启动配置 enabled=false 的 File 不能凭空运行时开启。
}
// true 恢复 File 接收日志；恢复后继续使用当前 File 级别。
if err := log.SetFileEnabled(true); err != nil {
    return err // 业务可以根据错误决定告警或回退处理。
}

// 读取 Console 和 File 的 Available、Enabled、当前 Level、ConfigLevel。
status, err := log.CurrentStatus()
if err != nil {
    return err // 状态读取失败时不要把不完整状态当作真实配置。
}
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
- `mode: async` 下，普通日志先进入有界队列，调用可能先返回；已经排队但尚未写出的记录会
  按处理当时的最新控制状态过滤，断点也可能早于实际写出，队列满时普通日志会按级别计数
  并丢弃。
- `mode: sync` 下，记录仍进入同一条队列，但调用会等待日志协程处理完成；它不是绕过队列的
  直接写出。需要严格观察“控制调用前后”的测试或管理脚本可使用 `mode: sync`，生产环境
  通常继续使用 `async`。

完整实现见 [`main.go`](./main.go)，配置见
[`config/application.yaml`](./config/application.yaml)。文件实际写入
`logs/runtime-control-origin.log`。
