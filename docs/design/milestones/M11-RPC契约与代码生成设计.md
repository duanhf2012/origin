# M11 RPC 契约与代码生成设计

> 文档状态：已实现并验收
> 创建日期：2026-07-27  
> 最后更新：2026-07-28
> 当前结论：M11 已完成实现、测试、性能基线和 Windows/Linux 验收

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

M11 不是完整网络 RPC 里程碑。M11 一并完成普通 Go 类型、结构体、容器和嵌套
Protobuf 的静态编解码；自定义静态 Codec、TCP、NATS、服务发现和完整停止分别由
M12～M15 及后续独立里程碑实现。

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
和最新路线图规定的当前里程碑边界为准。

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
10. 完全无返回值的方法在 M11 生成 `NotifyXxx` 和 `BroadcastXxx`；带任意返回值的方法
    生成 `AsyncXxx`、`AwaitXxx`、`NotifyXxx` 和 `BroadcastXxx`；
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
26. 首版不公开 `AwaitManaged` 或第二套 Await API；
27. M11 一并支持普通 Go 结构体、指针、数组、Slice、Map 和其中嵌套的 Protobuf
    生成结构体，不使用反射或 JSON 回退；
28. Map Key 只使用 M11 已支持的基础类型及其具名类型，不为 Map Key 单独扩展
    `uintptr`、复数、指针、接口或其他未支持类型；
29. `origingen` 必须在生成阶段拒绝所有未支持类型，并报告 RPC 契约、方法、参数、容器和
    字段的完整路径，不能延迟到运行时失败；
30. M11 不开放运行时自定义 Codec 注册表，但生成器内部必须保留可替换的静态编解码计划；
    M12 自定义 Codec 由生成器静态选择并生成直接函数调用，不在 RPC 热路径查表；
31. 普通 Go 结构体按导出字段声明顺序编码，不写字段 Tag 或稳定字段 ID；Map 不排序；
    完整契约指纹负责在解码前拒绝不一致 Schema。

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
- 同 Node Async、Await、Notify 和当前 Runtime 已知目标范围内的 Broadcast；
- 顶层 Protobuf、基础标量、字符串、`[]byte`、指针、数组、Slice、Map、普通结构体和
  嵌套 Protobuf 的静态编解码；
- 生成确定性、碰撞、非法声明、调度、错误和性能测试。

### 4.2 明确延后

| 能力 | 归属 |
|---|---|
| 自定义静态 Codec 和特殊类型扩展 | M12 |
| RPC 线协议、RequestID、pendingCall、连接管理和 TCP | M13 |
| NATS RPC Transport | M14 |
| 生命周期 Await 基础、完整 Stop 排空、OnStop Await RPC 和异常进程收尾 | M15 |
| Origin/etcd 服务发现、关注筛选和退休状态 | M15 后独立里程碑 |
| RoundRobin、Rand、ModKey、自定义路由和多 Node 发现范围 Broadcast | 服务发现之后独立里程碑 |
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
- `NotifyNodePlayerOnline`。

目标由 `rpc.Target` 决定，不能把 NodeID、ServiceName 重复加入每个 RPC 方法。

M11 为所有 RPC 方法生成 `BroadcastXxx`。它和 Notify 一样主动放弃业务结果，只返回本地
目标计算、编码和投递阶段的 `error`。例如：

```go
err := client.BroadcastPlayerOnline(ctx, playerID)
```

M11 尚未接入服务发现，因此标准广播只使用当前 Node `rpc.Runtime` 已注册的本地目标：

- `ToService("AService")` 广播给当前 Runtime 中全部同名、契约匹配且处于 Running 的本地
  `AService`；由于同一 Node 内 ServiceName 唯一，M11 中最多一个；
- `ToServiceOnNode(currentNodeID, "AService")` 使用相同本地规则；
- 指定其他 NodeID 或没有本地目标时返回 `CodeRPCNoRoute`；
- 编码或目标队列准入失败时返回对应错误；投递成功后不等待业务执行结果；
- 参数只编码一次，本地目标任务取得请求 Buffer 的唯一所有权。

服务发现和多 Node 路由接入后，生成方法及签名保持不变，标准广播范围扩展为：

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
| 一个或多个业务结果 | `AsyncXxx`、`AwaitXxx`、`NotifyXxx`、`BroadcastXxx`，请求—响应外观追加最终 `error` |
| 业务结果加末尾 `error` | `AsyncXxx`、`AwaitXxx`、`NotifyXxx`、`BroadcastXxx`，请求—响应外观复用最终 `error` |
| 只返回 `error` | `AsyncXxx`、`AwaitXxx`、`NotifyXxx`、`BroadcastXxx` |
| 完全无返回值 | `NotifyXxx`、`BroadcastXxx` |

`BroadcastXxx` 只复用原方法输入并返回一个本地接受阶段 `error`，不生成业务结果集合。
M11 先对当前 Runtime 已知的本地目标实现真实行为，后续只扩展候选快照来源。

### 7.4 输入与输出位置规则

M11 沿用已确认的多输入、多输出顺序规则，并把它作为固定顺序线格式的一部分：

1. `context.Context` 必须位于第一个参数，但不编码，也不占业务输入位置；
2. Context 后的业务输入按 Go 声明顺序依次成为输入位置 `1`～`N`；
3. 业务输出按 Go 声明顺序依次成为输出位置 `1`～`N`；
4. 最后的 `error` 不属于业务输出位置，由 RPC 错误部分单独处理；
5. 参数名和返回值名称不进入线格式，也不影响 ID 或指纹；
6. 调换顺序、在任意位置插入、末尾追加、删除或修改声明类型均属于不兼容修改；
7. 增加或删除服务端声明的最终 `error` 也属于契约变化；
8. 可变参数继续禁止，使用明确的 Slice 类型代替；
9. 每个输入和输出位置独立选择基础类型、`[]byte` 或顶层 Protobuf 编解码路径；
10. 这里的“位置”是生成期逻辑编号，不是每条载荷携带的字段 Tag。

固定顺序编码不能跳过未知输入或输出位置，因此即使只在末尾追加一个业务参数，也不能让
新旧签名直接混用。经常演进的 RPC 应优先使用一个顶层 Protobuf Request/Response，把
字段演进留在 Protobuf 内部。

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

