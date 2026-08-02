# 构建并运行二进制

本示例把快速入口应用构建为显式二进制，适合验证发布前的基本路径。构建产物放在本目录 `bin/`，已被 Git 忽略。

## 运行

Windows 依次执行 `build.bat`、`run.bat`；Linux/macOS 依次执行 `./build.sh`、`./run.sh`。等价命令为：

```bash
go build -o ./examples/10-deployment-and-operations/01-build-and-run/bin/hello-service \
  ./examples/00-quickstart/01-hello-service
```

生成二进制仍使用同一套 `start --app-name --config --node` 命令行参数。

## 构建信息与练习

仓库构建脚本可通过 ldflags 注入 Version、Commit、BuildTime，业务可从 `buildinfo` 查询。可将输出目录改为发布目录；不要把本地构建产物、密钥或生产 YAML 提交到仓库。

对应教程：[部署与运维](../../../docs/baseline/v3.0/guides/10-deployment-and-operations.md)。
