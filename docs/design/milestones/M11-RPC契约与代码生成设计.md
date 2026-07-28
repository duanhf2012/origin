# M11 RPC 契约与代码生成设计

> 文档状态：讨论中  
> 创建日期：2026-07-27  
> 最后更新：2026-07-28
> 当前结论：已确认部分作为后续讨论基线；完成全部开工 Review 前不得实施

## 1. 文档目的

M11 建立 Origin v3 第一套可实际调用的 RPC 契约、代码生成和同 Node 执行闭环。

本里程碑只解决以下问题：

1. 业务如何使用 Go 接口声明 RPC；
2. `origingen` 如何发现、校验并生成客户端和 Dispatcher；
3. ContractID、MethodID 和契约指纹如何稳定生成；
4. Service 实现如何在不编写注册样板代码的情况下自动绑定 Dispatcher；
5. 生成客户端如何使用统一目标对象表达“按 Service 选择”和“精确 Node + Service”；
6. 同一 Node 中的 RPC 如何经过真实编解码、Service 调度和 Dispatcher 完成闭环；
7. M11 支持的数据类型、错误、性能和测试边界。

M11 不是完整网络 RPC 里程碑。普通 Go 结构体静态编解码、TCP、NATS、服务发现和完整
停止分别由 M12～M15 及后续独立里程碑实现。

## 2. 设计依据

M11 必须同时遵守：

- [开发指导原则](../../../AGENTS.md)；
- [里程碑路线图](./里程碑路线图.md)；
- [里程碑设计文档复核清单](./里程碑设计文档复核清单.md)；
- [RPC 接口与调用语义设计](../details/2026-07-23-远程调用接口与调用语义设计.md)；
- [RPC 数据类型与序列化设计](../details/2026-07-23-远程调用数据类型与序列化设计.md)；
- [单目标 RPC 客户端与路由设计](../details/2026-07-24-单目标远程调用客户端与路由设计.md)；
- [Service 协作式调度设计](../details/2026-07-23-服务协作式调度设计.md)；
- [统一错误码设计](../details/2026-07-24-统一错误码设计.md)；
- [内存复用与对象池设计](../details/2026-07-25-内存复用与对象池设计.md)。

详细设计描述完整目标能力；本文负责裁剪 M11 实际实施范围。两者发生范围歧义时，以本文
和最新路线图规定的当前里程碑边界为准，不把 M12 之后的能力提前带入 M11。

## 3. 已确认的核心原则

1. Origin Native RPC 使用带 `//origin:rpc` 标记的 Go 接口作为唯一业务契约；
2. 业务只手写 RPC 接口和 Service 方法实现；
3. 客户端、静态编解码入口、Dispatcher、描述信息和 Service 适配代码均由
   `origingen` 生成；
4. 生成器命令名固定为 `origingen`；
5. 一个 Service 可以不公开 RPC，但最多实现一个公开 RPC 契约；
6. 同一个 RPC 契约可以由多个不同 Service 类型实现；
7. RPC 接口首版不支持嵌入其他接口，也不支持跨 Service 聚合；
8. 运行热路径不使用反射、字符串方法查找或通用 `[]any` 参数；
9. M11 同 Node 调用也必须经过编码、队列、Dispatcher 和解码；
10. 完全无返回值的方法在 M11 只生成 `NotifyXxx`；带任意返回值的方法生成
    `AsyncXxx`、`AwaitXxx` 和 `NotifyXxx`；所有方法的 `BroadcastXxx` 延后；
11. 所有生成调用最终都具有统一 `error` 语义；
12. RPC 与通用 Await 共用唯一默认超时配置，不再增加 `rpc.default_timeout`；
13. 超时优先级固定为：
    `调用方显式 Deadline > Service.SetDefaultAwaitTimeout > Node scheduler.default_await_timeout > Origin 内置 15s`；
14. 一次 Await 或 RPC 调用只计算一个有效 Context、一个有效 Deadline，并且只登记一个
    逻辑计时器；生成的 `AwaitXxx` 不重复附加 RPC 默认超时；
15. Async 只使用 Context 取消，不额外公开取消句柄；
16. Notify 只在编码、路由和本地发送接受前检查 Context；接受后不创建 pending 或超时项，
    也不能撤回已经接受的消息；
17. 客户端不持有 TCP/NATS 连接，目标只描述逻辑路由；
18. 客户端统一使用一个构造函数和一个具体 `rpc.Target` 值对象；
19. ContractID 和 MethodID 使用生成期 SHA-256 截断值，发现碰撞时生成失败；
20. 完整 SHA-256 契约指纹保留给兼容性检查和后续 Node 握手；
21. 不允许手工覆盖 ContractID 或 MethodID。
22. 每个 Node 创建并独占一个 `rpc.Runtime`，但 RPC 的业务目标始终是 Service；
23. 生成客户端只在构造冷路径取得一次 Runtime，不在调用热路径查询或断言；
24. Dispatcher 使用静态方法分派和静态编解码函数，不建立公共 Codec 接口；
25. M11 只支持 Running 阶段的本地 RPC；`OnStart`、`OnStop` 的生命周期 Await 基础进入
    M15，`AwaitService`、`AwaitNodeService` 随后在服务发现里程碑接入；
26. 首版不公开 `AwaitManaged` 或第二套 Await API。

## 4. M11 交付范围

### 4.1 必须实现

- `cmd/origingen` 命令入口；
- `internal/rpcgen` 生成器实现；
- 公开 `rpc` 包的最小类型和生成代码调用边界；
- 每个 Node 独占的 `rpc.Runtime` 及 Service Runtime 窄桥接；
- 模块级 `origingen rpc ./...` 扫描；
- `//origin:rpc` 契约发现和完整签名校验；
- ContractID、MethodID 和契约指纹生成；
- 强类型客户端；
- `rpc.Target`、`rpc.ToService` 和 `rpc.ToServiceOnNode`；
- Service 实现自动识别；
- 静态 Dispatcher 和 Service 适配方法；
- Node 内本地 Dispatcher 注册；
- 同 Node Async、Await 和 Notify；
- 顶层 Protobuf、基础标量、字符串和 `[]byte` 编解码；
- 生成确定性、碰撞、非法声明、调度、错误和性能测试。

### 4.2 明确延后

| 能力 | 归属 |
|---|---|
| 普通指针、数组、Slice、Map、普通结构体和嵌套 Protobuf | M12 |
| 稳定结构体字段 ID、自定义静态 Codec 和结构体兼容性 | M12 |
| RPC 线协议、RequestID、pendingCall、连接管理和 TCP | M13 |
| NATS RPC Transport | M14 |
| 生命周期 Await 基础、完整 Stop 排空、OnStop Await RPC 和异常进程收尾 | M15 |
| Origin/etcd 服务发现、关注筛选和退休状态 | M15 后独立里程碑 |
| RoundRobin、Rand、ModKey、自定义路由和 Broadcast | 服务发现之后独立里程碑 |
| 外部 gRPC 插件 | M15 后独立里程碑 |
| 流式 RPC | 首版不支持，有真实需求后重新设计 |
| RPC 压缩 | 基准证明有净收益后单独立项 |