只检查、不修改文件的 CI 入口：

```text
origingen rpc --check ./...
```

也允许：

```go
//go:generate go run github.com/duanhf2012/origin/v3/cmd/origingen rpc ./...
```

一次模块级执行完成：

1. 按当前 Go Build Context 加载目标包；
2. 找出全部 `//origin:rpc` 接口；
3. 构建接口、方法、参数、返回值及其全部可达字段和容器元素的完整类型图；
4. 找出同一 Go Module 内实现 `service.IService` 的具名 Service 类型；
5. 建立 Service 到 RPC 契约的实现关系；
6. 全局计算并检查 ContractID、MethodID；
7. 在渲染任何文件前完成全部签名、类型图、名称、Schema、Codec 计划和碰撞校验；
8. 所有校验通过后才在内存中生成完整文件；
9. 普通模式使用临时文件和同目录原子替换提交结果；
10. `--check` 模式逐字节比较应生成内容与工作树，发现缺失、过期或多余生成文件时返回
    非零状态，但不得修改文件。

任何包失败时，本次执行不允许先写一部分生成文件再失败。

### 8.3 生成前完整类型验证

`origingen` 必须递归验证每个 RPC 输入、输出及其全部可达类型。验证和生成是严格分离的
两个阶段；只有整个扫描范围没有错误时才允许渲染和写入。

至少检查：

- 顶层基础类型、Protobuf 和普通 Go 类型是否属于 M11 支持集合；
- 指针、数组、Slice、Map 的元素、Key 和 Value；
- 普通结构体的全部导出字段和嵌套路径；
- Map Key 是否与 M11 基础类型集合对齐；
- 循环类型、接口、函数、Channel、`uintptr`、复数和 `unsafe.Pointer`；
- 嵌套 Protobuf 是否使用不支持的 `oneof` 或 Opaque API；
- 具名结构体是否至少具有一个可序列化导出字段；显式 `struct{}` 除外；
- 静态 Schema、Codec 标识及版本是否能稳定进入完整契约指纹。

发现任一错误时，生成器立即使整次执行失败，不得生成“不完整但可编译”的 Codec。诊断必须
包含契约、方法、输入或输出位置、容器和字段组成的完整路径。例如：

```text
cannot generate Origin RPC codec:
  game.PlayerRPC.SavePlayer
  -> input 1
  -> Players
  -> map value
  -> Profile
  -> Data

unsupported type: interface{}
```

禁止把无法编码的字段静默忽略，禁止编码为空结构体，禁止切换到反射或 JSON，禁止延迟到
业务运行时才报错。旧生成文件无法单靠 `go build` 感知新增嵌套字段，因此项目构建和 CI
必须执行 `origingen rpc --check ./...`。

### 8.4 生成文件

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
- M11 同时生成的普通结构体 Schema。

完整指纹不进入每次调用热路径。M13 Node 握手和后续发现元数据可以使用它快速拒绝契约
不一致的实例。

### 9.6 兼容演进与发布规则

开发者确认继续采用原有的简单方案，不让 MethodID 包含参数或返回签名，也不为每次调用
增加独立方法签名字段：

- ContractID 只标识 RPC 接口；
- MethodID 只由契约规范名和方法名生成；
- 完整 ContractFingerprint 负责记录整个接口的精确签名和线格式；
- 指纹不匹配必须在业务载荷解码前返回契约不匹配，不能尝试猜测或兼容解码；
- M13/M14 必须通过握手、发现会话或等价的传输边界保证上述检查，但具体承载方式留在各自
  Transport 设计中确认，M11 不提前增加网络帧字段。

原 RPC 可以直接进行的兼容修改仅包括：

- 修改业务实现而不修改 Go RPC 签名；
- 修改参数或返回值名称；
- 顶层 Protobuf 消息保持同一消息全名，并遵守 Protobuf 的二进制兼容演进规则，例如新增
  可选字段且不复用已有字段编号。

输入或输出数量、顺序、声明类型、顶层消息全名、最终 `error` 声明或普通结构体不兼容
Schema 发生变化时，不允许在新旧版本同时运行期间直接修改原方法。优先采用以下流程：

1. 建立独立的版本化 RPC 契约和 Service，例如 `PlayerRPCV2` 与 `PlayerServiceV2`，旧
   契约和 Service 暂时保留；
2. 先部署新的接收端，使新旧契约同时可发现；
3. 再迁移调用方使用新契约；
4. 确认旧调用、排队消息和旧进程全部消失后，再在后续版本删除旧契约。

在同一个 RPC 接口中新增 `UpdatePlayerV2` 也会改变整个 ContractFingerprint，因此不能在
“完整指纹必须相等”的简单规则下实现无缝新旧混用；若选择这种方式，就要按整个契约执行
蓝绿或协调切换。若项目直接替换同名方法的不兼容签名，则必须采用维护窗口：停止旧调用
并排空旧消息，完成全部接收端更新后再启动新调用。

“先更新服务器”只有在修改本身兼容，或者新的版本化接收端与旧接收端同时保留时才安全。
仅把服务器同名方法直接改成不兼容的新签名，而旧调用方仍在发送请求，不能作为允许的
滚动升级方案。

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

Dispatcher 最终接口为：

```go
type ContractFingerprint [32]byte

type CallKind uint8

const (
    CallRequest CallKind = iota + 1
    CallNotify
)

type Dispatcher interface {
    ContractID() ContractID
    Fingerprint() ContractFingerprint

    Dispatch(
        ctx context.Context,
        methodID MethodID,
        kind CallKind,
        request []byte,
        response ResponseWriter,
    ) (ResponseWriter, error)
}
```

`CallRequest` 表示需要编码响应的 Await/Async；`CallNotify` 表示 Notify 或 Broadcast，
Dispatcher 仍调用真实业务方法，但跳过业务结果和业务 `error` 的响应编码。零值和未知
`CallKind` 返回 `CodeInvalidArgument`。

语义固定为：

- `ContractID` 和 `Fingerprint` 只读取生成期常量，不在调用热路径计算；
- `request` 是只读的已编码业务载荷；
- `ResponseWriter` 是 `rpc.Runtime` 在目标任务栈上创建的单次响应写入器，使用值传入和值
  返回，避免把指针传给 Dispatcher 接口导致 Go 逃逸分析强制堆分配；
