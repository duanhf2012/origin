# KCP 网络纵向切片验收报告

> 日期：2026-08-11
> Windows：Go 1.26.5，windows/amd64
> Ubuntu：Go 1.26.5，linux/amd64，Linux 7.0.0-28-generic
> 结论：KCP Module 与 Service Config 验收通过

## 已验证能力

- 统一 Session/Handler、Server、Client、Dialer、Client 状态与有界重连；
- 1/2/4 字节长度字段和 Big/Little Endian 全组合；
- Raw 双向消息、同 Service 自调用、FEC、AES BlockCrypt 和消息大小限制；
- MTU/加密/FEC 总报文边界、窗口、NoDelay、ACK、写延迟、DSCP 和 UDP Buffer 校验；
- 连接数、消息数、Session/端点字节预算、发送水位、容量拒绝和 Buffer Pool 配平；
- Handler error/panic、读空闲、静默对端重连、重复关闭和确定性停止；
- Server/Client Config 默认值、严格解码、单位转换、非法值与配置驱动 Example；Dialer 仅使用代码
  `DialOptions`，不读取 Service 配置；
- TCP 改用共用长度字段算法后的全仓回归。

## Windows 结果

以下门禁全部通过：

```text
go test ./... -count=1
go test -race ./... -count=1
go vet ./...
KCP 公共层连续 10 轮
KCP 内部层连续 20 轮
容量拒绝与主动关闭连续 100 轮
主动关闭 Race 连续 10 轮
长度帧 Fuzz 5 秒：1,097,857 次执行
```

重点包覆盖率：

```text
internal/kcpnet：76.4%
sysmodule/network/kcp：83.5%
```

未覆盖部分主要是难以稳定制造的操作系统 socket 失败、日志和内部不变量分支。Options/Config、
消息读写、所有权、容量、FEC/加密、生命周期和公共外观由单元、真实集成、Race、Fuzz 与 Ubuntu
复验共同覆盖，不以无意义断言换取覆盖率数字。

Windows 回环 Benchmark（20 次/规格）：

```text
32 B：    143,645 ns/op，  4,821 B/op，  72 allocs/op
256 B：   151,860 ns/op，  4,759 B/op，  72 allocs/op
4 KiB：   181,385 ns/op，  9,061 B/op， 136 allocs/op
64 KiB：  902,365 ns/op， 96,459 B/op，1,430 allocs/op
```

该数据是首轮基线。KCP 库自身分片和可靠传输占主要分配，没有证据支持为降低短基准数字增加公共池、
引用计数或复杂批处理，因此本阶段不扩大性能优化范围。

## Ubuntu 结果

当前工作树上传到独立临时目录 `/tmp/origin-kcp.QAESi3`，依赖以临时 vendor 方式离线提供；没有修改
远端既有仓库或 Go 全局安装。以下门禁全部通过：

```text
go test -mod=vendor ./... -count=1
相关 KCP/TCP/公共帧包 Race
go vet -mod=vendor ./...
KCP 内部与公共层连续 10 轮
配置完成后的 KCP 内部与公共层连续 100 轮
配置完成后的相关 Race 连续 10 轮
容量拒绝连续 100 轮
长度帧 Fuzz 5 秒：1,525,527 次执行
```

弱网使用临时 loopback `netem`，条件为 `80±20ms` 延迟、`5%` 丢包、`10%` 乱序。普通、FEC、
AES+FEC 内部回环及公共 Server/Client/Dialer 服务自调用连续 3 轮通过；测试结束后 qdisc 已恢复为
`noqueue`。该结果支持保留 MTU `1400`、窗口 `1024/1024`、NoDelay `10ms/2` 和 60 秒读空闲默认值。

Ubuntu 回环 Benchmark（20 次/规格）：

```text
32 B：     58,335 ns/op，  6,652 B/op，  74 allocs/op
256 B：    47,055 ns/op，  7,949 B/op，  76 allocs/op
4 KiB：    65,490 ns/op， 10,275 B/op， 138 allocs/op
64 KiB：  522,052 ns/op，101,746 B/op，1,483 allocs/op
```

配置驱动的 `03-kcp-raw-self-call` 真实启动后，Server 与 Client 均收到 `hello through kcp`；SIGINT
停止时 Application、Node 和两个 KCP Session 正常结束，关闭原因均为 `transport closed`。KCP 无远端
握手和无 FIN 的差异已写入 API 注释、使用指南、Example 和重连测试。

## 验收结论与范围

当前 KCP 外观、配置、默认值与文档一致，可以进入 v3.2 后续网络整体收口。弱网数据证明默认值在本次
基线下可用，不代表任何公网拓扑或在线规模下最优；实际项目仍需按消息分布、在线数、心跳和链路质量
压测。当前没有已知未解释失败，也不宣称测试能够数学证明“绝对无 Bug”。