### 4.3 首版明确禁止

- RPC 接口嵌入；
- 一个 Service 实现多个公开 RPC 契约；
- 跨 Service 聚合客户端；
- 可变参数；
- 多个 `context.Context`；
- `error` 出现在返回值中间或出现多次；
- 运行时反射注册；
- 字符串 `"Service.Method"` 查找；
- 隐式 JSON 回退；
- 手工指定或覆盖 ContractID、MethodID；
- 生成代码通过包级 `init()` 修改全局注册表。

这些限制是为保持契约单一、运行行为明确和实现精简而作出的首版决定，不属于功能遗漏。

## 5. 业务使用外观

### 5.1 手写 RPC 契约

业务在普通 Go 包中声明：

```go
package game

import (
    "context"

    "example.com/project/pb"
)

//origin:rpc
type PlayerRPC interface {
    GetPlayer(
        ctx context.Context,
        playerID int64,
    ) (*pb.Player, error)

    SavePlayer(
        ctx context.Context,
        player *pb.Player,
    ) error

    PlayerOnline(
        ctx context.Context,
        playerID int64,
    )
}
```

`//origin:rpc` 必须紧邻具名接口声明。相同接口不能重复标记，接口不能使用类型别名绕过
校验。

### 5.2 手写 Service 实现

业务只实现原始方法：

```go
type PlayerService struct {
    service.Service
}

func (s *PlayerService) GetPlayer(
    ctx context.Context,
    playerID int64,
) (*pb.Player, error) {
    return s.repository.Load(ctx, playerID)
}

func (s *PlayerService) SavePlayer(
    ctx context.Context,
    player *pb.Player,
) error {
    return s.repository.Save(ctx, player)
}

func (s *PlayerService) PlayerOnline(
    ctx context.Context,
    playerID int64,
) {
    s.online[playerID] = struct{}{}
}
```

业务不手写：

- RPC 注册函数；
- ContractID 或 MethodID；
- Dispatcher；
- `AsyncGetPlayer`、`AwaitGetPlayer`、`NotifyGetPlayer`、`NotifyPlayerOnline`；
- `RPCDispatcher()` 适配方法；
- `init()` 注册代码。

### 5.3 生成客户端

生成器概念上生成一个客户端类型和一个构造函数：

```go
type PlayerRPCClient struct {
    // 真实字段由生成器和 rpc 包共同确定。
}

func NewPlayerRPCClient(
    owner service.IService,
    target rpc.Target,
) PlayerRPCClient
```

按 Service 选择目标：

```go
client := game.NewPlayerRPCClient(
    s,
    rpc.ToService("PlayerService"),
)
```

精确指定 Node 和 Service：

```go
client := game.NewPlayerRPCClient(
    s,
    rpc.ToServiceOnNode("player-2", "PlayerService"),
)
```

两种写法返回相同的 `PlayerRPCClient`。目标差异属于数据，不通过第二个客户端类型或第二组
生成方法表达。

### 5.4 生成调用方法

带返回值的方法生成 Await、Async 和 Notify 三种调用外观：

```go
player, err := client.AwaitGetPlayer(ctx, playerID)

err = client.AsyncGetPlayer(
    ctx,
    playerID,
    func(
        callbackCtx context.Context,
        player *pb.Player,
        err error,
    ) {
        // 回调取得调用方 Service 执行权后才会运行。
    },
)

err = client.NotifyGetPlayer(ctx, playerID)
```

`AsyncGetPlayer` 的直接返回 `error` 只表达编码、目标校验、路由和队列准入等立即提交失败；
直接返回非 nil 时不创建本地完成状态，也不再投递回调。直接返回 nil 后，回调必须且只能
执行一次，并通过回调末尾 `error` 表达业务错误、超时、取消和后续框架错误。

`NotifyGetPlayer` 明确表示调用方主动放弃 `GetPlayer` 的业务结果和远端业务错误，只保留
本地接受阶段的最终 `error`。完全没有返回值的方法只生成 Notify：

```go
err := client.NotifyPlayerOnline(ctx, playerID)
```

M11 不生成：

- `AwaitNodeGetPlayer`；
- `AsyncNodeGetPlayer`；
- `NotifyNodePlayerOnline`；
- `BroadcastPlayerOnline`。

目标由 `rpc.Target` 决定，不能把 NodeID、ServiceName 重复加入每个 RPC 方法。

Broadcast 不在 M11 生成，但后续为所有 RPC 方法生成 `BroadcastXxx`。它和 Notify 一样
主动放弃远端业务结果，只返回本地发现、编码和投递阶段的 `error`。标准广播使用客户端
Target 的基础范围：

- `ToService("AService")` 表示当前发现快照中全部同名、契约匹配且可路由的
  `AService`；
- `ToServiceOnNode("node1", "AService")` 的范围最多只有该 Node 上的一个实例；
- 单目标 `RouteRoundRobin`、`RouteRandom`、`Route(key)` 和自定义 Selector 不缩小广播
  范围；
- 同一契约由其他 ServiceName 实现时不属于本次广播目标；
- 业务需要按区服、标签或其他业务条件广播子集时，可以读取稳定发现快照后逐个使用精确
  Target 调用 `NotifyXxx`，但标准全范围广播不要求业务重复实现目标筛选和投递。

## 6. 单客户端与 Target 设计

### 6.1 公开外观

`rpc.Target` 是具有不可变使用语义的具体小型值对象：

```go
package rpc

type Target struct {
    // 字段保持未导出。
}

func ToService(serviceName string) Target

func ToServiceOnNode(
    nodeID string,
    serviceName string,
) Target
```

Target 只保存：

- 目标模式；
- 可选 NodeID；
- ServiceName。

Target 不保存：

- Service 实例指针；
- 路由快照；
- TCP Connection；
- NATS Connection 或 Subject；
- Future、RequestID、Timer；
- 每次调用的 Buffer。

Target 使用具体值类型，不使用接口、闭包或 `map[string]any`，构造和复制不得产生不必要
的堆分配。

### 6.2 M11 的目标解释

M11 尚未接入服务发现：

- `ToService("PlayerService")` 只在调用方所属 Node 中按实际 ServiceName 精确查找；
- `ToServiceOnNode(nodeID, "PlayerService")` 只在 `nodeID` 等于调用方所属 NodeID 时继续
  本地查找；
- 指定其他 NodeID 时返回 `CodeRPCNoRoute`；
- 找到 Service 但其契约不匹配时返回明确的 RPC 契约错误；
- Target 构造不执行查找、网络 I/O 或等待；
- 空名称、空 NodeID 和零值 Target 不 panic，在真正调用时返回
  `CodeInvalidArgument`。