- 生成 Dispatcher 在业务方法成功返回后先计算准确响应大小，再调用
  `(&response).Allocate(size)` 一次取得最终可写 Slice，然后把更新后的值返回 Runtime；
- `CallNotify` 传入零值 `ResponseWriter`，并且不能申请或编码响应；
- Dispatcher 返回后不能继续持有 `ctx`、`request`、`ResponseWriter` 或已分配 Slice；
- Runtime 管理请求和响应 Buffer 的最终释放，Dispatcher 不直接接触 BufferPool；
- Notify 仍使用同一静态方法分派；即使原方法带返回值，也不得编码或保留业务响应，目标
  业务错误和 panic 只进入目标侧诊断；
- Broadcast 到达每个目标后同样使用 `CallNotify`，不建立第三种 Dispatcher 分支；
- 未知 MethodID、解码失败、业务错误、panic 和编码失败均返回统一错误。

不使用原草案中的 `responseDst []byte`，原因是 Runtime 在业务方法执行前无法知道动态
响应的准确大小。提前传入固定容量会造成大 Buffer 浪费，容量不足时又会触发隐藏的 Go
堆扩容和二次复制。`ResponseWriter` 只保存当前 Runtime 和一个响应 Buffer 指针，不是
通用 `io.Writer`，也不引入接口分派；它使生成代码可以在业务方法返回后保持“一次申请、
一次写入、所有权明确”的低延迟路径。实现阶段的逃逸分析确认，原先传入
`*ResponseWriter` 会因接口调用保守逃逸；值传入/值返回后该对象不再逃逸，并使完整同
Node Await 每次调用减少一次分配。

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
或接口动态分派；M11 中普通结构体同样生成静态函数，不改变 Dispatcher 接口。

### 11.5 编译期 Codec 扩展接缝

M11 不允许业务注册或替换自定义 Codec，但生成器内部不能把类型判断、线布局和 Go 代码
拼接成无法替换的一段逻辑。内部类型模型至少区分：

- 类型 Schema；
- 大小计算计划；
- 编码计划；
- 解码计划；
- Codec 稳定标识和版本。

M11 只安装 Origin 内置计划。M12 增加自定义 Codec 时，由 `origingen` 在生成阶段为目标
类型选择自定义计划，并在生成代码中直接调用其静态函数。RPC Runtime 不建立按类型查找的
Codec Map，不在每次调用中做接口断言或动态分派。

Codec 稳定标识和版本必须进入包含该类型的完整契约指纹。相同 Go 类型切换 Codec 后，旧
节点与新节点在业务载荷解码前得到契约不匹配，不能让两种线格式静默互通。M12 可以扩展
生成器输入和公开接口，但不得改变 M11 内置 Codec 的既有线格式。

## 12. M11 数据类型

### 12.1 本里程碑支持

每个顶层输入和输出独立分类。M11 支持：

- `bool`；
- 有符号和无符号基础整数；
- `float32`、`float64`；
- `string`；
- `[]byte` 原始字节快速路径；
- 指向受支持类型的普通指针，并保留 nil 与非 nil 零值的区别；
- 元素类型受支持的数组和 Slice；
- Key 与 Value 均受支持的 Map；
- 只递归处理导出字段的普通 Go 结构体；
- 普通结构体或容器中嵌套的 Protobuf 生成结构体；
- 顶层 Protobuf 生成消息；
- 以上基础类型的具名定义类型和别名。

顶层 Protobuf 使用标准 Protobuf 编解码，保留 unknown fields、`optional`、`oneof` 和
Open、Hybrid、Opaque API 语义。

Map Key 与 M11 已支持的基础类型对齐，只支持 `bool`、有符号整数、无符号整数、
`float32`、`float64`、`string` 及其具名定义类型和别名。Protobuf Enum 属于具名整数，
因此可以作为 Map Key。Map Key 不单独扩展 M11 尚未支持的类型。

显式匿名空结构体 `struct{}` 可以编码为零字节。具名结构体如果没有任何可序列化导出字段，
必须由 `origingen` 报错；禁止把 `time.Time` 等依赖非导出字段表达逻辑状态的类型静默编码
为空。后续只能通过 M12 自定义静态 Codec 支持这类特殊类型。

### 12.2 M11 固定顺序线格式

M11 使用 Origin 自有的版本化固定顺序二进制格式，不为顶层输入或输出生成隐藏的
Protobuf 包装消息，也不在每次调用中重复写入参数 Tag、类型、数量或格式版本：

1. MethodID 已经唯一确定方法；
2. ContractFingerprint 已经精确约束输入和输出的数量、位置及声明类型，并在解码前完成
   兼容性检查；
3. 生成的静态编解码器已经知道每个位置的固定布局；
4. 逻辑参数位置号继续进入契约规范化字节和指纹，但不进入每次业务载荷；
5. 线格式版本进入 ContractFingerprint，版本不匹配在调用前失败。

所有固定宽度数值均使用小端序。M11 基础布局固定如下：

| Go 声明类型 | 线格式 |
|---|---|
| `bool` | 1 Byte，只接受 `0` 或 `1` |
| `int8`、`uint8` | 固定 1 Byte |
| `int16`、`uint16` | 固定 2 Byte |
| `int32`、`uint32` | 固定 4 Byte |
| `int64`、`uint64` | 固定 8 Byte |
| `int`、`uint` | 固定 8 Byte；目标架构解码时执行范围校验 |
| `float32` | IEEE 754 固定 4 Byte |
| `float64` | IEEE 754 固定 8 Byte |
| `string` | `uint32` 长度后紧跟 Go 字符串的原始字节 |
| `[]byte` | `uint32` presence/长度后紧跟原始字节 |
| 顶层 Protobuf | `uint32` presence/长度后紧跟标准 Protobuf 字节 |

有符号整数采用对应宽度的二进制补码，不使用 Varint 或 ZigZag。`int` 和 `uint` 在线上
统一为 64 位，避免 32 位和 64 位进程产生不同协议；32 位目标收到超出本机范围的值时返回
解码错误，不能截断。`uintptr` 与进程地址宽度和语义相关，M11 永久不支持。

