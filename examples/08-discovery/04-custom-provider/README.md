# 自定义 Provider SPI

示例以一个“Consul 风格”演示 Provider 展现最小替换接口；它不会真正连接 Consul。真正的 Consul Provider 应作为独立包实现相同 SPI，再在 Application 启动前注册。

## 最小职责

Factory 从 `provider.Context` 读取自己的配置并创建 Provider；Provider 负责设置 TTL、发布/撤销本地 Node、把规范化远端快照提交给 Host；框架负责目录更新、事件和 RPC 路由。

## 运行与练习

执行 `run.bat` 或 `./run.sh`，日志会提示自定义 Provider 已就绪。查看 `config/application.yaml` 的 `discovery.type: consul` 与 `consul` 配置块；修改 `address` 为空会触发示例的显式配置校验。

## 边界

不要让业务 Service 直接依赖具体 Consul 客户端。这样将来可替换 Provider，而业务发现查询、监听和 RPC 路由代码保持不变。

对应教程：[服务发现](../../../docs/baseline/v3.0/guides/08.discovery.md)。