M13/M14 接入远端 Transport 后，继续使用同一个 Target 和生成客户端，不改变业务调用
外观。后续服务发现和路由里程碑只扩展 `ToService` 的候选来源，不修改强类型方法。

### 6.3 为什么不使用两个构造函数

两个构造函数虽然实现直接，但会使每个 RPC 契约重复生成自动目标和精确目标入口。未来
增加路由策略时还可能继续扩张构造函数集合。

单构造函数加 Target：

- 只生成一组 API；
- 调用处仍然明确展示目标语义；
- 不依赖空 NodeID 或可变参数表达特殊含义；
- 后续扩展目标选择时不改变客户端类型；
- 不为当前热路径增加动态分派。

## 7. RPC 接口签名规则

### 7.1 输入

1. 第一个参数必须是 `context.Context`；
2. 只能出现一个 Context；
3. Context 不进入业务数据编码；
4. Context 后允许零个或多个业务参数；
5. 不设置人为参数数量上限；
6. 不支持 `...T`，使用 `[]T` 代替。

### 7.2 输出

1. 允许零个或多个业务输出；
2. `error` 可以省略；
3. 存在 `error` 时最多一个并且必须位于最后；
4. 生成客户端始终保留一个最终 `error`；
5. 服务端未声明 `error` 时，最终错误只能表达框架失败；
6. 完全没有返回值的方法只有通知调用语义；
7. 只返回 `error` 的方法仍具有请求—响应语义，但客户端也允许显式选择 Notify 并放弃
   远端错误。

### 7.3 生成分类

| 服务端签名 | M11 生成 |
|---|---|
| 一个或多个业务结果 | `AsyncXxx`、`AwaitXxx`、`NotifyXxx`，请求—响应外观追加最终 `error` |
| 业务结果加末尾 `error` | `AsyncXxx`、`AwaitXxx`、`NotifyXxx`，请求—响应外观复用最终 `error` |
| 只返回 `error` | `AsyncXxx`、`AwaitXxx`、`NotifyXxx` |
| 完全无返回值 | `NotifyXxx` |

后续为以上全部分类生成 `BroadcastXxx`，但 M11 不生成 Broadcast 方法，避免在没有服务
发现目标快照时建立不完整实现。

## 8. origingen 工具

### 8.1 目录与包

```text
cmd/origingen/       命令行入口，package main
internal/rpcgen/     扫描、校验、建模和生成，package rpcgen
rpc/                 公开稳定类型与生成代码调用边界，package rpc
```

业务项目只调用 `origingen`，不直接导入 `internal/rpcgen`。

### 8.2 命令

固定入口：

```text
origingen rpc ./...
```

也允许：

```go
//go:generate go run github.com/duanhf2012/origin/v3/cmd/origingen rpc ./...
```

一次模块级执行完成：

1. 按当前 Go Build Context 加载目标包；
2. 找出全部 `//origin:rpc` 接口；
3. 构建接口、方法、参数和返回值模型；
4. 找出同一 Go Module 内实现 `service.IService` 的具名 Service 类型；
5. 建立 Service 到 RPC 契约的实现关系；
6. 全局计算并检查 ContractID、MethodID；
7. 完成全部签名、类型、名称和碰撞校验；
8. 所有校验通过后才生成文件；
9. 使用临时文件和同目录原子替换提交结果。

任何包失败时，本次执行不允许先写一部分生成文件再失败。

### 8.3 生成文件

每个受影响包最多生成一个：

```text
origin_rpc.gen.go
```

文件必须包含标准生成头：

```go
// Code generated by origingen. DO NOT EDIT.
```

生成规则：

- 包、契约、Service、方法和辅助声明使用稳定排序；
- 相同输入重复生成得到逐字节相同结果；
- 生成后执行 Go 格式化；
- 生成代码不得依赖执行顺序或 Map 遍历顺序；
- 业务不能手工修改生成文件；
- CI 必须能够重新生成并检查工作树无差异。

契约与实现可以位于不同包，但首版必须位于同一 Go Module。跨 Module 的实现发现和绑定不
进入 M11。

## 9. ContractID、MethodID 与契约指纹

### 9.1 类型

```go
type ContractID uint64
type MethodID uint64
```

运行时使用生成常量，不在每次调用中计算哈希。

### 9.2 标识职责

- ContractID 标识完整 RPC 接口；
- MethodID 全局标识一个 RPC 方法；
- ContractID 不表示 NodeID、ServiceName 或请求序号；
- MethodID 不表示 RequestID；
- RequestID 到 M13 才引入。

目标 Service 已经由 Target 选定后，消息热路径只需使用全局唯一 MethodID 查找
Dispatcher；ContractID 主要用于注册、诊断、发现元数据和兼容性校验。

### 9.3 生成方案

已确认 ContractID 和 MethodID 使用带域隔离的 SHA-256 截断值。当前建议的规范名称为：

```text
Contract canonical name:
  <完整 Go import path>.<interface name>

Method canonical name:
  <contract canonical name>.<method name>
```

当前建议的概念计算为：

```text
ContractID = SHA-256("origin.rpc.contract.v1\0" + contractName) 前 8 字节
MethodID   = SHA-256("origin.rpc.method.v1\0" + methodName) 前 8 字节
```

前 8 字节建议按大端解释为 `uint64`。域前缀、分隔符、名字组成和大小端属于线协议稳定性
的一部分，必须在 M11 开工 Review 中完整确认；确认并发布后不得修改。

MethodID 不包含参数签名，使同一方法的兼容数据演进不改变方法身份。完整方法签名和数据
Schema 进入契约指纹；不兼容修改由指纹和兼容性检查拒绝。

### 9.4 碰撞处理

`origingen rpc ./...` 在写文件前建立模块级全局表：

- 两个不同契约得到同一 ContractID 时立即失败；
- 两个不同方法得到同一 MethodID 时立即失败；
- 错误同时列出两端完整规范名和冲突 ID；
- 任一碰撞都不写入任何新生成结果；
- 首版不提供手工 ID 覆盖逃避碰撞。

启动注册和后续 Node 握手继续执行二次冲突检查，但不能代替生成期检查。

### 9.5 完整契约指纹

每个契约额外生成完整 SHA-256 指纹，至少覆盖：

- Contract 规范名；
- 按稳定顺序排列的方法；
- 方法名和 MethodID；
- 输入和输出位置；
- 每个位置的数据类型描述；
- error 声明方式；
- M11 Protobuf 消息全名和基础类型布局；
- 以后由 M12 补充的普通结构体 Schema。

完整指纹不进入每次调用热路径。M13 Node 握手和后续发现元数据可以使用它快速拒绝契约
不一致的实例。

## 10. Service 自动识别与绑定

