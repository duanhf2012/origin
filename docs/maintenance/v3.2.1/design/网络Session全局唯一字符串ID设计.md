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

空字符串是无效 ID。每次建立新的逻辑 Session 都生成新的小写 RFC 9562 UUID v4 文本；
Client 重连不得复用旧 ID。TCP、WebSocket 和 KCP 通过公共 Core 使用同一生成逻辑，不在 ID
中编码 Transport、地址、ServiceName 或业务玩家身份。

本设计只修改 `sysmodule/network` 的业务长连接 Session。Node 服务发现与 RPC 使用的进程
代次 `SessionID uint64` 保持不变，两者职责和 Wire 完全独立。

## 3. 生成与失败语义

实现使用标准库 `crypto/rand` 读取 128 位随机数，设置 UUID v4 的 Version 和 Variant 位，
再编码为 36 字节字符串。生成不依赖时间、机器信息、地址、PID、包级计数器或全局可变状态。

UUID v4 提供 122 位随机空间，是工程意义上的全局唯一，而不是依赖中心协调的数学绝对唯一。
Runtime 在登记前仍检查自己的活动 Map；极端碰撞时重新生成，连续多次碰撞返回 `ErrInternal`。
随机源失败同样返回 `ErrInternal`，不得退化为时间戳、弱随机或局部递增值。

## 4. 性能与内存

随机读取和字符串编码只发生在连接建立冷路径。接收和发送热路径继续直接持有 `*Session`，
不会逐消息生成、解析或复制 ID；业务显式调用 `Server.Session`/`CloseSession` 时才执行字符串
Map 查询。

字符串 Key 比 `uint64` 增加 Map 与 Session 内存，并增加哈希成本，但单端点 Session 上限为
65,536，且查询不是框架消息热路径。验收必须记录 UUID 生成的 `ns/op`、`B/op`、`allocs/op`，
并确认收发基准没有新增逐消息分配。

## 5. 兼容性

这是公开源码不兼容修改：比较零值应从 `id == 0` 改为 `id == ""`，使用 `atomic.Uint64`
暂存 ID 的代码需改为 Channel、互斥保护或字符串所有权结构，格式化应使用 `%s`/`%q`。

框架没有把该 SessionID 写入 TCP、WebSocket 或 KCP Wire，所以不存在协议迁移、灰度互通或
历史数据解析。若业务自行持久化旧数值 ID，需要在升级时清空这些仅对旧进程有效的瞬时连接
索引，不能把旧数值转换成新 UUID。
