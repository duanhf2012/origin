# 网络 Session 全局唯一字符串 ID 验收报告

> 日期：2026-08-15
>
> 状态：Windows 实现与验收完成
>
> 目标：v3.2.1

## 1. 实现结果

TCP、WebSocket 和 KCP 共用的 `network.SessionID` 已从 Runtime 局部递增 `uint64` 改为
Base64URL 字符串。每条新 Session 使用相对 `2026-01-01T00:00:00Z` 的 32 位秒级循环时间域，
并从 `crypto/rand.Reader` 获取完整 128 位随机数；20 字节整体使用无填充 Base64URL 编码为
固定 27 字符文本，空字符串继续表示无效 ID。

算法已抽取到公开 `util/identifier` 包。其他功能可直接调用 `identifier.NewTimeRandom()`；
需要实例级时钟或随机源的组件可调用 `identifier.NewTimeRandomWith(now, source)`。网络 Runtime
使用后一入口并把返回文本转换为 `network.SessionID`，不再保留私有的 ID 编码实现。

Runtime 不再持有递增计数器。ID 在进入活动 Session Map 前检查碰撞，最多重试 4 次；随机
源失败或连续碰撞返回 `CodeInternal`。停止和容量过载在读取随机源前快速拒绝，生成期间发生
状态变化时在登记锁内再次校验。

Node 服务发现和 RPC 的进程代次 `SessionID uint64`、三种网络 Wire、帧格式、重连、关闭和
Handler 调度语义均未修改。

## 2. 测试与覆盖率

新增和调整的验证包括：

- 固定时间域与随机输入精确生成 `AQIDBAABAgMEBQYHCAkKCwwNDg8`，锁定 27 字符 Base64URL 格式；
- 覆盖 Epoch 前一秒、Epoch、32 位最后一秒与约 136 年回绕；
- `NewTimeRandom` 便捷入口和 `NewTimeRandomWith` 可注入入口均参与大样本去重；
- nil、短读和随机源错误均不产生 ID；
- 16,384 次独立安全随机生成无重复且无空值；
- Runtime 非法参数、停止、容量满、随机失败、碰撞后成功重试和连续碰撞失败；
- TCP、WebSocket、KCP 真实回环均验证 Client/Server 两个独立 Runtime 的 ID 不同，并继续
  通过字符串 ID 查询和关闭 Server Session；
- 公共类型契约固定 `SessionID` 可由字符串常量构造，零值为空字符串。

`util/identifier` 包总语句覆盖率为 100%。`sysmodule/network/internal/core` 当前包总语句覆盖率为
12.9%，低值来自该内部 Core 原先没有独立单元测试、主要由三个外部传输包集成覆盖。
本次重点函数覆盖率为：

| 函数 | 覆盖率 |
| --- | ---: |
| `identifier.NewTimeRandom` | 100% |
| `identifier.NewTimeRandomWith` | 100% |
| `identifier.timeRandomTimestamp` | 100% |
| `Runtime.NewSession` | 92.9% |
| `Runtime.Session` | 100% |
| `Runtime.CloseSession` | 100% |

## 3. Benchmark

环境：Windows amd64，Go 1.27rc2，AMD Ryzen 7 7840HS。命令：

```text
go test ./util/identifier -run '^$' \
  -bench '^BenchmarkNewTimeRandom$' -benchmem -benchtime=1s -count=3
```

三轮结果为 134.2～135.3 ns/op、56 B/op、2 allocs/op。分配由包含时间域的临时字节和最终不可变字符串产生，
只发生在建连冷路径；接收、发送和 Handler 消息路径没有新增 ID 生成、解析、Map 查询或分配。

## 4. 通过的门禁

```text
go test ./util/identifier ./sysmodule/network/... -count=10
go test -race ./util/identifier ./sysmodule/network/... -count=1
go test ./... -count=1
go vet ./...
go build ./...
GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build ./...
go run ./cmd/origingen rpc --check ./...
go mod tidy -diff
git diff --check
```

全部通过。本轮未执行 Linux/macOS 原生网络运行；Linux amd64 已完成无 CGO 交叉构建，三传输
真实连接、关闭和 Race 在 Windows 原生环境通过。

## 5. 迁移提示

业务代码需要把 `id == 0` 改为 `id == ""`，把 `%d` 改为 `%s` 或 `%q`，并停止用
`atomic.Uint64` 暂存网络 SessionID。旧数值 ID 只代表旧 Runtime 的瞬时局部句柄，升级时应
清空相关在线连接表，不能转换或继承为新 SessionID。Base64URL 文本区分大小写，
持久化字段和索引必须使用区分大小写的比较规则。