### 10.1 自动识别

业务不显式声明绑定对象。生成器使用 Go 类型系统检查：

1. 类型是同一 Module 内的具名具体类型；
2. 指针或值方法集满足 `service.IService`；
3. 同时完整实现某个 `//origin:rpc` 接口。

发现后生成编译期断言和 Dispatcher 适配代码。方法实现可以分散在同包多个文件中。

### 10.2 多实现和多契约

- 同一个 RPC 契约允许被多个 Service 类型实现；
- 普通 Service 和模板 Service 都按真实运行实例名注册；
- 一个 Service 类型实现两个或更多公开 RPC 契约时生成失败；
- Service 没有实现任何公开 RPC 契约时不生成 Dispatcher，仍可正常运行；
- RPC 接口嵌入导致的方法集合不用于规避一个 Service 一个契约的限制。

### 10.3 生成适配

生成器概念上为 Service 实现包生成：

```go
func (s *PlayerService) RPCDispatcher() rpc.Dispatcher
```

业务不实现该方法。Node 使用一个最小内部接口判断 Service 是否具有 Dispatcher：

```go
type dispatcherProvider interface {
    RPCDispatcher() rpc.Dispatcher
}
```

`RPCDispatcher` 方法名和返回类型已经确认。`dispatcherProvider` 由 Node 使用方定义为
未导出接口，避免额外公开一个只供框架装配使用的接口类型；跨包识别只要求生成方法本身
导出。不建立包级全局 `IRPCService` 注册表。

### 10.4 Node 注册

每个 Node 创建并独占一个 `*rpc.Runtime`。同一 Application 中的多个 Node 分别持有不同
Runtime，不能共享 Dispatcher 目录、路由状态或以后加入的 TCP/NATS 连接状态。

Node 在实例装配冷路径：

1. 创建当前 Node 的 `rpc.Runtime`；
2. 按已经确定的 Service 配置创建实例；
3. 检查实例是否实现 Dispatcher Provider；
4. 校验 Dispatcher 的 ContractID 和指纹；
5. 调用 Runtime 注册方法，把实际 ServiceName、Service 实例和 Dispatcher 保存到当前
   Node 私有目录；
6. 重复 ServiceName、同一 Service 重复注册或描述不一致时使 Node 启动失败；
7. Node 停止时关闭 Runtime，并随 Service 目录一起释放。

`rpc.Runtime` 是 Node 内部的 RPC 路由和执行基础设施，不是 RPC 业务目标。RPC 契约、
客户端和 Dispatcher 始终面向 Service；Node 只提供 Service 实例所在的运行和连接边界。

注册目录属于 `rpc.Runtime` 实例，不使用包级可变全局状态。同一进程中的多个 Application
和 Node 互不污染。

### 10.5 Service 到 RPC Runtime 的最小桥接

`service` 包不导入 `rpc` 或 `node`。M11 在 `service` 包提供只读框架辅助函数：

```go
func RuntimeOf(target IService) Runtime
```

该函数返回 Service 绑定时已经存在的 `service.Runtime`；nil、未绑定或有类型 nil 的
Service 返回 nil，不发生 panic。

`node.serviceRuntime` 在原有 `service.Runtime` 能力之外额外实现：

```go
func (runtime *serviceRuntime) RPCRuntime() *rpc.Runtime
```

`rpc` 包内部只在生成客户端构造冷路径使用以下未导出接口取得当前 Node Runtime：

```go
type runtimeProvider interface {
    RPCRuntime() *Runtime
}
```

调用链为：

```text
生成客户端
    -> rpc.NewGeneratedClient(owner, target, contractID)
    -> service.RuntimeOf(owner)
    -> runtimeProvider.RPCRuntime()
    -> 当前 Node 的 *rpc.Runtime
```

这样保持 `service -> rpc`、`rpc -> node` 的反向依赖都不存在，也不需要全局 Map 按
Service 指针查找 Runtime。生成客户端只在构造时完成一次接口断言；每次 RPC 调用不重复
取得或断言 Runtime。

## 11. Dispatcher

### 11.1 最小接口

当前 Dispatcher 草案为：

```go
type ContractFingerprint [32]byte

type Dispatcher interface {
    ContractID() ContractID
    Fingerprint() ContractFingerprint

    Dispatch(
        ctx context.Context,
        methodID MethodID,
        request []byte,
        responseDst []byte,
    ) ([]byte, error)
}
```

由于已确认“带返回值方法也生成 Notify”，Dispatcher 必须知道本次调用是否需要响应，否则
Notify 会无意义地编码随后被丢弃的业务结果。第 20 节需要在开工前确认是在 `Dispatch`
增加轻量 `CallKind`，还是使用同等清晰且无歧义的最小入口；确认前本节签名不视为最终 ABI。

语义固定为：

- `ContractID` 和 `Fingerprint` 只读取生成期常量，不在调用热路径计算；
- `request` 是只读的已编码业务载荷；
- `responseDst` 由 `rpc.Runtime` 提供，Dispatcher 把响应追加到该 Slice；
- 返回的 Slice 是本次完整响应，可以与 `responseDst` 共享底层数组；
- Dispatcher 返回后不能继续持有 `ctx`、`request`、`responseDst` 或返回 Slice；
- Runtime 管理请求和响应 Buffer 的最终释放，Dispatcher 不直接接触 BufferPool；
- Notify 仍使用同一静态方法分派；即使原方法带返回值，也不得编码或保留业务响应，目标
  业务错误和 panic 只进入目标侧诊断；
- 未知 MethodID、解码失败、业务错误、panic 和编码失败均返回统一错误。

### 11.2 静态分派

生成 Dispatcher 使用 MethodID Switch 或等价静态表：

```go
switch methodID {
case playerRPCGetPlayerMethodID:
    // 解码参数、调用 PlayerService.GetPlayer、编码返回。
case playerRPCSavePlayerMethodID:
    // 解码参数、调用 PlayerService.SavePlayer、编码返回。
default:
    return errs.ErrRPCMethodNotFound
}
```

热路径禁止：

- `reflect.Value.Call`；
- 按方法名字符串查找；
- 把参数装入 `[]any`；
- 每次构建方法 Map；
- 为每个参数做运行时类型判断。

### 11.3 Dispatcher 职责

生成 Dispatcher 只负责：

1. 校验 MethodID；
2. 调用生成解码器；
3. 以静态类型调用真实 Service 方法；
4. 捕获并转换业务方法边界 panic；
5. 编码业务结果和最终错误；
6. 不保留输入和输出 Slice，只释放 Dispatcher 自己明确创建且未转移所有权的临时对象。

目标查找、Service 状态、队列准入、Await 恢复和未来 Transport 不复制到每个
Dispatcher。

### 11.4 不建立公共 Codec 接口

