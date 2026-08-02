# etcd 服务发现

此示例使用 etcd 保存服务目录，适合已有 etcd 基础设施的环境。业务 Service 与 Origin Provider 示例相同，只替换发现 Provider 的 YAML 配置。

## 前置条件与配置

先运行 `deps-up.bat` 或 `./deps-up.sh`，再用 `check-deps` 验证本机 `2379` 端口。`endpoints` 指向 etcd，`local_network` 选择本地可达端点，`ttl` 控制租约；未配置 `namespace` 时使用 `origin`。

## 运行与观察

执行 `run.bat` 或 `./run.sh`，预期看到其他 Node 的 discovered 日志。结束后运行 `deps-down`，仅关闭本示例 compose 启动的依赖。

## 生产提示

本示例为本地开发配置。生产应使用 HTTPS/TLS、最小权限账号及受控 endpoint，不应把认证信息提交到业务默认 YAML。

对应教程：[服务发现](../../../docs/baseline/v3.0/guides/07-discovery.md)。
