# 基础示例

本示例用于验证 Origin v3 的最小公开接口。目前演示：

- 查询编译时注入的 Version、Commit 和 BuildTime；
- 创建带稳定错误码的错误；
- 使用 `CodeOf` 取得错误码。

直接运行：

```text
go run ./examples/_baseline/v3.0/m0-basic
```

通过构建脚本编译：

```text
scripts\buildwin.bat ./examples/_baseline/v3.0/m0-basic
scripts\buildlinux.bat ./examples/_baseline/v3.0/m0-basic
```

脚本生成的默认文件名分别为 `m0-basic.exe` 和 `m0-basic`。通过脚本编译时会自动注入构建信息；直接使用 `go run` 时构建信息允许为空。

后续规则：

1. 本归档示例始终保持最小、可编译和可运行；
2. RPC、NATS 等独立场景分别放入 `examples` 的独立子目录；
3. 示例不建立独立 `go.mod`，始终跟随仓库根模块编译；
4. 所有示例都必须通过 `go build ./...`。
