# 服务发现示例

内置 Origin Provider、状态事件、替换 SPI 和等待服务属于框架教程；etcd 是需要单独部署的第三方
集成。业务代码应只面向发现目录和公共查询接口，而非某个具体 Provider Client。

- [01-origin-provider](./01-origin-provider/README.md)：内置 Origin Provider。
- [02-etcd-provider](./02-etcd-provider/README.md)：etcd Provider，需要本地 etcd。
- [03-watch-and-lost](./03-watch-and-lost/README.md)：发现、Lost 状态事件。
- [04-custom-provider](./04-custom-provider/README.md)：自定义 Provider SPI。
- [05-await-service](./05-await-service/README.md)：等待并查询目标服务。

框架教程：[服务发现](../../docs/baseline/v3.0/guides/08.discovery.md)。第三方教程：
[etcd 服务发现](../../docs/extensions/etcd-discovery.md)。