`string` 不可为 nil，长度 `0` 表示空字符串；Origin 不额外校验 UTF-8，保持 Go string
可以承载任意字节的语义。`[]byte` 和可空的顶层 Protobuf 消息使用
下列 presence/长度规则：

- `0xFFFFFFFF`：nil；
- `0`：非 nil，内容长度为零；
- `1`～`0xFFFFFFFE`：后面紧跟对应长度的内容。

因此 nil `[]byte` 与非 nil 空 `[]byte`、nil Protobuf 指针与非 nil 空 Protobuf 消息都能
准确往返。顶层 Protobuf 的四字节 presence/长度属于 Origin 方法载荷，不属于 Protobuf
消息本身；非 nil 内容仍使用标准 `proto.MarshalAppend`/`proto.Unmarshal` 语义。编码器可以
先在目标 Buffer 中保留四字节，随后把 Protobuf 结果直接追加到同一 Buffer 并回填长度，
禁止为了增加外层长度再强制创建一份完整临时消息。

具名类型规则固定如下：

- `type PlayerID int64` 按 `int64` 布局编码，但契约规范化字节保留完整包路径、类型名和
  底层类型，不能与另一个具名 `int64` 类型意外匹配；
- `type PlayerID = int64` 属于别名，规范化为 `int64`；
- 预声明别名 `byte`、`rune` 分别规范化为 `uint8`、`int32`。

解码器必须在切片、分配和调用 Protobuf 解码前检查剩余长度及消息上限；必须拒绝截断、
非法 bool、长度越界、整数范围溢出和解完后的多余尾部字节。方法没有业务输入或没有业务
输出时，对应载荷可以为零字节；只要存在 `string`、`[]byte` 或顶层 Protobuf 位置，即使
内容为空也仍有四字节长度或 presence 标记。