M11 不定义 `rpc.Codec` 或通用 `Encode(any)`、`Decode(any)` 接口。`origingen` 为每个方法
直接生成静态函数，例如：

```go
func encodeGetPlayerRequest(
    dst []byte,
    playerID int64,
) ([]byte, error)

func decodeGetPlayerRequest(
    src []byte,
) (playerID int64, err error)

func encodeGetPlayerResponse(
    dst []byte,
    player *pb.Player,
    callErr error,
) ([]byte, error)
```

生成客户端和 Dispatcher 直接调用这些函数。这样不需要 `any`、运行时反射、Codec 查找
或接口动态分派；M12 扩展普通结构体时继续生成静态函数，不改变 Dispatcher 接口。

## 12. M11 数据类型

### 12.1 本里程碑支持

每个顶层输入和输出独立分类。M11 支持：

- `bool`；
- 有符号和无符号基础整数；
- `float32`、`float64`；
- `string`；
- `[]byte` 原始字节快速路径；
- 顶层 Protobuf 生成消息；
- 上述类型的具名别名是否直接支持，留在开工 Review 确认。

顶层 Protobuf 使用标准 Protobuf 编解码，保留 unknown fields、`optional`、`oneof` 和
Open、Hybrid、Opaque API 语义。

### 12.2 M12 范围的阶段性失败

以下合法目标能力在 M11 暂不生成：

- `*int` 等普通指针；
- 普通 Slice 和数组；
- Map；
- 普通 Go 结构体；
- 容器和结构体中嵌套的 Protobuf 类型；
- 自定义静态 Codec。

生成错误必须明确说明“该类型属于 M12，当前 M11 尚未实现”，并列出接口、方法、参数或
返回位置。不得误报为永久禁止，也不得使用反射或 JSON 临时回退。

### 12.3 空载荷

Protobuf 空消息、空字符串和空 `[]byte` 可以编码为零字节业务载荷。M11 内部调用描述仍
包含 MethodID、参数位置和调用分类，不能把零字节业务载荷解释为缺少 RPC 请求。

## 13. 同 Node RPC 执行流程

### 13.1 统一原则

同 Node RPC 不直接调用目标 Service 方法。无论将来使用 TCP、NATS 还是当前本地路径，
都必须验证相同的：

- 参数编码；
- 目标查找；
- Service 状态与队列准入；
- Dispatcher；
- 目标 Service 串行执行权；
- 返回值编码和解码；
- 最终错误语义。

这样可以避免开发时多 Node 同进程运行正常，而生产中一个 Node 一个进程后才暴露协议、
类型、队列或调度错误。

### 13.2 Context 边界

调用方的 Service Task Context 可以沿当前调用栈向下传递，也可以派生取消、Deadline 和
不可变元数据。它携带的 Service 执行权令牌只能由所属当前任务使用，不得交给其他
goroutine 调用 `Await` 等需要当前 Service 执行权的 API。

其他 goroutine 可以观察派生 Context 的取消状态和不可变元数据，但不能把该 Context
当作自身的 Service Task Context，也不能借此访问调用方 Service 状态。

本地 RPC 必须：

1. 从调用 Context 提取有效 Deadline、取消状态和后续协议需要的不可变元数据；
2. 为目标 Service 任务使用目标 Scheduler 提供的任务 Context；
3. 在目标任务中组合 RPC Deadline 语义；
4. Async 回调使用调用方 Scheduler 新建的回调任务 Context；
5. 不让目标任务持有调用方 Task Context 的私有执行权令牌。

有效超时只计算一次。生成调用复用 Service Await 已确定的默认链：

```text
调用方显式 Deadline
    > Service.SetDefaultAwaitTimeout
    > Node scheduler.default_await_timeout
    > Origin 内置 15s
```

调用方 Context 已有显式 Deadline 时沿用其 Go Runtime Timer，不再登记 M8；没有
Deadline 的合法 Context 使用统一默认值和一条 M8 Deadline。M8 派生 Context 必须公开
计算后的 `Deadline()`，同一次调用不能同时登记 Go Timer 和 M8 Deadline。

### 13.3 Await

`AwaitXxx`：

1. 完成不需要等待的静态参数、owner 和 Task Context 校验；
2. 进入调用方 Service 已有 `Await` 原语，计算唯一有效 Deadline 并释放执行槽；
3. 原任务 goroutine 在 Await 等待函数内编码请求、读取路由并提交目标 Service Ready FIFO；
4. 提交失败时保存错误并进入正常 FIFO 恢复，不在未持有执行权时直接返回业务代码；
5. 提交成功后，原任务 goroutine 等待本地调用完成；
6. 目标 Service 取得执行槽后解码并调用业务方法；
7. 目标按调用模式编码结果或记录通知错误，并完成本地调用；
8. 原任务进入调用方 Ready FIFO；
9. 原任务重新取得执行权后解码结果并返回。

不为每次 Await 创建辅助 goroutine。同一个 Service Await 调用自身 RPC 时也按上述流程
释放执行槽，因此目标任务可以正常执行，不形成直接递归或执行权死锁。

`AwaitXxx` 只接收并传递一个有效 Context，内部直接复用 Service Await 原语，不再次套用
RPC 默认超时，也不公开 `AwaitManaged` 等重复入口。Deadline 从进入 Await 原语开始覆盖
编码、路由、目标队列排队、业务执行、响应和恢复排队全过程。

### 13.4 Async

`AsyncXxx`：

1. 校验参数、编码并尝试提交目标任务；
2. 不释放调用方当前执行槽；
3. 编码、目标、路由或队列准入立即失败时直接返回 `error`，不创建完成状态且不投递回调；
4. 提交成功后生成方法立即返回 nil；
5. 目标完成后把强类型回调投递到调用方 Service Ready FIFO；
6. 即使目标立即完成，回调也不能在当前调用栈内执行；
7. 回调取得调用方 Service 执行权后才能访问 Service 状态；
8. 返回 nil 的调用必须且只能发布一次回调。

Async pending 使用与 Await 相同的有效 Deadline。调用方需要主动取消时使用
`context.WithCancel` 派生 Context 并调用 `cancel()`；生成方法不再返回另一种取消句柄。

M11 只处理 Running 期间的基本回调。Draining、停止期间的新调用和回调排空由 M15 完整
实现。

### 13.5 Notify

`NotifyXxx`：

1. 编码请求；原始方法带返回值时调用方显式放弃全部业务结果；
2. 提交目标 Service Ready FIFO；
3. 队列接受后立即返回 `nil`；
4. 不等待目标业务开始或结束；
5. 不创建响应、Future、RequestID 或 pendingCall；
6. 目标业务返回值不编码，业务错误或 panic 只在目标侧记录；
7. 编码失败、无目标、契约不匹配、Service 非 Running 或队列满直接返回相应错误。

