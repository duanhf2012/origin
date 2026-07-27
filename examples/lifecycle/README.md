# M7 生命周期示例

该示例展示 `Application → Node → Service` 的最小可运行闭环：

- `app.Setup` 可以放在独立 `init_*.go` 文件的 `init()` 中；
- 启动命令中的 Node 顺序就是启动顺序，停止时严格反序；
- `scene-1001:PlayerService` 从同一 Go 类型模板创建独立实例；
- `Ctrl+C` 或 `stop` 命令会触发反序优雅停止。

在仓库根目录运行：

```text
go run ./examples/lifecycle start --app-name lifecycle-demo --config ./examples/lifecycle/config --node gateway-1,game-1
```

另一个终端可以请求停止：

```text
go run ./examples/lifecycle stop --app-name lifecycle-demo
```
