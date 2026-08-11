# KCP 网络纵向切片实施计划

> 目标版本：Origin v3.2
> 状态：实施与验收完成
> 实施顺序：TCP、WebSocket 及其配置验收之后，KCP Service Config 之前
> 设计依据：[`Origin 网络模块核心设计`](../design/Origin网络模块核心设计.md)
> 外观基线：当前已经实现并人工确认的 `sysmodule/network`、`tcp` 与 `websocket`

## 1. 范围与原则

1. 本切片实现 `sysmodule/network/kcp` 的 Server、Client、Dialer、运行时 Options、示例、测试和
   双平台验收；不实现 KCP Service Config。只有运行时参数和默认值通过真实弱网、容量与稳定性
   验证后，下一切片才允许固化 KCP Config。
2. 当前代码中的公共外观是实现基线。设计文档与代码不一致时，公共 `Session`、`Handler`、
   `Server`、`Client`、`Dialer` 形状以当前代码为准；必须改变公共外观时先修订设计，不建立临时
   兼容层。
3. KCP 复用已经通过 TCP/WebSocket 验证的 Runtime、Buffer Pool、端点容量、消息所有权、Service
   串行回调、Client 状态和有界重连，不另建业务消息队列或事件体系。
4. 只实现当前必需的 KCP 能力：长度帧、大小端、MTU、窗口、NoDelay、ACK NoDelay、Write Delay、
   FEC、DSCP、UDP Socket Buffer 和代码注入的 `BlockCrypt`。Stream Mode 固定开启；废弃 DUP、动态
   热更新和静态密钥配置不进入外观。
5. 设计与性能优化均控制范围。长度头和 Payload 使用 KCP `WriteBuffers` 一次提交，不拼接完整消息；
   其他优化必须由 Benchmark/Profile 证明，不为理论上的零拷贝增加公共池或引用计数。

## 2. 包边界

```text
internal/lengthframe       TCP/KCP 共用的无符号长度字段编解码小算法
internal/kcpnet            KCP Session、Listener、单次 Dial、队列和 socket 参数
sysmodule/network/kcp      公共 Options、Server、Client、Dialer 与 Runtime 适配
examples/13-network        使用 Service 自调用的 KCP 最小示例
```

`internal/kcpnet` 只负责传输 I/O、帧、队列、超时和资源生命周期。业务回调仍经公共 Runtime 投递到
所属 Service；KCP 包不能绕过 Runtime 直接调用使用者 Handler。

## 3. 实施顺序

1. [x] 引入并锁定 `kcp-go/v5` 依赖，记录其许可证和版本；
2. [x] 抽取最小长度帧算法，保持 TCP 行为和测试不变；
3. [x] 实现 KCP Options 默认值、复制规则及创建 socket 前的严格校验；
4. [x] 实现 KCP Conn 的单 Reader、单 Writer、长度帧、所有权、背压和确定性关闭；
5. [x] 实现 Listener、单次 Dial、连接准入以及 KCP/UDP 参数应用；
6. [x] 接入公共 Runtime，完成 Server、Client、Dialer 与有界重连；
7. [x] 补齐单元、Fuzz、共同契约、服务自调用、重连、FEC/加密和真实 UDP 集成测试；
8. [x] 完成 Windows 全仓、Race、Vet、覆盖率和 Benchmark 门禁；
9. [x] 在 Ubuntu 执行真实 KCP、服务自调用、Race、弱网、资源泄漏与稳定性验证；
10. [x] 根据实测结果复核 Options/default，判定 KCP Module 里程碑通过；之后才实现 KCP Config。

## 4. 参数门禁

- 长度字段只允许 1、2、4 字节，Big/Little Endian 均需互通；消息上限不能超过字段表达范围；
- MTU 必须在 `50..1500`，避免 `kcp-go` 对小值静默忽略或超过内部报文缓冲；
- 窗口必须为正数并能由当前实现稳定表达；NoDelay 更新间隔必须为整毫秒且位于 `10ms..5s`；
- FEC 只允许 `0/0` 关闭或两个正数组合，组合还必须通过 Reed-Solomon 构造校验；
- DSCP 只允许六位值 `0..63`；Socket Buffer 为零时保留 OS 默认值，不能为负数；
- KCP 默认读空闲为 60 秒。KCP 拨号只创建本地 UDP Session，不代表对端业务已经应答；教程、测试
  和错误语义必须明确这一点。

## 5. 必测边界

- Server、Client、Dialer 的双向 Raw 消息、同 Service 自调用和确定性停止；
- 1/2/4 字节长度字段与 Big/Little Endian 全组合、空消息、边界消息、超大声明及截断帧；
- OnOpen/Message/Writable/Close 顺序、恰好一次、Handler error/panic 和并发重复 Close；
- 消息数、单 Session 字节、端点总字节过载，80%/50% 水位和慢连接关闭；
- 初始拨号失败、Context 取消、连接静默、读写超时、有界重连和停止中退避；
- MTU、窗口、NoDelay、FEC、DSCP、Socket Buffer 的非法配置和真实应用；
- 无加密、匹配/不匹配 `BlockCrypt`，FEC 关闭/开启及丢包、延迟、乱序、抖动环境；
- 启动失败和停止后的 UDP 端口、Session、goroutine、队列、预算及 Buffer Pool 配平。

重点核心路径以可达语句和分支接近 100% 为目标；平台上不能稳定制造的底层错误使用故障注入、
Race、Fuzz 和 Ubuntu 真实集成补证，不用无意义断言换覆盖率。

## 6. 验收门禁

- Windows 与 Ubuntu 的 `go test ./... -count=1`、相关包 Race、`go vet ./...` 全部通过；
- KCP Options、Conn、Listener、公共适配层和共同契约测试通过，重点包覆盖率已记录；
- KCP Framer Fuzz、必要 Benchmark 和弱网数据完成，无未解释回退；
- Server、Client、Dialer、FEC、加密和同 Service 自调用在 Ubuntu 真实 UDP 环境通过；
- 示例可独立构建运行，关键参数和业务代码具有面向使用者的简洁中文注释；
- 文档、实现和测试一致，工作树只包含本切片改动；本门禁通过前不得实现 KCP Config。

## 7. 执行结论

2026-08-11，以上门禁全部通过。Ubuntu 弱网条件为 `80±20ms` 延迟、`5%` 丢包、`10%` 乱序，
默认 MTU、窗口、NoDelay 和 60 秒读空闲无需调整；随后才实现独立 KCP Service Config。详细结果见
[`KCP 网络纵向切片验收报告`](../reports/KCP网络纵向切片验收报告.md)。