M11 Notify 的“发送成功”表示目标本地队列已经接受，不表示业务成功。Context 只约束
接受前的编码、路由和投递过程；目标队列接受后不能撤回，不创建 pending 或超时项，目标
业务执行也不再受调用方 Deadline 约束。

### 13.6 Buffer 所有权

- 编码优先向 Application 已有 BufferPool 取得的 Buffer 追加；
- 提交成功后，请求 Buffer 所有权转移给目标任务；
- 提交失败时，调用方立即释放；
- Dispatcher 返回后不得继续引用输入 Buffer；
- Await/Async 响应由完成状态持有，消费者复制出结果后释放；
- Notify 目标任务处理完成后释放；
- Protobuf 解码结果不能引用即将释放的输入 Buffer；
- 不因为本地调用而直接共享可变业务对象指针。

## 14. M11 本地调用状态

M11 不引入 M13 的 RequestID、pending 表、连接标识和断线完成。

Await 和 Async 仍需要一个最小本地完成状态，用于：

- 一次性完成；
- 保存编码结果或错误；
- 唤醒 Await；
- 投递 Async 回调；
- 管理请求和响应 Buffer 所有权。

该对象只在一次本地调用的直接参与方之间传递，不进入全局 Map。M11 暂称 `localCall`，
只负责同 Node 最小闭环；M13 引入 RequestID 和远程请求—响应状态时，以最终池化
`pendingCall` 取代它，不长期并存两套重复状态机。

是否池化 `localCall` 必须由 Benchmark、逃逸分析和失败路径复杂度决定。未证明稳定收益
前不为了理论零分配引入难以验证的 ABA 防护。

## 15. 错误语义

M11 复用现有通用和 Service 错误：

- `CodeInvalidArgument`；
- `CodeCanceled`；
- `CodeDeadlineExceeded`；
- `CodeServiceNotReady`；
- `CodeServiceStopping`；
- `CodeServiceStopped`；
- `CodeServiceQueueFull`；
- `CodeRPCNoRoute`；
- `CodeInternal`。

M11 还需要在 RPC 与编解码编号区间补充最小错误：

- 契约不匹配；
- MethodID 不存在；
- 请求解码失败；
- 响应解码失败；
- 远端或目标业务方法 panic。

具体错误码名称和编号必须在后续 M11 Review 中与
[统一错误码设计](../details/2026-07-24-统一错误码设计.md)一起确认，不在实现中临时添加。

固定错误优先复用只读哨兵。具体 NodeID、ServiceName、契约名、方法名和生成字段路径只
进入结构化日志或生成期诊断，不为高频失败构造通用 Details Map。

所有生成调用都必须保留最终 `error`：

- `AwaitXxx` 的最后一个返回值是业务或框架 `error`；
- `AsyncXxx` 自身返回立即提交 `error`，强类型回调的最后一个参数返回提交成功后的最终
  业务或框架 `error`；
- `NotifyXxx` 和后续 `BroadcastXxx` 直接返回本地接受阶段的框架 `error`，不返回远端
  业务错误。

nil Context、Async 的 nil callback、零值客户端、无效 owner 和无效 Target 都必须返回
稳定参数或未就绪错误，不得 panic。Async 在这些校验失败时不创建调用状态且不投递回调。

## 16. 包依赖与可见性

预期依赖方向：

```text
cmd/origingen
    -> internal/rpcgen
        -> Go 包加载与类型信息

业务契约生成代码
    -> rpc
    -> service
    -> protobuf（仅契约使用时）

node
    -> rpc
    -> service

rpc
    -> service
    -> internal/bufferpool（通过框架内部所有权边界）
```

约束：

1. `service` 不反向导入 `node`；
2. `service` 不导入 `rpc`；
3. `rpc` 不导入 `node`；
4. 每个 Node 创建一个 `rpc.Runtime`，通过 `node.serviceRuntime` 的窄桥接供生成客户端取得；
5. 生成客户端构造时只进行一次 Runtime 接口断言，RPC 热路径直接使用已保存指针；
6. 生成代码不能导入 Origin 的 `internal` 包；
7. Node 只通过最小 Dispatcher Provider 接口识别 RPC Service；
8. 不建立公共 Codec 接口，编解码使用生成的静态函数；
9. 不为了隐藏所有生成代码入口建立大型抽象层；
10. 只有业务或生成代码确实需要的类型才公开；
11. 是否建立独立 `internal/rpcruntime`，只有在实现证明能够降低职责耦合且不造成生成代码
    越过 Go `internal` 边界时再决定，M11 默认不预建。

## 17. 冷路径与热路径

### 17.1 冷路径

以下工作只允许发生在生成或 Node 启动阶段：

- Go 包加载和类型检查；
- SHA-256 标识计算；
- 契约指纹计算；
- Service 实现关系识别；
- Dispatcher 描述校验；
- ServiceName 到 Dispatcher 的注册；
- 方法表构建；
- 从 owner 取得并保存当前 Node 的 `*rpc.Runtime`。

冷路径优先保证诊断清晰和结果确定，不为了微小启动速度牺牲可维护性。

### 17.2 热路径

每次 RPC 调用必须避免：

- 运行时反射；
- 方法字符串拼接和查找；
- SHA-256 计算；
- `[]any` 参数组装；
- Target 接口装箱；
- 每调用辅助 goroutine；
- 每调用标准库独立 Timer；
- 重复序列化或完整 payload 复制；
- 不必要的锁、Channel 跳转和闭包逃逸；
- 为固定框架错误动态格式化字符串。

允许的必要调度边界：

- 调用方 Service 到目标 Service Ready FIFO；
- Await 完成后恢复原调用方任务；
- Async 完成后把回调作为调用方新任务投递。

不为了减少一次必要队列跳转而直接调用目标 Service 方法。

## 18. 测试与验收

### 18.1 生成器

至少覆盖：

1. 标记接口发现；
2. 未标记接口忽略；
3. 接口嵌入失败；
4. Context 缺失、位置错误或重复失败；
5. 可变参数失败；
6. error 位于中间或重复失败；
7. 一个 Service 多契约失败；
8. 同一契约多 Service 实现成功；
9. 契约和实现跨包、同 Module 成功；
10. ContractID、MethodID 稳定；
11. 人工构造 ID 碰撞时在写文件前失败；
12. 相同输入重复生成逐字节一致；
13. 任一包校验失败时没有部分写入；
14. 生成文件格式和标准头；
15. M12 类型得到清晰的阶段性错误；
16. 四类返回签名严格生成第 7.3 节规定的方法集合；
17. M11 的 Async、Await 和 Notify 最终 `error` 位置不会遗漏。

### 18.2 本地 RPC

至少覆盖：

