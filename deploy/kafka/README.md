# Origin Kafka 开发与验收环境

本目录提供一个持久化的 Apache Kafka 4.3.1 单节点 KRaft 环境，用于 Origin v3.2 的教程、Windows/Ubuntu 协议测试和故障恢复测试。它不是生产集群拓扑。

## 启动

```bash
cd deploy/kafka
docker compose up -d
./create-topics.sh
docker compose ps
```

默认外部连接地址为 `192.168.8.3:9092`，Docker 网络内连接地址为 `origin-kafka:19092`。如果 Ubuntu 地址或端口不同，复制 `.env.example` 为 `.env` 后修改；`KAFKA_ADVERTISED_HOST` 必须是客户端真正可达的地址，不能在跨主机测试中写 `localhost`。

## 测试

Windows PowerShell：

```powershell
$env:ORIGIN_KAFKA_BROKERS='192.168.8.3:9092'
go test ./sysmodule/kafkamodule -run TestIntegration -count=1
```

Ubuntu：

```bash
ORIGIN_KAFKA_BROKERS=192.168.8.3:9092 \
  go test ./sysmodule/kafkamodule -run TestIntegration -count=1
```

集成测试不会自动创建 Topic；`create-topics.sh` 会显式创建 Raw、JSON、PB、Consumer、Recovery 和 compacted Topic，以验证 `auto.create.topics.enable=false`。

## 数据与停止

Kafka 数据保存在命名卷 `origin-kafka-data`。普通临时停止使用 `docker compose stop`，再次启动使用 `docker compose start`。本验收流程不会执行 `docker compose down`，也不会删除容器、Network 或 Volume。

当前 Listener 是局域网明文，仅适合可信开发网络。生产必须使用多 Broker、TLS、SASL/ACL、监控、容量规划和独立数据盘。

这台双机验收环境的 Windows 与 Ubuntu 时钟曾相差约 26 小时，因此 Compose 仅为开发测试把 `log.message.timestamp.after.max.ms` 放宽到 48 小时。生产不要复制这个容差，应使用 NTP 同步 Producer 与 Broker；Kafka 4.3 默认只允许 CreateTime 最多领先 Broker 1 小时，这能及时暴露错误时钟。
