# 合约、生成与 Bind

此示例演示同一 Node 内强类型 RPC 的完整链路：声明合约、生成 Dispatcher/客户端、将实现作为普通 Service 登记、使用生成的 Bind 函数调用它。业务代码不需要手写编解码或字符串方法名。

## 关键文件

- [`../../_support/tutorialrpc/player_service.go`](../../_support/tutorialrpc/player_service.go)：只声明 `PlayerService` RPC 合约。
- [`../../_support/tutorialrpc/player_service.rpc.gen.go`](../../_support/tutorialrpc/player_service.rpc.gen.go)：由同名契约文件生成的客户端、静态 Dispatcher 和冷启动描述符，不要手改。
- [`player_service.go`](player_service.go)：当前业务目录中的普通 `PlayerService` 实现；业务侧没有生成文件。
- `generate.bat` / `generate.sh`：调用 `go generate`，再由契约文件中的 `//go:generate` 指令运行 `origingen` 更新共享代码。
- `main.go`：`CallerService` 使用 `BindPlayerService` 得到客户端，在
  `OnStart(ctx context.Context)` 中用 `AwaitGetPlayer`，并在普通 goroutine 中用
  `CallGetPlayer`。

## 运行

生成代码已提交，可直接执行 `run.bat` 或 `./run.sh`。修改 RPC 接口后，先运行 `generate.bat`，再执行 `go test ./...` 或本示例。提交前可在仓库根目录运行 `go run ./cmd/origingen rpc --check ./...`，它只检查生成结果是否缺失、过期或多余，不会改写文件。

## 观察与练习

预期日志包含 `rpc result: player-1001` 和 `call result: player-2002`。`OnStart` 接收的
`ctx` 是框架创建的 Lifecycle Context，推荐直接用于 `AwaitXxx`；Await 也接受 nil、
Background、TODO 或显式 Deadline，但普通 goroutine 必须使用 `CallXxx`。
如果 RPC 失败，`OnStart` 返回错误并触发启动回滚。`BindPlayerService` 默认绑定同名
`PlayerService`；若模板化部署改了实际服务名，使用生成的 `BindPlayerServiceTo`。可新增
一个合约方法，重新生成后观察强类型客户端同步出现。

命名规范是“契约名等于目标 Service 名”：`playerapi.PlayerService` 表示 RPC 合约，业务包中的 `player.PlayerService` 表示实现。Go 接口不增加 `I` 前缀；包边界负责区分契约与实现。

实现文件使用 `var _ tutorialrpc.PlayerService = (*PlayerService)(nil)` 做编译期校验。这样漏实现方法会立即失败；生成器只扫描契约包，不扫描或改写业务 Service。Node 冷启动时按模板名 `PlayerService` 找到契约描述符并创建静态 Dispatcher。

若配置写成 `player-1:PlayerService`，`player-1` 是发现、路由和配置使用的实际名，`PlayerService` 仍是自动关联契约的模板名。Service 没有 `SetName`：实际名由配置决定，避免业务代码和配置产生两个名称来源；调用改名实例时使用 `BindPlayerServiceTo(target, "player-1")`。

完整的安装、目录、`go generate` 与 CI 工作流见 [RPC 基础](../../../docs/baseline/v3.0/guides/06.rpc-basics.md)；v3.1 变更索引见
[RPC 调用与 Context](../../../docs/maintenance/v3.1/guides/README.md)。