1. ToService 调用同 Node Service；
2. ToServiceOnNode 指定当前 Node 中的 Service；
3. ToServiceOnNode 指定其他 Node 时返回无路由；
4. 空 Target 和非法名称；
5. 目标不存在；
6. 目标契约不匹配；
7. 目标 Service 未 Ready、Stopping、Stopped；
8. 目标队列满；
9. Await 正常结果、业务错误、框架错误、超时和取消；
10. Await 释放和恢复调用方执行槽；
11. Service Await 调用自身 RPC 不死锁；
12. Async 回调不抢占当前任务且最多一次；
13. Async 立即失败直接返回错误且不投递回调，返回 nil 后回调严格一次；
14. Notify 覆盖完全无返回值、只返回 error 和带业务返回值的方法，并且只等待队列接受；
15. 请求—响应业务方法 panic 返回统一错误；
16. Notify panic 只产生目标侧诊断；
17. 输入、响应和失败路径 Buffer 全部释放；
18. 停止后没有 goroutine、Timer 或 Buffer 泄漏；
19. 同一进程中的多个 Node 使用完全隔离的 RPC Runtime 注册目录；
20. nil、未绑定或缺少 RPC Runtime 的 owner 不 panic，并返回统一错误；
21. nil Context、Async nil callback 和零值客户端直接返回错误且不产生调用或回调。

### 18.3 数据类型

至少覆盖：

- 每种基础标量；
- 空和非空 string；
- nil、空和非空 `[]byte` 的既定边界；
- 顶层 Protobuf 空消息；
- 顶层 Protobuf unknown fields、optional、oneof 和 Opaque API；
- 多输入、多输出和混合基础类型/Protobuf；
- M12 普通指针、容器、结构体和嵌套 Protobuf 的阶段性失败。

### 18.4 性能

至少保存：

- Target 构造的 `ns/op`、`allocs/op` 和 `B/op`；
- 基础类型编码和解码；
- 顶层 Protobuf 编码和解码；
- Dispatcher 命中；
- 生成客户端构造时取得 Runtime 的成本和分配；
- 同 Node Await、Async、Notify；
- 自调用和跨 Service 调用；
- 目标不存在和队列满失败路径；
- 不池化与池化 `localCall` 的对照；
- P50、P95、P99 延迟；
- Windows 与 Linux 的可复现环境和结果。

性能验收重点不是追求脱离业务的绝对 QPS，而是确认热路径没有运行时反射、每调用辅助
goroutine、隐藏 Timer 和可避免的多次 payload 复制。

## 19. 生成代码示意

以下只说明职责，不提前锁定最终内部 API：

```go
const (
    playerRPCContractID rpc.ContractID = 0x...
    getPlayerMethodID   rpc.MethodID   = 0x...
)

type PlayerRPCClient struct {
    client rpc.Client
}

func NewPlayerRPCClient(
    owner service.IService,
    target rpc.Target,
) PlayerRPCClient {
    return PlayerRPCClient{
        client: rpc.NewGeneratedClient(
            owner,
            target,
            playerRPCContractID,
        ),
    }
}

func (c PlayerRPCClient) AwaitGetPlayer(
    ctx context.Context,
    playerID int64,
) (*pb.Player, error) {
    // 生成编码和解码函数直接进入统一 RPC 调用核心。
}
```

业务不直接依赖 `rpc.Client` 或 Dispatcher 的低层调用方法。公开这些类型只为生成代码
和 Node 装配，不能把它们扩张成另一套手写 RPC API。是否额外公开只读 Descriptor 仍由
后续 Review 决定。

`rpc.Client` 是值语义的生成代码底座，概念字段为：

```go
type Client struct {
    owner      service.IService
    runtime    *Runtime
    target     Target
    contractID ContractID
}
```

字段保持未导出。`NewGeneratedClient` 不执行路由、网络 I/O 或等待；owner 无效或尚未绑定
RPC Runtime 时构造出不可调用的安全 Client，真正调用返回统一参数或未就绪错误，不发生
panic。客户端不持有独立 TCP/NATS 连接。

## 20. 后续 M11 Review 项

2026-07-28 按开发指导原则完成一次逐节 Review。已确认的调用生成和广播规则已经回写；
以下问题会直接影响生成 ABI、线格式、所有权或低延迟实现，必须在开工前逐项确认：

| 顺序 | 待确认问题 | 当前建议 |
|---|---|---|
| 1 | 基础类型线布局、位置字段、`int`/`uint`、具名类型、nil/空值和顶层 Protobuf presence | 建立一套版本化静态线格式；`int`/`uint` 按 64 位线值编码并在目标架构做范围校验；具名基础类型按底层类型处理；nil 与空值是否保持区分必须在本项明确 |
| 2 | `[]byte` 输入解码后是借用请求 Buffer 还是复制为业务独立内存 | 按规则由开发者确认：借用延迟和分配更低但业务不能长期持有，复制使用自由且更安全但增加大消息成本 |
| 3 | RPC 契约可见性、泛型和跨包实现桥接 | RPC 接口和方法要求导出、首版禁止泛型契约；契约包生成 `New<Contract>Dispatcher(impl Contract)`，Service 包只生成 `RPCDispatcher()` 薄适配，避免重复 Codec |
| 4 | Dispatcher 如何区分请求—响应与 Notify | 增加两个值的轻量 `rpc.CallKind`；Notify 调用真实方法但跳过响应编码，避免用 nil Slice 暗示模式 |
| 5 | Await/Async Deadline 与 M8 接入 | Await 复用 Service M8 Deadline；Async 使用每 Node RPC Runtime 的一条共享 `DeadlineQueue`，不为每次调用建立 Go Timer |
| 6 | `localCall` 字段、完成同步和池化 | M11 只保留一个私有一次性完成状态；先实现清晰基线并 Benchmark，M13 用最终池化 `pendingCall` 替换，避免为短期对象维护复杂 ABA 防护 |
| 7 | RPC 错误码、业务错误编码、panic 和本地/远端一致性 | 在 2000 区间补充契约不匹配、方法不存在、请求解码、响应解码和执行 panic；同 Node 也经过相同错误编码/解码，不直接传 Go error 指针 |
| 8 | ContractID、MethodID 和完整指纹的规范化字节 | 一次性固定域前缀、UTF-8 名称、分隔符、大端 ID、方法排序、类型描述和生成格式版本，并以 golden test 锁定 |
| 9 | Go 包加载和 Protobuf 依赖 | 使用固定版本 `golang.org/x/tools/go/packages` 与 `google.golang.org/protobuf`；Protobuf 优先使用官方 Append/Options API，具体版本在实施计划前查验最新固定版 |
| 10 | 旧生成文件清理和生成 ABI 版本 | 只删除包含完整 origingen 标记且本轮确认不再需要的文件；生成代码加入编译期 ABI 版本校验，手写或标记异常文件绝不删除 |
| 11 | 是否公开只读 Descriptor | M11 不公开；生成代码和 Runtime 内部保留最小描述，等监控、调试或插件出现真实消费者后再公开 |

