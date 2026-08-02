# 合约、生成与 Bind

此示例演示同一 Node 内强类型 RPC 的完整链路：声明合约、生成 Dispatcher/客户端、将实现作为普通 Service 登记、使用生成的 Bind 函数调用它。业务代码不需要手写编解码或字符串方法名。

## 关键文件

- [`../../_support/tutorialrpc/player.go`](../../_support/tutorialrpc/player.go)：`PlayerRPC` 合约与 `PlayerService` 实现。
- `generate.bat` / `generate.sh`：调用 `origingen` 更新共享合约的生成代码。
- `main.go`：`CallerService` 使用 `BindPlayerRPC` 得到客户端。

## 运行

生成代码已提交，可直接执行 `run.bat` 或 `./run.sh`。修改 RPC 接口后，先运行 `generate.bat`，再执行 `go test ./...` 或本示例。

## 观察与练习

预期日志为 `rpc result: player-1001`。`BindPlayerRPC` 默认绑定 `PlayerService`；若模板化部署改了实际服务名，使用生成的 `BindPlayerRPCTo`。可新增一个合约方法，重新生成后观察强类型客户端同步出现。

对应教程：[RPC 基础](../../../docs/baseline/v3.0/guides/05-rpc-basics.md)。
