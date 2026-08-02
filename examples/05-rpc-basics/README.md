# RPC 基础示例

先运行第一个示例理解合约与同步 Await，再运行第二个示例选择 Async 或 Notify。两个示例
共用 `_support/tutorialrpc` 的合约和 `*.rpc.gen.go`，但各自在当前业务目录定义普通
`PlayerService`；业务实现目录不会生成适配文件。

- [01-contract-generate-bind](./01-contract-generate-bind/README.md)：合约、代码生成、Bind 与 Await。
- [02-async-and-notify](./02-async-and-notify/README.md)：Async 回调与 Notify。

对应教程：[RPC 基础](../../docs/baseline/v3.0/guides/05-rpc-basics.md)。