此外，当前 M9 实现与已确认的唯一计时器设计仍有差异，必须作为 M11 实施前置修正，不属于
可以跳过的后续优化。

### 20.1 Context 定时器调研与已确认结论

截至 2026-07-28 的官方源码和项目实践：

1. Go `context.WithDeadline` 内部创建 `timerCtx` 并调用 `time.AfterFunc`；
   `WithTimeout` 直接委托给 `WithDeadline`。Go Runtime 把 Timer 保存在每个 P 的最小堆中，
   近年持续优化锁竞争、回收和 Stop/Reset 语义，但每个新 Deadline Context 仍有对象分配
   和 Runtime Timer 维护成本：
   [Go Context 源码](https://go.dev/src/context/context.go#L625)、
   [Go Runtime Timer 源码](https://go.dev/src/runtime/time.go#L130)、
   [Go 1.14 Timer 优化](https://go.dev/doc/go1.14#runtime)；
2. gRPC 明确让调用方通过 Context 设置 Deadline，默认不替调用创建 Deadline；Go 的
   Deadline 还会沿后续调用传播：
   [gRPC Deadline 指南](https://grpc.io/docs/guides/deadlines/)；
3. NATS Go 的 `RequestWithContext` 直接等待响应或 `ctx.Done()`，不会在已有 Context
   之外再创建第二个 Timer：
   [NATS Context 请求源码](https://github.com/nats-io/nats.go/blob/main/context.go)；
4. 高性能 RPC 框架 Kitex 在客户端没有直接叠加 `context.WithTimeout`，而是建立自定义
   timeout Context，并使用 `sync.Pool` 复用 `time.Timer`；服务端普通路径仍使用
   `context.WithTimeout`。这说明极端热路径确实会优化 Timer 分配，但代价是一套明显更复杂
   的超时任务、Worker 和回收状态机：
   [Kitex 客户端超时池](https://github.com/cloudwego/kitex/blob/develop/client/rpctimeout_pool.go)、
   [Kitex 服务端超时中间件](https://github.com/cloudwego/kitex/blob/develop/server/middlewares.go)；
5. fasthttp 也公开 `AcquireTimer`/`ReleaseTimer`，通过 `sync.Pool` 复用 Go Timer，
   目标明确是降低 GC 压力，而不是声称 Go Timer 不能使用：
   [fasthttp Timer 池](https://github.com/valyala/fasthttp/blob/master/timer.go)。

在当前 Windows 开发机、Go 1.26.5 上运行 Go 官方 `BenchmarkWithTimeout`，每个 Benchmark
操作创建并取消 10 个一小时 Context。40～4000 个预存活 Context 场景的代表结果约为
`1.65～2.69us/op`、`2720 B/op`、`40 allocs/op`，折算每个 Context 约
`0.17～0.27us`、`272 B`、`4 allocs`。这不是线上 RPC Benchmark，但能够说明 Go Timer
单次 CPU 成本不高，大量活跃或高频创建时主要风险是堆对象和 GC。

M8 已有对照基线：Windows 登记长 Deadline 约 `320.4ns/op`、首次 `1 alloc/op`，稳定
登记后取消约 `264.5ns/op`、`0 allocs/op`；Linux 一百万活跃 Deadline 约
`101.7 B/条`。两组 Benchmark 的语义不同，不能只按 `ns/op` 直接判输赢；M8 的主要收益是
统一管理海量框架默认 Deadline、稳定复用和降低 GC，而 Go Timer 的优势是标准 Context
兼容和高于 M8 `10ms` Tick 的显式短 Deadline 精度。

开发者于 2026-07-28 确认采用以下方案：

1. 调用方传入已经带 Deadline 的 Context 时，Origin 原样使用其 Go Timer，不再登记 M8；
2. 调用方 Context 没有 Deadline 时，Origin 按统一默认链计算 Deadline，使用无 Go Timer
   的内部可取消 Context 加一条 M8 Deadline；`context.Background()` 和
   `context.TODO()` 在超时选择层都属于这一类；
3. Await 与 RPC 只消费同一个有效 Context；Async 对显式 Context 的取消监听可使用
   `context.AfterFunc` 或等价一次性挂接，不再创建第二个 Timer，具体热路径必须 Benchmark；
4. 不在首版增加 `service.WithTimeout`、`rpc.WithTimeout` 或 CallOptions 等 Origin 专用
   超时外观。若真实项目证明显式短超时是高频 GC 热点，再以相同负载对比专用 M8 Context；
5. 任一路径都必须在完成、取消和停止时解除监听或取消 M8 Deadline，并保持一次性终态。

“无 Deadline”只决定计时方式，不绕过 API 自身的 Context 合法性校验。`Service.Await` 和
生成的 `AwaitXxx` 仍要求外层 Context 携带当前 Origin Task 执行权令牌；业务应传入事件、
RPC 或 Timer 回调收到的 Task Context，不能用裸 `context.Background()` 伪造执行权。
合法 Task Context 没有显式 Deadline 时同样使用 M8 默认超时。允许普通 Context 的框架
控制入口或后续独立客户端若收到 `context.Background()`/`context.TODO()`，也按相同默认链
使用 M8。

M8 管理的派生 Context 必须向下游正确暴露计算出的 `Deadline()`，到期后
`context.Cause` 和 Origin 最终错误均为 `context.DeadlineExceeded`；不能只关闭一个没有
Deadline 语义的普通取消 Context。该对象为框架私有的轻量包装，不创建 Go Runtime Timer。

该方案保留标准 Go 使用习惯，同时把数量最大、配置统一的默认超时交给 M8。其实现复杂度
明显低于全面接管用户 Context，也不会出现同一次调用同时拥有 Go Timer 和 M8 Deadline。

当前 M9 实现仍会把显式 Deadline 同时登记到 M8，且默认超时派生 Context 尚未公开
`Deadline()`；这是已识别的实现差异。进入 M11 编码前必须修正 M9 并补充回归与 Benchmark，
使两条路径都只保留一个物理计时器。其余第 20 节 Review 项确认、本文状态改为“已确认”并
在复核清单记录“允许实施”之前，不创建 M11 实施计划，不编写 M11 代码。

## 21. 当前结论

M11 将交付一个范围受控但真实可运行的 RPC 最小闭环：

```text
Go RPC 接口
    -> origingen
    -> 强类型客户端与静态 Dispatcher
    -> rpc.Target
    -> 同 Node Service Ready FIFO
    -> 静态解码和业务方法
    -> 静态编码
    -> Await 恢复 / Async 回调 / Notify 完成
```

这一闭环优先验证业务接口、生成结果、数据边界和 Service 单执行权，不提前加入网络、发现
和复杂路由。M12～M14 只在该稳定基础上依次补齐数据类型、TCP 和 NATS，不能重新发明
客户端外观或 Dispatcher 语义。
