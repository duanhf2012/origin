# TCP 网络首批纵向切片变更记录

> 日期：2026-08-10
> 状态：实现与本地/Ubuntu 验收完成

本切片新增统一 `network.Session`/`Handler`/容量与统计外观、Raw 传输、PB/JSON Router、自定义
Codec，以及 TCP Server、Client、Dialer。WebSocket、KCP 和 Gin 不在本切片实现范围。

主要变更：

- Buffer Pool 增加容量查询、容量内 Resize、独占 Slice 接管和保留容量计算；
- 新增无队列语义的原子 `bytebudget`；
- TCP 长度帧支持 1/2/4 字节 Big/Little Endian；
- 发送队列改为惰性 Ring，增加每连接消息/字节、Module 总字节、80%/50% 水位和慢连接关闭；
- 网络 Runtime 不建立第二条入站队列，完整 Raw Buffer 预留额度后直接进入 Service Scheduler；
- Raw `Send` 保持安全复制，PB 直接编码到最终 Buffer；JSON 使用标准库 Marshal 后复制到最终池化
  Buffer，保留安全所有权，后续只有 Benchmark/Profile 证明必要时才扩大 Encoder API；
- Client 默认不重连；开启后使用单一 Worker、有界指数退避、抖动和停止取消；
- Dialer 明确为一次性能力，调用方负责在 owner Service 停止前关闭 Session。

实现中额外修正了一个配置边界：容量预算按 Buffer 保留容量记账，因此校验也按相同档位判断，
避免 17 字节消息在 17 字节队列配置下“配置合法但永远无法发送”。

最终 Race 门禁还发现 Listener 的拒绝计数晚于 socket 关闭发布：客户端可能先观察到断开，却仍读到
旧统计值。实现已调整为先提交 `RejectedConnections`、再关闭 socket，并通过 100 次重复测试固定。

## 2026-08-11 Service 配置补充

- 新增独立 `ServerConfig`、`ClientConfig` 和完整默认值；Dialer 是一次性代码对象，只使用
  `DialOptions`，不进入 Service 配置；
- 配置使用带单位 `Duration`/`ByteSize`，通过 `Options(handler)` 在启动冷路径转换并复用原校验；
- Client/Dialer 固定单 Session；Client 配置不公开冗余连接数和端点总预算；
- 新增默认 10s 的单次 TCP `DialTimeout`，调用方 Context 更早到期时仍优先生效；
- 自调用 Example 改为从所属 Service 严格读取 `tcp.server`/`tcp.client`，未知字段直接阻止启动；
- 补全 TCP Config、Options 和 Example 的中文字段及执行步骤注释。