本方案不追求在 Native RPC 中复刻 Protobuf 的可跳过未知字段能力。Origin 方法签名发生
变化时 ContractFingerprint 本来就会变化；需要独立演进多个字段时，应优先使用一个顶层
Protobuf 请求或响应。Protobuf 官方线格式通过 Tag/WireType 支持跳过未知字段，Varint
对小整数节省空间，但会增加变长解析路径：
[Protobuf Encoding](https://protobuf.dev/programming-guides/encoding/)。
外部 gRPC 插件负责协议适配，不要求 gRPC 与 Origin Native RPC 共用完整线格式。

编码 ABI 冻结前必须使用代表性的小整数、多参数和 Protobuf RPC 对固定顺序方案执行
Benchmark，记录耗时、分配和载荷大小。若结果表明性能与可维护性发生明显冲突，必须按
开发指导原则重新确认，不能由实现者静默更换线格式。

### 12.3 普通 Go 结构体和容器布局

普通 Go 类型继续使用固定顺序、小端、无运行时类型信息的线格式：

- 指针使用一字节 presence：`0` 表示 nil，`1` 表示后面紧跟所指向值，其他值非法；
- 普通 Slice 和 Map 使用四字节 `uint32` presence/元素数量：
  `0xFFFFFFFF` 表示 nil，`0` 表示非 nil 空容器，其余值表示元素数量；
- 数组长度由静态类型确定，不在线上重复编码；
- 普通结构体只编码导出字段，严格按 Go 声明顺序递归编码；
- 导出的匿名嵌入字段作为一个普通声明字段递归编码，不执行 JSON 风格扁平化；
- 小写非导出字段完全不进入 Schema 和线格式；
- Map 先编码元素数量，再按当前 Go Map 遍历顺序依次编码 Key 和 Value，不排序；
- 普通结构体或容器中的 Protobuf 生成类型按普通导出字段递归处理，不调用
  `proto.Marshal`。

普通结构体不要求 Tag，不维护字段 ID，也不在每条消息中写字段名、Tag、类型或长度。
结构体导出字段的追加、插入、删除、重排、重命名或类型修改都会改变完整契约指纹，并属于
不兼容修改。需要字段级兼容演进时，应改用顶层 Protobuf 请求或响应；普通 Go 结构体的不
兼容演进使用新版本 RPC 契约，或者协调新旧节点更新顺序。

Map 的编码字节不承诺跨进程或跨调用逐字节一致，只保证解码后的 Key/Value 语义一致。
契约指纹、生成文件和 golden test 不依赖运行时 Map 遍历顺序。该选择避免为每次编码创建
Key Slice、排序和产生额外分配。

### 12.4 解码安全上限

M11 在现有 RPC 最大消息长度之外增加不可配置的首版内部防护：

- 单个 Slice 或 Map 最多声明 `1048576` 个元素；
- 静态类型图最多嵌套 `64` 层；
- 循环或自引用类型在生成阶段拒绝，因此运行时不建立对象引用表或循环检测 Map；
- 所有字符串长度、字节长度和容器元素数量都必须先与剩余 payload、最大消息长度和元素
  上限交叉校验，再执行分配；
- 计算目标容量时必须检查整数加法、乘法和目标架构 `int` 溢出；
- 任一位置非法时终止本次解码，不向业务方法提交半初始化参数。

`1048576` 只限制单个容器，不改变 RPC 默认 `4M` 最大消息限制；绝大多数非零宽度元素会
先受消息长度约束。该内部上限主要阻止 `[]struct{}` 等极小元素声明制造超大循环或分配。

### 12.5 不支持类型与生成期失败

M11 首版不支持：

- `uintptr`；
- `complex64`、`complex128`；
- `unsafe.Pointer`；
- `interface{}`、`any` 以及其他接口类型；
- 函数和 Channel；
- 包含上述类型的指针、数组、Slice、Map 或普通结构体路径；
- 循环对象图；
- 嵌套在普通结构体或容器路径中的 Protobuf `oneof` 和 Opaque API；
- 自定义静态 Codec。

Map Key 同样只能使用第 12.1 节已经支持的基础类型及其具名类型。即使某种类型满足 Go
语言的 `comparable` 约束，只要它不在 M11 基础类型支持集合中，仍不得作为 Map Key。
因此 Map Key 不支持 `uintptr`、复数、指针、接口、Channel、数组或结构体。

`origingen` 扫描到不支持类型时必须在生成阶段终止，并列出 RPC 契约、方法、参数或返回
位置、容器以及具体字段组成的完整路径，同时说明不支持原因和可执行的修改建议。禁止生成
到一半才失败，禁止延迟到运行时失败，也禁止使用反射、JSON、空结构体或静默忽略作为回退。
自定义静态 Codec 属于 M12 的阶段性能力边界；其余本节类型属于首版明确不支持。

### 12.6 空载荷

没有业务输入或业务输出的方法可以使用零字节方法载荷。存在参数位置时，空字符串、
空 `[]byte` 和空 Protobuf 消息仍按第 12.2 节写入四字节长度或 presence 标记。M11 内部
调用描述始终包含 MethodID 和调用分类，不能把合法零字节方法载荷解释为缺少 RPC 请求。

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

### 13.6 Broadcast

`BroadcastXxx`：

1. 校验 Context、owner、Target 和输入参数；
2. 从当前 `rpc.Runtime` 的不可变本地注册快照取得全部同名、契约匹配且 Running 的目标；
3. 没有目标时返回 `CodeRPCNoRoute`；
4. 参数只编码一次；
5. M11 中同一 Node 的 ServiceName 唯一，因此本地候选最多一个，按 `CallNotify` 投递；
6. 队列接受后立即返回，不创建响应、Future、RequestID 或 pendingCall；
7. 目标业务错误、返回值和 panic 不回传，只在目标侧记录；
8. 服务发现接入后继续使用同一生成方法，把候选来源扩展为稳定发现快照，并按目标 Node
   复用同一编码结果。

M11 不建立“伪广播成功”或返回“不支持”的空壳方法。当前只有本地候选时也必须走真实
编码、准入、Dispatcher 和业务调用路径，以锁定后续多 Node 广播复用的 API 和语义。

### 13.7 Buffer 所有权

- 编码优先向 Application 已有 BufferPool 取得的 Buffer 追加；
- 提交成功后，请求 Buffer 所有权转移给目标任务；
- 提交失败时，调用方立即释放；
- Dispatcher 解码业务输入时，所有业务可见的 `[]byte` 都按实际长度复制为独立 Slice；
- nil `[]byte` 保持 nil，非 nil 空 `[]byte` 保持非 nil 空 Slice；
- 目标业务可以保存或修改收到的 `[]byte`，不会引用或污染请求 Buffer；
- Dispatcher 返回后，任何业务参数都不得继续引用输入 Buffer；
- Await/Async 响应由完成状态持有；生成解码器必须先把业务可见的 `[]byte` 复制为独立
  Slice，再释放响应 Buffer 和投递返回值或回调；
- Notify 目标任务处理完成后释放；
- Broadcast 在 M11 本地范围内与 Notify 使用相同单一所有权；后续多目标广播必须对同一
  只读编码结果建立明确的目标投递所有权，不能把一个可释放 Buffer 同时交给多个所有者；
- Protobuf 解码结果不能引用即将释放的输入 Buffer；
- 不因为本地调用而直接共享可变业务对象指针。

普通 `[]byte` 不使用 BufferPool 承载其业务可见结果，因为业务没有明确、可靠的归还时机。
这部分内存由正常 Go GC 管理。M11 不公开借用 Slice、Release 方法或 `BorrowedBytes` 类型；
只有真实性能 Benchmark 证明复制成为显著热点时，后续里程碑才单独设计显式零拷贝类型，
不能改变普通 `[]byte` 已确认的安全语义。

## 14. M11 本地调用状态

M11 不引入 M13 的 RequestID、pending 表、连接标识和断线完成。

Await 和 Async 仍需要一个最小本地完成状态，用于：

- 一次性完成；
- 保存编码结果或错误；
- 唤醒 Await；
- 投递 Async 回调；
- 管理请求和响应 Buffer 所有权。

该对象只在一次本地调用的直接参与方之间传递，不进入全局 Map。M11 暂称 `localCall`，
只负责同 Node 最小闭环；M13 的远程请求—响应路径使用按值存入会话 Map 的
`pendingCall`，两者共享一次性完成语义，但不强行合并成携带全部本地和远程字段的大对象。

是否池化 `localCall` 必须由 Benchmark、逃逸分析和失败路径复杂度决定。未证明稳定收益
前不为了理论零分配引入难以验证的 ABA 防护。

### 14.1 M11 对象池边界

M11 直接复用已经实现并验证的内部池：

- M2 `bufferpool`：请求和响应字节；
- M9 Service 私有 Task Pool：目标 RPC 任务和 Async 回调任务；
- M8 Deadline 条目池：默认超时与取消；
- M10 业务 Timer 池：RPC 不建立第二套 Timer 对象。

M11 不自动池化：

- 业务普通结构体、Map、Slice 和业务可见 `[]byte`；
- Protobuf Request、Response 和嵌套消息；
- Context、error、回调闭包；
- `rpc.Target` 和生成客户端值；
- 编码 Reader/Writer 小对象，它们应保持栈上值语义；
- 只读 Descriptor 和生成常量，它们与 Runtime 同生命周期。

`localCall` 是 M11 唯一新增的池化候选。实施顺序固定为：

1. 先实现不池化且生命周期清晰的正确性基线；
2. 保存成功、超时、取消、目标 panic 和停止竞争场景的逃逸分析与 Benchmark；
3. 再实现 Node 私有 `sync.Pool` 对照；
4. 只有完整 Reset、防迟到完成和消费者结束条件保持清晰，并且分配、GC 或尾延迟存在稳定
   收益时才启用；
5. 数据不能证明收益或状态机明显变复杂时，M11 保持不池化。

M13 最终 `pendingCall` 以值存入原物理会话 Map，跨平台 Benchmark 均为零分配，因此没有
增加专用池。RequestID 与会话 Map 仍负责隔离迟到事件。不能为了形式复用 M13 代码，把
RequestID、远程 pending Map 或连接状态塞入 M11 `localCall`。

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

- `CodeRPCContractMismatch = 2004`；
- `CodeRPCMethodNotFound = 2005`；
- `CodeRPCEncodeFailed = 2006`；
- `CodeRPCRequestDecodeFailed = 2007`；
- `CodeRPCResponseDecodeFailed = 2008`；
- `CodeRPCExecutionPanic = 2009`；
- `CodeRPCBroadcastPartialFailed = 2010`。

完整语义见[统一错误码设计](../details/2026-07-24-统一错误码设计.md)。M11 本地 Broadcast
候选最多一个，不会产生部分成功；`2010` 先固定编号，供后续多 Node 广播聚合错误复用。

固定错误优先复用只读哨兵。具体 NodeID、ServiceName、契约名、方法名和生成字段路径只
进入结构化日志或生成期诊断，不为高频失败构造通用 Details Map。

所有生成调用都必须保留最终 `error`：

- `AwaitXxx` 的最后一个返回值是业务或框架 `error`；
- `AsyncXxx` 自身返回立即提交 `error`，强类型回调的最后一个参数返回提交成功后的最终
  业务或框架 `error`；
- `NotifyXxx` 和 `BroadcastXxx` 直接返回本地接受阶段的框架 `error`，不返回远端
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

生成编解码器必须：

- 为每个受支持类型生成静态大小计算、编码和解码路径；
- 先计算最终 payload 大小，再从 BufferPool 取得目标 Buffer，一次写入最终位置；
- 顶层 Protobuf 使用 `proto.Size` 和官方 Append API 直接追加到最终 Buffer；
- 普通结构体和容器直接写入最终 Buffer，不生成隐藏 Request/Response 包装对象；
- `string` 直接复制到目标 Buffer，不先构造临时 `[]byte`；
- 固定宽度数字使用可内联的小端写入，具体使用标准库函数还是生成位运算由 Benchmark
  决定，禁止无数据引入 `unsafe`；
- 解码先校验长度、元素数量和整数溢出，再分配 Slice、Map 或 Protobuf；
- Notify 不创建响应 Buffer，Broadcast 的同一参数集合只执行一次编码；
- 业务可见 `[]byte` 的一次安全复制属于已确认所有权成本，不能为了表面零拷贝破坏生命周期。

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
15. 所有不支持类型均在生成期得到包含完整字段路径的清晰错误；
16. 任一可达字段不支持时整次生成失败且没有部分写入；
17. `--check` 能发现缺失、过期和多余生成文件且不修改工作树；
18. 四类返回签名严格生成第 7.3 节规定的方法集合；
19. M11 的 Async、Await、Notify 和 Broadcast 最终 `error` 位置不会遗漏。

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
21. nil Context、Async nil callback 和零值客户端直接返回错误且不产生调用或回调；
22. Broadcast 对本地匹配目标真实投递且不等待业务结果；
23. Broadcast 没有目标、指定其他 Node、目标队列满和目标 panic 的既定语义；
24. Broadcast 参数只编码一次且所有 Buffer 在成功、失败和停止路径恰好释放一次。

### 18.3 数据类型

至少覆盖：

- 每种基础标量；
- 空和非空 string；
- nil、空和非空 `[]byte` 的既定边界；
- Service 保存或修改输入 `[]byte` 后，请求 Buffer 释放和复用不会改变该 Slice；
- Await 返回值和 Async 回调保存或修改 `[]byte` 后，响应 Buffer 释放和复用不会改变该
  Slice；
- 顶层 Protobuf 空消息；
- 顶层 Protobuf unknown fields、optional、oneof 和 Opaque API；
- 多输入、多输出和混合基础类型/Protobuf；
- 普通指针的 nil/零值语义；
- 空、nil 和非空 Slice、Map；
- 数组、普通结构体和多层嵌套结构体；
- `map[int64]pb.PlayerProfile`、`map[int64]*pb.PlayerProfile`、Protobuf Slice 和指针；
- 嵌套 Protobuf 只处理导出字段且不调用 Protobuf 编解码；
- `uintptr`、复数、`unsafe.Pointer`、接口、函数、Channel、循环对象图以及非法 Map Key
  均在生成阶段失败，并包含完整类型路径；
- `struct{}`、无导出字段具名结构体、匿名嵌入字段和 `time.Time` 拒绝规则；
- Slice/Map 元素数量上限、64 层类型深度、截断、整数溢出和分配前校验；
- Map 多次编码可以具有不同字节顺序，但往返语义一致且生成文件保持确定。

### 18.4 性能

至少保存：

- Target 构造的 `ns/op`、`allocs/op` 和 `B/op`；
- 基础类型编码和解码；
- 顶层 Protobuf 编码和解码；
- Dispatcher 命中；
- 生成客户端构造时取得 Runtime 的成本和分配；
- 同 Node Await、Async、Notify；
- 同 Node Broadcast；
- 自调用和跨 Service 调用；
- 基础类型、普通结构体、Slice、Map、嵌套 Protobuf 的大小计算、编码和解码；
- 小消息、普通消息和接近 `4M` 上限消息；
- Map 不排序与排序对照，确认正式实现没有隐藏 Key Slice 和排序分配；
- 目标不存在和队列满失败路径；
- 不池化与池化 `localCall` 的对照；
- P50、P95、P99 延迟；
- GC 次数、暂停和突发负载结束后的堆回落；
- Windows 与 Linux 的可复现环境和结果。

性能验收重点不是追求脱离业务的绝对 QPS，而是确认热路径没有运行时反射、每调用辅助
goroutine、隐藏 Timer、运行时 Codec 查找和可避免的多次 payload 复制。任何为了性能加入
的对象池、额外缓存、手写位运算或特殊分支都必须由对照 Benchmark 证明净收益，并且不能
破坏代码清晰度、所有权和失败路径正确性。

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
和 Node 装配，不能把它们扩张成另一套手写 RPC API。M11 不公开只读 Descriptor；等监控、
调试或插件出现真实消费者后再单独 Review。

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

## 20. M11 Review 已确认

2026-07-28 按开发指导原则完成逐节 Review。以下影响生成 ABI、线格式、所有权和低延迟
实现的结论均已确认并回写：

| 顺序 | Review 问题 | 当前结论或建议 | 状态 |
|---|---|---|---|
| 1 | 基础类型线布局、位置字段、`int`/`uint`、具名类型、nil/空值和顶层 Protobuf presence | 采用第 12.2 节固定顺序格式；参数位置只进入指纹；`int`/`uint` 为 64 位线值；具名定义类型保留身份；`[]byte` 和 Protobuf 明确区分 nil 与空值 | 已确认 |
| 2 | `[]byte` 输入解码后是借用请求 Buffer 还是复制为业务独立内存 | 普通 `[]byte` 输入、Await 返回值和 Async 回调值全部复制为业务独立 Slice；nil/空语义保持；不使用 BufferPool 承载业务 Slice；M11 不公开借用类型 | 已确认 |
| 3 | RPC 契约可见性、泛型和跨包实现桥接 | RPC 接口和方法要求导出、首版禁止泛型契约；契约包生成 `New<Contract>Dispatcher(impl Contract)`，Service 包只生成 `RPCDispatcher()` 薄适配，避免重复 Codec | 已确认 |
| 4 | Dispatcher 如何区分请求—响应与 Notify | 使用两个有效值的轻量 `rpc.CallKind`；Await/Async 使用 `CallRequest`，Notify/Broadcast 使用 `CallNotify`；零值非法 | 已确认 |
| 5 | Await/Async Deadline 与 M8 接入 | 调用方显式 Deadline 原样使用；无 Deadline 时由 M8 默认链提供唯一计时；Async 共享每 Node DeadlineQueue，不为每次调用建立第二个 Timer | 已确认 |
| 6 | `localCall` 字段、完成同步和池化 | M11 只保留一个私有一次性完成状态并保持未池化；M13 `pendingCall` 以值存入会话 Map，零分配基线证明无需对象池 | 已确认 |
| 7 | RPC 错误码、业务错误编码、panic 和本地/远端一致性 | 固定 `2004–2010` 契约、方法、编解码、执行 panic 和广播部分失败错误；同 Node 也按相同错误语义处理，不直接传 Go error 指针 | 已确认 |
| 8 | ContractID、MethodID 和完整指纹的规范化字节 | MethodID 只包含契约名和方法名；完整指纹精确检查签名、Schema、Codec 标识和格式版本；规范化字节必须由 golden test 锁定 | 已确认 |
| 9 | Go 包加载和 Protobuf 依赖 | 固定 `golang.org/x/tools/go/packages` 与 `google.golang.org/protobuf` 版本；Protobuf 优先使用官方 Append/Options API；具体固定版本在实施计划前查验 | 已确认 |
| 10 | 旧生成文件清理和生成 ABI 版本 | 只删除完整标记且本轮确认不再需要的文件；生成代码加入 ABI 校验；增加 `origingen rpc --check ./...` 检查缺失、过期和多余文件 | 已确认 |
| 11 | 是否公开只读 Descriptor | M11 不公开；生成代码和 Runtime 内部只保留最小描述，出现监控、调试或插件真实消费者后再 Review | 已确认 |
| 12 | 普通 Go 类型、Map Key 和嵌套 Protobuf 的 M11 范围 | M11 一并支持普通指针、数组、Slice、Map、普通结构体和嵌套 Protobuf；Map Key 与已支持基础类型对齐；`uintptr`、复数、`unsafe.Pointer`、接口、函数和 Channel 在 `origingen` 生成期失败 | 已确认 |
| 13 | 普通结构体字段协议、Map 顺序和解码安全上限 | 导出字段固定声明顺序，无 Tag/字段 ID；Map 不排序；循环类型生成期拒绝；单容器最多 `1048576` 个元素，类型图最多 64 层 | 已确认 |
| 14 | Broadcast 是否在 M11 生成 | 所有 RPC 都生成 `BroadcastXxx`；M11 对当前 Runtime 已知本地目标执行真实通知投递，后续只扩展发现快照来源 | 已确认 |
| 15 | 不支持类型与自定义 Codec 扩展 | `origingen` 先验证完整类型图再原子生成；M11 不开放自定义 Codec，但保留生成期静态计划，M12 生成直接调用且 Codec 标识进入指纹 | 已确认 |
| 16 | M11 池化和编码性能 | 复用 Buffer/Task/Deadline/Timer 池；业务对象不池化；`localCall` 数据决定；生成 Codec 直接写最终 Buffer，并保存跨平台分配和尾延迟基线 | 已确认 |
| 17 | Dispatcher 如何在未知响应大小时取得最终 Buffer | 使用 Runtime 栈上的具体 `ResponseWriter` 值传入和值返回；生成代码先精确计算大小再一次 Allocate；Notify 传零值，不使用固定预留 Slice 或隐藏堆扩容，并避免接口指针参数导致堆逃逸 | 已确认 |

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
使两条路径都只保留一个物理计时器。M11 实施计划必须把该修正列为第一个前置步骤；该步骤
通过回归与 Benchmark 前，不得开始 RPC 生成器或 Runtime 编码。

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
    -> Await 恢复 / Async 回调 / Notify 或 Broadcast 完成
```

这一闭环优先验证业务接口、生成结果、数据边界和 Service 单执行权，不提前加入网络、发现
和复杂路由。M12～M14 只在该稳定基础上依次补齐自定义 Codec、TCP 和 NATS，不能重新
发明客户端外观、Dispatcher 语义或 M11 已确定的数据表示。

## 22. 实施与验收结果

M11 于 2026-07-28 按本文范围完成实现，实际交付包括：

1. `rpc` 公开包的 Target、稳定 ID、指纹、静态 Codec 基础、ResponseWriter、Client 和
   每 Node 独立 Runtime；
2. `cmd/origingen` 与 `internal/rpcgen`，支持 `origingen rpc ./...` 和
   `origingen rpc --check ./...`；
3. 生成前完整签名和类型图校验、SHA-256 截断碰撞检查、确定性文件内容、旧生成文件
   Overlay、临时文件替换和多余文件清理；
4. 基础类型、具名类型、普通指针、数组、Slice、Map、普通结构体、顶层 Protobuf 和嵌套
   Protobuf 普通结构路径的静态编解码；
5. Await、Async、Notify、Broadcast、同 Service 自调用、精确 Node + Service、本地错误、
   panic、超时、取消、队列过载和 Buffer 所有权闭环；
6. M9 唯一计时器差异修正：显式 Deadline 只使用调用方 Go Timer，无显式 Deadline 时只
   使用一条 M8 Deadline，并由轻量 Context 正确公开 `Deadline()`。

### 22.1 Async 回调预约的最终实现

实施时没有向 Scheduler 增加新的 Reserved Task 状态或第二套 Future 调度器。最终采用更
精简的方式：

1. Async 在提交目标请求前，先向调用方 Service 的普通有界 FIFO 投递一个内部完成任务；
2. 该任务与业务 `DispatchAsync` 共用 `max_tasks`、停止排空、panic 边界和统计；
3. 目标提交成功后打开提交门闩，完成任务通过现有 `Service.Await` 等待唯一结果；
4. 目标提交立即失败时打开中止门闩，内部任务自行结束且不调用业务 callback；
5. 返回 nil 后 callback 在该完成任务恢复调用方 Service 执行权后严格执行一次；
6. 显式 Context Deadline 继续只使用原 Go Timer；无 Deadline 时继续只使用调用方
   Service 的 M8 默认 Deadline。

这一实现既在目标提交前真正预约了回调容量，又没有复制 Scheduler 的任务状态、Deadline
绑定和停止逻辑。

### 22.2 panic 和日志的最终边界

请求—响应 RPC 的业务 panic 在 RPC Dispatcher 外层转换为
`CodeRPCExecutionPanic`，同时由目标 Runtime 记录一条带业务堆栈的错误日志，不再重新
抛给 Service 根任务边界产生第二条重复日志。Notify/Broadcast 的业务 error 或 panic
同样只进入目标侧诊断，不回传给已经完成接受阶段的调用方。

### 22.3 Buffer 与 localCall 池化结论

请求和响应 Buffer 复用 Application 共享的 M2 BufferPool；Service Task、Deadline 和
Timer 继续复用现有池。普通业务结果不池化。

`localCall` 保持不池化，原因不是遗漏，而是已测量后的明确决定：

- Await 状态只需要一个完成 Channel，Windows 基线约 `131～140ns/op`、
  `208B/op`、`2 allocs/op`；Linux 约 `73.6～75.5ns/op`、`208B/op`、`2 allocs/op`；
- Async 还需要提交和中止门闩，Windows 约 `258～270ns/op`、`432B/op`、
  `4 allocs/op`；Linux 约 `137.6～138.2ns/op`、`432B/op`、`4 allocs/op`；
- Channel 到达终态后已经关闭，不能直接复用；仅池化外层小对象最多减少一次分配，却要
  增加代次、晚到响应和 ABA 防护状态机；
- 当前收益不足以抵消代码复杂度和错误风险，因此遵守开发原则保持未池化基线。M13 已对
  真实并发表中的远程 `pendingCall` 独立测量；值类型 Map 基线为零分配，最终同样不池化。

### 22.4 性能基线

Go 1.26.5、Windows amd64、AMD Ryzen 7 7840HS：

| 项目 | 结果 |
|---|---:|
| Target 构造 | `约 12.3～12.6ns/op`，`0 B/op`，`0 allocs/op` |
| 24B 基础 Codec 往返 | `约 34.5～35.9ns/op`，`0 B/op`，`0 allocs/op` |
| 同 Node 生成 Await 闭环 | `约 4.1～4.4us/op`，`1243 B/op`，`23 allocs/op` |
| 16B `[]byte` Codec 往返 | `约 51.9～54.0ns/op`，`16 B/op`，`1 allocs/op` |
| 1KB `[]byte` Codec 往返 | `约 407～423ns/op`，`1024 B/op`，`1 allocs/op` |
| 接近 4M `[]byte` Codec 往返 | `约 0.90～0.98ms/op`，`约 8.0 MiB/op`，`3 allocs/op` |

Go 1.26.5、Ubuntu 26.04 linux/amd64、QEMU Virtual CPU：

| 项目 | 结果 |
|---|---:|
| Target 构造 | `约 12.36～12.50ns/op`，`0 B/op`，`0 allocs/op` |
| 24B 基础 Codec 往返 | `约 47.90～48.48ns/op`，`0 B/op`，`0 allocs/op` |
| 同 Node 生成 Await 闭环 | `约 3.64～3.77us/op`，`1242 B/op`，`23 allocs/op` |
| 同 Node Await P50 | `约 2.91～2.95us` |
| 同 Node Await P95 | `约 6.08～6.37us` |
| 同 Node Await P99 | `约 10.66～11.29us` |

同 Node 闭环包含请求大小计算、BufferPool、编码、目标 Service FIFO、静态 Dispatcher、
解码、业务方法、响应编码、调用方 Await 恢复和结果解码，不是直接函数调用数据。

最终逃逸分析确认 `ResponseWriter` 值传入/值返回后不再逃逸。与最初的接口指针方案相比，
完整闭环从 `24 allocs/op` 降至 `23 allocs/op`；业务结果、闭包、Task Context 和
`localCall` 仍按其跨调度生命周期发生必要逃逸，不使用不安全技巧规避。

### 22.5 质量门禁

已通过：

- `origingen rpc --check ./...`；
- `gofmt` 与 `go vet ./...`；
- Windows `go test ./...` 和 `go test -race ./...`；
- Ubuntu 26.04、Go 1.26.5 离线固定依赖环境下的 `go test ./...`、
  `go test -race ./...` 和上述 Benchmark；
- `linux/amd64` 与 `darwin/arm64`、`CGO_ENABLED=0` 的全 Module 交叉编译；
- Reader 随机截断、伪造长度和非法载荷 Fuzz；
- RPC 单元与生成集成合并覆盖率约 `82.9%`；生成器包覆盖率约 `84.9%`；
- 多 Node Runtime 隔离、队列满、晚到响应、Async 严格一次、超时、panic、自调用和
  BufferPool 未归还统计测试。

### 22.6 最终代码复核补充

最终提交前的逐路径复核另外修正并锁定：

1. Async 请求已经提交、但内部完成任务开始前 Context 取消时，显式放弃调用并归还已经
   到达或之后到达的响应 Buffer；
2. Async 目标提交失败优先于同时发生的 Context 取消，保证“返回非 nil error 后业务
   callback 绝不执行”；
3. 生成 Async 方法在编码前拒绝 nil callback，避免把使用错误推迟成工作任务 panic；
4. RPC 契约与 Service 分包时，纯实现包不生成未使用的 `context` 导入；
5. 类型别名和泛型 RPC 契约返回生成期错误，不会让生成器自身 panic；
6. `--check` 不修改磁盘；正式生成只替换或删除带完整 origingen 标记的文件，并拒绝覆盖
   同名手写文件；
7. 生成 ABI 使用两个方向的无符号常量约束，只有 Runtime ABI 与生成版本严格相等时才能
   编译，升级和降级都不会静默通过。
