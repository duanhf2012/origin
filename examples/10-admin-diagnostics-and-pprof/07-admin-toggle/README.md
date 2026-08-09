# 07 运行期启停 Admin

`--admin` 只决定 Application 启动时是否立刻创建 Admin Listener；它不是唯一入口。Application 已进入
Running 状态、且 Admin 路由已经在启动阶段冻结后，代码仍可调用：

- `StartAdminServer(address)`：打开 Listener。
- `AdminAddress()`：读取是否正在运行及实际绑定地址。
- `StopAdminServer(ctx)`：停止接受新请求，并在 `ctx` 的预算内等待活跃请求退出。

本例的 `run.bat` / `run.sh` **故意不传 `--admin`**。Service 启动后，代码在 2 秒打开
`127.0.0.1:6065`，4 秒关闭，6 秒重开，8 秒再次关闭。两个可访问窗口是第 2–4 秒和第 6–8 秒：

```bash
curl -s http://127.0.0.1:6065/admin/v1/diagnostics
```

核心流程如下：

```go
if err := runtime.StartAdminServer("127.0.0.1:6065"); err != nil {
    return err
}
// StartAdminServer 的返回值只有 error；实际地址要再查询，尤其绑定 :0 时。
address, running := runtime.AdminAddress()
if !running {
    // 启动失败，或没有发布可用 Listener。
}

stopCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
defer cancel()
if err := runtime.StopAdminServer(stopCtx); err != nil {
    // Stop 可能因等待预算到期而失败；不要无期限阻塞。
}
```

`StartAdminServer` 使用的是启动时已冻结的同一张 Admin 路由表，不会重新扫描 Provider 或在运行期新增
端点。重复以相同地址 Start 是幂等的；若要换地址，必须先成功 Stop。没有 Guard 时只能绑定环回地址；
跨主机绑定仍需先配置 `SetAdminGuard`，并配合 TLS、反向代理和网络策略。

如果从 Service Timer 或 Task 调用 `StopAdminServer`，应像本例一样放入 `Await`：停止可能等待正在运行的
Admin 请求，先释放该 Service 的串行执行槽可以避免不必要地阻塞同一 Service 的其他任务。测试直接驱动
抽出的启停函数，因此不依赖真实 Listener 或计时等待。
