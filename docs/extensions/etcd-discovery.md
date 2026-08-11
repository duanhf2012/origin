# 使用 etcd 进行服务发现

etcd Provider 适合已经运行 etcd 集群、需要在多个 Origin 进程间共享服务目录的环境。业务 Service
只使用 Origin 的发现查询和事件接口，不直接依赖 etcd Client。

## 本地运行

进入 [`examples/08-discovery/02-etcd-provider`](../../examples/08-discovery/02-etcd-provider/README.md)，
按当前系统依次执行 `deps-up`、`check-deps`、`run` 和 `deps-down` 脚本。示例脚本只管理本示例的本地
依赖，不会配置生产集群。

## 最小配置

```yaml
discovery:
  type: etcd
  etcd:
    endpoints:
      - http://127.0.0.1:2379
    local_network: game-partition-1
    ttl: 5s
    request_timeout: 3s
```

`local_network` 是当前配置下所有 Node 的注册网络。未配置 `namespace` 时使用 `/origin`。业务侧的
`labels`、`allow_discovery`、监听器和查询 API 与 Provider 无关，继续阅读
[服务发现](../baseline/v3.0/guides/08.discovery.md)。

## 读取其他网络

```yaml
discovery:
  type: etcd
  etcd:
    endpoints: [http://127.0.0.1:2379]
    local_network: game-partition-1
    watch_networks:
      - game-partition-2
```

`watch_networks` 只增加读取范围，不会把当前 Node 注册到其他网络。去重后的本地与监听网络总数最多
为 64；名称使用小写 kebab-case。

## 生产连接

HTTPS endpoint 会启用 TLS。生产环境应使用受信任 CA、最小权限认证，并在需要时配置双向 TLS：

```yaml
discovery:
  type: etcd
  etcd:
    endpoints:
      - https://etcd-1.example.com:2379
      - https://etcd-2.example.com:2379
    namespace: /origin/game-prod
    local_network: game-partition-1
    auth:
      username: ${ETCD_USERNAME}
      password: ${ETCD_PASSWORD}
    tls:
      ca_file: certs/etcd-ca.pem
      cert_file: certs/client.pem
      key_file: certs/client-key.pem
      server_name: etcd.example.com
      insecure_skip_verify: false
```

用户名和密码必须同时配置；也可以改用 Token，但不能与用户名/密码同时使用。相对证书路径以配置
目录为基准解析。不要在生产环境使用 HTTP endpoint 或跳过证书验证。

## 使用边界

- Provider 负责发布、租约和权威快照，Origin 负责筛选、事件顺序和 RPC 路由；
- Provider 短暂断线时会进入恢复状态并保留最后快照，超过 TTL 后才报告 Lost；
- endpoint、认证、TLS、etcd 容量和备份由部署系统负责；
- 需要接入 Consul 等其他系统时，实现独立的
  [Provider 扩展](../../examples/08-discovery/04-custom-provider/README.md)，不要在业务 Service 中直接维护第二份目录。
