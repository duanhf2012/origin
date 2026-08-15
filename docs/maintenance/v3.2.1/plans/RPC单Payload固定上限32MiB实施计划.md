# RPC 单 Payload 固定上限 32 MiB 实施计划

> 状态：已完成
>
> 基线：v3.2.0 Origin RPC
>
> 目标：v3.2.1
>
> 兼容性：线协议和生成代码 ABI 不变；NATS 部署配置需要同步调整

1. 将 `rpc.DefaultMaxPayloadSize` 调整为 32 MiB，并在配置校验中把它明确作为硬上限。
2. 更新 Codec 注释、边界测试和性能基线描述，验证 32 MiB 临界值成功、超一字节失败。
3. 将仓库 Compose NATS `max_payload` 调整为 33 MiB，并让嵌入式集成测试 Broker 覆盖
   默认业务 payload 与最坏包络。
4. 保持外部 NATS 集群测试的 4 MiB 兼容配置，不替部署方擅自假设 Broker 已升级。
5. 执行格式化、单元测试、集成测试、Race、Vet、构建及生成代码一致性检查。
