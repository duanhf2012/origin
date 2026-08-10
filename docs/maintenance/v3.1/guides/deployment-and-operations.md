# Origin 部署与运维

> 状态：v3.1 发布前复审完成
>
> 目标版本：v3.1.0
>
> 边界：本文说明 Origin 应用的构建、启动、停止和运行保护；`deploy/compose` 只用于本地开发与集成测试，不是生产编排

## 构建一个可追溯制品

在业务仓库锁定 Go 版本和依赖后，先运行测试与生成检查，再构建具体 `main` 包。以下路径用
`./cmd/game-server` 作为业务项目示例：

```bash
go test ./... -count=1
go run ./cmd/origingen rpc --check ./...

build_time=$(date -u +%Y-%m-%dT%H:%M:%SZ)
version=v3.1.0
commit=$(git rev-parse HEAD)
go build -trimpath -o ./build/game-server \
  -ldflags "-X=github.com/duanhf2012/origin/v3/buildinfo.buildTime=$build_time \
-X=github.com/duanhf2012/origin/v3/buildinfo.version=$version \
-X=github.com/duanhf2012/origin/v3/buildinfo.commit=$commit" \
  ./cmd/game-server
```

`origingen` 位于 Origin 源码仓库；业务仓库若使用独立工具路径，应执行自己的等价生成检查。
制品、配置和依赖版本应一起归档。只有 BuildTime 不同的两个制品也不是逐字节可重复构建；需要
复现时显式固定三项链接值和 Go 工具链。

Origin 仓库的 `scripts/buildwin.bat`、`scripts/buildlinux.bat` 用于 Windows 本机编译或交叉编译，
默认只验证全部包可构建；要得到可部署程序，必须把具体 `main` 包作为参数，或在业务仓库使用
上面的明确 `-o` 命令。

## 目录与权限

建议分离四类数据：

```text
/opt/origin/game-server        # 只读二进制
/etc/origin/game/              # 只读配置与证书
/run/origin-game/              # PID 与控制文件，重启后可重建
/var/log/origin-game/          # 业务日志（若启用文件输出）
```

进程使用独立的非 root 账号。配置中的 NATS/etcd 凭据、TLS Key 和 NKey Seed 应限制为该账号可读，
不要写入镜像、命令行、日志或 Diagnostics 导出。每个 Application 必须使用独立 `--app-name` 和
`--pid-dir`，避免控制文件互相覆盖。

## 启动与停止

```bash
/opt/origin/game-server start \
  --app-name game \
  --config /etc/origin/game \
  --pid-dir /run/origin-game \
  --node game-1 \
  --admin 127.0.0.1:6061

/opt/origin/game-server stop \
  --app-name game \
  --pid-dir /run/origin-game \
  --timeout 30s
```

`stop` 在 Linux/macOS 向持有 PID 锁的进程发送 `SIGTERM`，并等待锁释放；`Ctrl+C` 与平台
`SIGTERM` 进入同一优雅停止路径。外部命令的 `--timeout` 只限制调用方等待，不会强杀进程。
Application `Options.StopTimeout` 是进程内部的总停止预算，两者应与进程管理器的停止期限一起
配置；不要在 timeout 后直接删除 PID/控制文件。

一个最小 systemd 单元可写成：

```ini
[Unit]
Description=Origin game application
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
User=origin-game
Group=origin-game
WorkingDirectory=/opt/origin
RuntimeDirectory=origin-game
RuntimeDirectoryMode=0750
ExecStart=/opt/origin/game-server start --app-name game --config /etc/origin/game --pid-dir /run/origin-game --node game-1 --admin 127.0.0.1:6061
ExecStop=/opt/origin/game-server stop --app-name game --pid-dir /run/origin-game --timeout 30s
TimeoutStopSec=45s
Restart=on-failure
NoNewPrivileges=true
PrivateTmp=true

[Install]
WantedBy=multi-user.target
```

先在实际 Service 的 `OnStop`、Provider 撤销和日志 Flush 时长上验证 30/45 秒是否足够，再设定
生产值。不要让 systemd 的 `TimeoutStopSec` 小于外部 `--timeout` 或内部 `StopTimeout`。

## 管理面与依赖安全

- Admin 无 Guard 时只允许环回监听；生产中仍应通过受控代理、TLS、认证、网络 ACL、审计和限流
  保护。Diagnostics 默认使用 Summary，Full 只在排障时请求。
- pprof 使用独立 Listener，只在受控排障窗口开启；采样完成后立即关闭，不把它用作周期 Metrics。
- NATS/etcd 应启用 TLS、最小权限凭据和服务端消息/连接上限。配置的 Advertise 地址必须能被
  其他 Node 实际访问，不能把容器内部名称发布给容器外客户端。
- `deploy/compose/base-compose.yml` 只提供当前集成测试需要的 etcd/NATS，发布端口默认绑定到
  `127.0.0.1`。受控测试网确需远程访问时可设置 `ORIGIN_BIND_ADDRESS`；该 Compose 没有生产
  认证、备份、升级或高可用运维保证。MongoDB 等发布后组件不放入当前部署入口。

## 发布与排障检查

1. 记录制品 Hash、版本、Commit、Go 版本、配置版本和依赖版本；
2. 先启动一个受控实例，检查启动日志、稳定错误码和 Admin Summary；
3. 验证 Discovery 收敛、RPC 成功/超时、Retire/Resume 与一次完整优雅停止；
4. 观察 goroutine、连接、Timer、Buffer、内存和 P95/P99，不只看平均值；
5. 扩容前确认队列、pending、Payload、NATS/etcd 和日志磁盘预算；
6. 回滚时恢复匹配的二进制与配置组合，不混用未经验证的 Wire/配置版本。

具体诊断顺序见[故障排查](../../../baseline/v3.0/guides/12.troubleshooting.md)，Admin 与 pprof
用法见[第 10 章](./10.admin-diagnostics-and-pprof.md)，性能口径见
[第 11 章](../../../baseline/v3.0/guides/11.performance.md)。
