# 网络 Session 全局唯一字符串 ID 设计

> 状态：已实现并完成 Windows 验收
>
> 基线：v3.2.0 网络 Module
>
> 目标：v3.2.1
>
> 兼容性：修改公开 `network.SessionID` 的底层类型；不修改 TCP、WebSocket、KCP Wire

## 1. 问题

当前 `network.SessionID` 是 `uint64`，由每个 Server、Client 或 Dialer 独立拥有的
`core.Runtime` 从 1 递增。它只保证同一 Runtime 的活动 Session 不冲突；不同传输、不同
Module、同传输多个端点以及 Runtime 重启后都会产生相同数值。

框架自己的 `Server.Session` 和 `CloseSession` 查询始终限定在具体 Server，因此当前内部
Map 不会误命中。但业务把 ID 单独保存到玩家连接表、事件或跨 Module 索引时，局部数值不再
足以表达连接身份，容易把两个真实连接视为同一个 Session。

## 2. 公共外观

```go
type SessionID string

type Session interface {
    ID() SessionID
}

func (server *Server) Session(id SessionID) (Session, bool)
func (server *Server) CloseSession(id SessionID, cause error) bool
```

生成算法位于可复用的公开工具包：

```go
import "github.com/duanhf2012/origin/v3/util/identifier"

value, err := identifier.NewTimeRandom()
```

`identifier.NewTimeRandomWith(now, source)` 为需要实例级时钟和随机源注入的框架组件提供
同一算法；`identifier.TimeRandomLength` 固定为 27。

空字符串是无效 ID。每次建立新的逻辑 Session 都生成新的固定 27 字符、无填充 Base64URL 文本；
Client 重连不得复用旧 ID。TCP、WebSocket 和 KCP 通过公共 Core 使用同一生成逻辑，不在 ID
中编码 Transport、地址、ServiceName 或业务玩家身份。

本设计只修改 `sysmodule/network` 的业务长连接 Session。Node 服务发现与 RPC 使用的进程
代次 `SessionID uint64` 保持不变，两者职责和 Wire 完全独立。

## 3. 生成与失败语义

算法由 `util/identifier` 单一实现。它把相对 `2026-01-01T00:00:00Z` 的秒数投影到
32 位无符号整数，作为大端序的前 4 字节；
后 16 字节由标准库 `crypto/rand` 提供完整 128 位随机数。20 字节整体通过标准库
`base64.RawURLEncoding` 编码为 27 字符字符串。生成不依赖机器信息、地址、PID、包级计数器
或全局可变状态。

32 位时间域约 136 年循环一次，满足约 100 年的区分需求。时钟回拨、多机器同秒或时间域回绕只会让多个
ID 共享时间域，每个 ID 仍保留完整 128 位随机空间。因此最差情况只退化为纯 128 位随机方案，
不依赖时钟正确性才能保持工程上实际唯一。
Runtime 在登记前仍检查自己的活动 Map；极端碰撞时重新生成，连续多次碰撞返回 `ErrInternal`。
随机源失败同样返回 `ErrInternal`，不得退化为时间戳、弱随机或局部递增值。
Network Runtime 直接调用 `identifier.NewTimeRandomWith` 并把结果转换为 `network.SessionID`，
不复制编码、Epoch 或随机逻辑。

## 4. 性能与内存

时钟读取、随机读取和字符串编码只发生在连接建立冷路径。接收和发送热路径继续直接持有 `*Session`，
不会逐消息生成、解析或复制 ID；业务显式调用 `Server.Session`/`CloseSession` 时才执行字符串
Map 查询。

字符串 Key 比 `uint64` 增加 Map 与 Session 内存，并增加哈希成本，但单端点 Session 上限为
65,536，且查询不是框架消息热路径。验收必须记录 SessionID 生成的 `ns/op`、`B/op`、`allocs/op`，
并确认收发基准没有新增逐消息分配。

## 5. 兼容性

这是公开源码不兼容修改：比较零值应从 `id == 0` 改为 `id == ""`，使用 `atomic.Uint64`
暂存 ID 的代码需改为 Channel、互斥保护或字符串所有权结构，格式化应使用 `%s`/`%q`。

框架没有把该 SessionID 写入 TCP、WebSocket 或 KCP Wire，所以不存在协议迁移、灰度互通或
历史数据解析。若业务自行持久化旧数值 ID，需要在升级时清空这些仅对旧进程有效的瞬时连接
索引，不能把旧数值转换成新 SessionID。Base64URL 文本区分大小写，业务如果存入数据库，
对应字段和索引也必须使用区分大小写的比较规则。ID 包含可推导的约略建连秒数，不得把
SessionID 当作不可猜测的认证凭证或保密字段。
