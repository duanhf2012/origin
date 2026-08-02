# 10：部署与运维

## 我想构建并运行一个二进制文件

运行：[examples/10-deployment-and-operations/01-build-and-run](../../../../examples/10-deployment-and-operations/01-build-and-run)。它提供 Windows/Linux 构建和运行脚本，以及等价 `go build` 命令。

```bash
go build -o ./bin/hello-service ./examples/00-quickstart/01-hello-service
./bin/hello-service start --app-name deployed-hello \
  --config ./examples/00-quickstart/01-hello-service/config --node hello-1
```

## 我想运行依赖 NATS 或 etcd 的示例

运行：[examples/10-deployment-and-operations/02-compose-dependencies](../../../../examples/10-deployment-and-operations/02-compose-dependencies)。

```text
deps-up.bat
check-deps.bat
deps-down.bat
```

脚本只调用仓库 `deploy/compose/base-compose.yml`，不会在业务示例运行时隐式启动或删除容器。

## 我想在 Ubuntu 作为长期进程运行

运行：[examples/10-deployment-and-operations/03-ubuntu-systemd](../../../../examples/10-deployment-and-operations/03-ubuntu-systemd)。它给出最小 systemd Unit、配置目录和日志检查方式。

## 深入一点

生产配置应明确区分开发、测试、生产目录；NATS/etcd 凭据不进入示例默认 YAML。TCP、Origin Discovery、诊断 HTTP、pprof 若监听非回环地址，必须由网络策略、反向代理或业务侧认证/TLS 保护。
