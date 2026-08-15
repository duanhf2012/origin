# 网络 Session 全局唯一字符串 ID 实施计划

> 状态：已完成
>
> 目标：v3.2.1
>
> 设计依据：`../design/网络Session全局唯一字符串ID设计.md`

## 1. 实施顺序

1. 把公共 `network.SessionID` 改为字符串，并同步 GoDoc 的全局身份语义；
2. 在网络 Core 增加无全局状态、可注入时钟与 Reader 测试的 32 位秒级时间域加 128 位随机 ID 生成器；
3. 删除 Runtime 局部递增计数器，在登记前生成并检查活动 Map；
4. 迁移 TCP、WebSocket、KCP 测试中的零值、原子暂存和格式化；
5. 增加 Base64URL 格式、时间域 Epoch/回绕、随机源失败、大样本去重和三传输真实连接测试；
6. 运行覆盖率、Benchmark、Race、全仓 test/vet/build 和 Linux 无 CGO 交叉构建。

## 2. 范围保护

- 不修改 Node 服务发现/RPC 的进程 `SessionID uint64`；
- 不修改网络 Wire、帧格式、连接生命周期、重连或关闭语义；
- 不引入第三方 ID 依赖、包级计数器、时间戳 ID 或中心分配器；
- 不在收发热路径增加 ID 解析、字符串拼接、锁或分配。
