# Ubuntu systemd 最小 Unit

`origin-tutorial.service` 是将 Origin 二进制交给 systemd 监管的最小参考，不应不经修改直接用于生产。它假设二进制和配置分别位于受控目录，并由非 root 账户运行。

## 部署步骤

1. 用 `../01-build-and-run` 构建二进制，复制到 `/opt/origin-tutorial/hello-service`。
2. 将 YAML 复制到 `/etc/origin-tutorial/application.yaml`。
3. 创建非 root 的 `origin` 用户，并调整两个目录所有者。
4. 复制 Unit 到 `/etc/systemd/system/origin-tutorial.service`。
5. 执行 `sudo systemctl daemon-reload` 和 `sudo systemctl enable --now origin-tutorial`。
6. 使用 `journalctl -u origin-tutorial -f` 查看启动与运行日志。

## 上线前检查

确认 Unit 中的路径、用户、Node 参数和配置路径与实际部署一致。生产还应补齐环境变量管理、日志轮转、网络 ACL、TLS/认证、外部依赖健康检查及升级/回滚策略。

对应教程：[部署与运维](../../../docs/baseline/v3.0/guides/10-deployment-and-operations.md)。
