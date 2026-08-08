# etcd 服务发现

此示例使用 etcd 保存服务目录，适合已有 etcd 基础设施的环境。业务 Service 与 Origin Provider 示例相同，只替换发现 Provider 的 YAML 配置。

## 前置条件与配置

先运行 `deps-up.bat` 或 `./deps-up.sh`，再用 `check-deps` 验证本机 `2379` 端口。`endpoints` 指向 etcd，`local_network` 选择发现数据所属和监听的网络分区，`ttl` 控制租约；未配置 `namespace` 时使用 `origin`。

如需读取其他网络，可以在 `discovery.etcd` 中增加：

```yaml
watch_networks:
  - cn-north
```

它只增加读取范围，不改变当前 Node 的发布网络；当前示例没有启动 `cn-north` Node，因此默认配置不包含该字段。跨网络演示和配置边界见[服务发现教程的“读取其他网络”](../../../docs/baseline/v3.0/guides/08.discovery.md#深入一点读取其他网络)。

配置中的 `labels: {region: cn-east}` 表示 Node 向服务发现发布自己的区域，等价于：

```yaml
labels:
  region: cn-east
```

本示例只演示最常用的区域标签。其他 Node 可以使用 `allow_discovery.node_labels.region` 筛选它；当前示例没有配置 `allow_discovery`，因此仍按默认规则发现 Provider 范围内的全部公开 Service。`region` 是业务约定的标签名，不是框架强制字段，也不会与 `discovery.etcd.local_network` 自动关联。

自定义标签、多个候选值和多条筛选规则的完整说明见[服务发现教程](../../../docs/baseline/v3.0/guides/08.discovery.md#深入一点自定义标签键)。

## 运行与观察

执行 `run.bat` 或 `./run.sh`，预期看到其他 Node 的 discovered 日志。结束后运行 `deps-down`，仅关闭本示例 compose 启动的依赖。

## 生产提示

本示例为本地开发配置。生产应使用 HTTPS/TLS、最小权限账号及受控 endpoint，不应把认证信息提交到业务默认 YAML。

对应教程：[服务发现](../../../docs/baseline/v3.0/guides/08.discovery.md)。
