# Origin 覆盖率、Example 与教程最终验收报告

> 日期：2026-08-10
> 范围：全面复审 Task 7
> 结论：通过；生产代码保持冻结，可以进入独立终审

## 1. 验收原则

本轮只补充不改变生产行为的测试，并收口 Example、教程和脚本。公开外观以当前代码及
`tests/contracts` 的人工确认结果为准；设计文档与代码不一致时，不根据旧文档反向修改当前
使用者外观。覆盖率用于发现风险，不以机械达到总百分比为目标，也不为极端防御分支增加生产
注入点、兼容层或重复实现。

服务自己调用自己的 RPC 被列为重点功能：既检查普通 goroutine 调用，也检查 Service Task 内
调用、回调顺序、执行槽释放和死锁边界，并纳入 Windows、Ubuntu 全仓 Race 与最终发布门禁。

## 2. 覆盖率与测试结论

最终普通全仓 Profile 的原始语句覆盖率为 `72.6%`。该数字包含大量独立 `main` Example、命令
入口和测试辅助包，不能代表框架生产代码风险。审计按逐包、逐文件、逐函数展开，并取单元测试、
RPC/NATS 真实集成、Node/Service、Origin Provider 等七组 Profile 的函数最大覆盖结果：

- 排除 Example、测试夹具、生成文件和测试辅助包后，共复核 `1,672` 个生产函数；
- Windows Profile 中有 `17` 个函数为零覆盖；Ubuntu 已实测其中两个文件链接边界函数，因此
  跨平台剩余 `15` 个；
- `admin 97.1%`、`application 89.7%`、`diagnostics 100%`、`config 80.6%`、`node 79.7%`、
  `service 81.7%`、`internal/discovery 86.6%`、`internal/rpcgen 86.9%`、`internal/tcpnet 91.0%`；
- `rpc` 的包内普通覆盖率为 `59.1%`，正式生成客户端、TCP/NATS 和系统连接路径另由跨包集成
  Profile 覆盖，不能只读取该包的单一百分比；`internal/natsnet` 同理由真实 NATS 集成补足。

本轮补充的关键证据包括：

- Service/Module 配置与发现委托、Node 生命周期状态、失败状态、Timer 位置和远端路由；
- 生成客户端的本地自调用 `Call/Await/Async/Notify`、FIFO 回调，以及 Task 内同步 `Call` 只能
  由 Deadline 解除的边界；同时验证未 Prepare 的低层本地/NATS Notify；
- TCP 空闲心跳后继续 RPC、NATS Reply Subject、系统 Peer 主动关闭、NATS 全 Peer 断开；
- RPC Deadline/Context/Codec/Wire、晚到响应、退避上限和关闭错误归一化；
- Origin 快照过期堆、Provider 容量、Directory/Snapshot、配置数值边界和日志丢弃计数。

剩余零覆盖函数均已逐项分类：必需接口的空实现或元数据占位；第三方 NATS Lame Duck/订阅终态
归一化回调；生成器不支持类型的错误格式化；损坏内部状态和不可能编码状态防御；发送成功前
回滚、过期推进等没有稳定公共注入点的极窄竞态。`rpc.natsRuntime.sendRequest` 是当前 Prepare
前置条件下不可达的低层请求分支。上述分支均不属于正常或重点功能缺口，不通过扩大生产设计来
追求数字。所有新增测试在 Windows 和 Ubuntu 的普通、定向重复或 Race 门禁中通过。

## 3. Example、脚本与教程

仓库包含 `42` 个带 `main.go` 的 Example 目录，其中 `40` 个是当前学习路径，`2` 个是冻结的
v3.0 基线。每个目录都具备 README、Windows 脚本和 Linux 脚本，并由全仓 Build/Test 覆盖编译。

Ubuntu 隔离环境实际执行了 `38` 个无需外部依赖的当前场景；另执行 NATS、etcd、Local/TCP/NATS
性能脚本、RPC 超时、非法配置、Discovery Lost 和 Diagnostics 收集 `9` 项，共 `47` 个实际运行
场景，全部得到预期业务输出并完成停止、清理。NATS 教程使用测试自有 Broker、独立端口和唯一
namespace；etcd 使用唯一 network，没有修改或停止机器上既有 NATS/etcd 容器。

Linux 脚本收口结果：

- `62` 个跟踪的 `.sh` 全部设置 Git 可执行位；
- `.gitattributes` 固定 Shell 脚本为 LF；全部脚本无 CRLF；
- Ubuntu 对全部 `62` 个脚本执行 `bash -n`，无语法错误；
- 实跑直接向构建后的示例二进制发送 SIGINT，确认应用自身完成优雅停止。

教程按 00～12 使用者顺序复核。当前学习入口统一指向 v3.1 Admin/Diagnostics 教程；API 索引
按当前外观重写，明确普通项目使用生成 RPC Client，不直接依赖 `rpc.Runtime/Reader/Writer/Sizer`。
示例注释覆盖执行目的、生命周期、并发归属、错误处理和清理；删除重复历史过程与已经失效的
公开调用说明。最终 Markdown 检查覆盖 `188` 个文件、`708` 个本地链接，断链为零（忽略
fenced/inline code）。

## 4. 最终双平台门禁

| 环境 | Test | Vet | 生成检查 | Build | Race |
| --- | ---: | ---: | ---: | ---: | ---: |
| Windows 11 / Go 1.26.5 | 通过，72.2s | 通过，25.9s | 通过，8.1s | 通过，21.3s | 通过，94.8s |
| Ubuntu 26.04 / Linux 7.0 / Go 1.26.5 | 通过，32s | 通过，1s | 通过，2s | 通过，9s | 通过，45s |

Windows 普通四项并行执行，因此各项时间只表示本次可重复执行证据，不用于性能比较。Ubuntu
使用受控并发串行门禁。两端均无失败、Race、生成漂移或构建缺口。

## 5. 结论

Task 7 没有发现新的生产缺陷、公开外观冲突、未解释关键覆盖缺口或不可执行教程。唯一生产代码
简化是删除确认无任何引用的私有 `protocolError`；其余修改限于测试、教程、Example 注释和脚本
可执行性。测试覆盖、Example 和教程完成门禁满足，生产代码继续冻结，允许进入 Task 8 独立终审。
