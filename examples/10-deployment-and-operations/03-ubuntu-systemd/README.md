# Ubuntu systemd 最小 Unit

`origin-tutorial.service` 是最小参考，而不是可以不经修改直接用于生产的文件。

部署步骤：

1. 用 `01-build-and-run` 构建二进制并复制到 `/opt/origin-tutorial/hello-service`。
2. 将 YAML 复制到 `/etc/origin-tutorial/application.yaml`。
3. 创建非 root 的 `origin` 用户并调整目录所有者。
4. 复制 Unit 到 `/etc/systemd/system/origin-tutorial.service`。
5. 执行 `sudo systemctl daemon-reload`、`sudo systemctl enable --now origin-tutorial`。
6. 通过 `journalctl -u origin-tutorial -f` 查看日志。

生产环境应补充环境变量、日志轮转、网络 ACL、TLS/认证和外部依赖健康检查。
