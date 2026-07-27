# M11 RPC 契约与代码生成设计

> 文档状态：讨论中  
> 创建日期：2026-07-27  
> 最后更新：2026-07-27  
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
10. M11 生成 `AsyncXxx`、`AwaitXxx` 和 `NotifyXxx`，Broadcast 延后；
11. 所有生成调用最终都具有统一 `error` 语义；
12. 未显式提供 Deadline 时最终使用 Origin 内置 `15s`；
13. 客户端不持有 TCP/NATS 连接，目标只描述逻辑路由；
14. 客户端统一使用一个构造函数和一个具体 `rpc.Target` 值对象；
15. ContractID 和 MethodID 使用生成期 SHA-256 截断值，发现碰撞时生成失败；
16. 完整 SHA-256 契约指纹保留给兼容性检查和后续 Node 握手；
17. 不允许手工覆盖 ContractID 或 MethodID。

## 4. M11 交付范围

### 4.1 必须实现

- `cmd/origingen` 命令入口；
- `internal/rpcgen` 生成器实现；
- 公开 `rpc` 包的最小类型和生成代码调用边界；
- 模块级 `origingen rpc ./...` 扫描；
- `//origin:rpc` 契约发现和完整签名校验；
- ContractID、MethodID 和契约指纹生成；
- 强类型客户端；
- `rpc.Target`、`rpc.ToService` 和 `rpc.ToNodeService`；
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
| 完整 Stop 排空、OnStop Await RPC 和异常进程收尾 | M15 |
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
- `AsyncGetPlayer`、`AwaitGetPlayer`、`NotifyPlayerOnline`；
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
    rpc.ToNodeService("player-2", "PlayerService"),
)
```

两种写法返回相同的 `PlayerRPCClient`。目标差异属于数据，不通过第二个客户端类型或第二组
生成方法表达。

### 5.4 生成调用方法

请求—响应方法生成：

```go
player, err := client.AwaitGetPlayer(ctx, playerID)

client.AsyncGetPlayer(
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
```

完全没有返回值的方法生成：

```go
err := client.NotifyPlayerOnline(ctx, playerID)
```

M11 不生成：

- `AwaitNodeGetPlayer`；
- `AsyncNodeGetPlayer`；
- `NotifyNodePlayerOnline`；
- `BroadcastPlayerOnline`。

目标由 `rpc.Target` 决定，不能把 NodeID、ServiceName 重复加入每个 RPC 方法。

## 6. 单客户端与 Target 设计

### 6.1 公开外观

`rpc.Target` 是具有不可变使用语义的具体小型值对象：

```go
package rpc

type Target struct {
    // 字段保持未导出。
}

func ToService(serviceName string) Target

func ToNodeService(
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
- `ToNodeService(nodeID, "PlayerService")` 只在 `nodeID` 等于调用方所属 NodeID 时继续
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
6. 完全没有返回值的方法被识别为 Notify；
7. 只返回 `error` 的方法仍是请求—响应 RPC。

### 7.3 生成分类

| 服务端签名 | M11 生成 |
|---|---|
| 一个或多个业务结果 | `AsyncXxx`、`AwaitXxx`，追加最终 `error` |
| 业务结果加末尾 `error` | `AsyncXxx`、`AwaitXxx`，复用最终 `error` |
| 只返回 `error` | `AsyncXxx`、`AwaitXxx` |
| 完全无返回值 | `NotifyXxx` |

Broadcast 的契约语义已经保留，但 M11 不生成 Broadcast 方法，避免在没有服务发现目标
快照时建立不完整实现。

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

以上名称表达职责，最终公开签名仍需在 M11 后续 Review 中确认。该接口由 Node 使用方
定义，不建立包级全局 `IRPCService` 注册表。

### 10.4 Node 注册

Node 在实例装配冷路径：

1. 按已经确定的 Service 配置创建实例；
2. 检查实例是否实现 Dispatcher Provider；
3. 校验 Dispatcher 的 ContractID、MethodID 和指纹；
4. 以实际 ServiceName 保存到当前 Node 私有目录；
5. 重复 ServiceName、重复方法或描述不一致时使 Node 启动失败；
6. Node 停止时随 Service 目录一起释放。

注册目录属于 Node 实例，不使用包级可变全局状态。同一进程中的多个 Application 和 Node
互不污染。

## 11. Dispatcher

### 11.1 静态分派

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

### 11.2 Dispatcher 职责

生成 Dispatcher 只负责：

1. 校验 MethodID；
2. 调用生成解码器；
3. 以静态类型调用真实 Service 方法；
4. 捕获并转换业务方法边界 panic；
5. 编码业务结果和最终错误；
6. 按统一所有权规则释放输入和临时 Buffer。

目标查找、Service 状态、队列准入、Await 恢复和未来 Transport 不复制到每个
Dispatcher。

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

调用方的 Service Task Context 只能由调用方当前任务使用，不能直接传给目标 Service 或
其他 goroutine。

本地 RPC 必须：

1. 从调用 Context 提取有效 Deadline、取消状态和后续协议需要的不可变元数据；
2. 为目标 Service 任务使用目标 Scheduler 提供的任务 Context；
3. 在目标任务中组合 RPC Deadline 语义；
4. Async 回调使用调用方 Scheduler 新建的回调任务 Context；
5. 不让目标任务持有调用方 Task Context 的私有执行权令牌。

如何在不创建每调用 goroutine、标准库 Runtime Timer 或第二套 Deadline 系统的情况下
传播本地 RPC Deadline 和取消，仍属于 M11 后续必须确认的实现细节。

### 13.3 Await

`AwaitXxx`：

1. 校验 Target 并编码请求；
2. 把请求投递到目标 Service Ready FIFO；
3. 使用调用方 Service 已有 `Await` 原语释放执行槽；
4. 当前任务 goroutine 等待本地调用完成；
5. 目标 Service 取得执行槽后解码并调用业务方法；
6. 目标编码结果或错误并完成本地调用；
7. 原任务进入调用方 Ready FIFO；
8. 原任务重新取得执行权后解码结果并返回。

不为每次 Await 创建辅助 goroutine。同一个 Service Await 调用自身 RPC 时也按上述流程
释放执行槽，因此目标任务可以正常执行，不形成直接递归或执行权死锁。

### 13.4 Async

`AsyncXxx`：

1. 编码并提交目标任务；
2. 不释放调用方当前执行槽；
3. 生成方法立即返回；
4. 目标完成后把强类型回调投递到调用方 Service Ready FIFO；
5. 即使目标立即失败或立即完成，回调也不能在当前调用栈内执行；
6. 回调取得调用方 Service 执行权后才能访问 Service 状态；
7. 每次调用最多发布一次回调。

M11 只处理 Running 期间的基本回调。Draining、停止期间的新调用和回调排空由 M15 完整
实现。

### 13.5 Notify

`NotifyXxx`：

1. 编码请求；
2. 提交目标 Service Ready FIFO；
3. 队列接受后立即返回 `nil`；
4. 不等待目标业务开始或结束；
5. 不创建响应、Future、RequestID 或 pendingCall；
6. 目标业务错误或 panic 只在目标侧记录；
7. 编码失败、无目标、契约不匹配、Service 非 Running 或队列满直接返回相应错误。

M11 Notify 的“发送成功”表示目标本地队列已经接受，不表示业务成功。

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

该对象只在一次本地调用的直接参与方之间传递，不进入全局 Map。暂称 `localCall` 仅用于
讨论，不提前锁定最终类型名。

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
2. `rpc` 不导入 `node`；
3. 生成代码不能导入 Origin 的 `internal` 包；
4. Node 只通过最小 Dispatcher Provider 接口识别 RPC Service；
5. 不为了隐藏所有生成代码入口建立大型抽象层；
6. 只有业务或生成代码确实需要的类型才公开；
7. 是否建立独立 `internal/rpcruntime`，只有在实现证明能够降低职责耦合且不造成生成代码
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
- 方法表构建。

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
15. M12 类型得到清晰的阶段性错误。

### 18.2 本地 RPC

至少覆盖：

1. ToService 调用同 Node Service；
2. ToNodeService 指定当前 Node；
3. ToNodeService 指定其他 Node 返回无路由；
4. 空 Target 和非法名称；
5. 目标不存在；
6. 目标契约不匹配；
7. 目标 Service 未 Ready、Stopping、Stopped；
8. 目标队列满；
9. Await 正常结果、业务错误、框架错误、超时和取消；
10. Await 释放和恢复调用方执行槽；
11. Service Await 调用自身 RPC 不死锁；
12. Async 回调不抢占当前任务且最多一次；
13. Async 立即失败也异步投递回调；
14. Notify 只等待队列接受；
15. 请求—响应业务方法 panic 返回统一错误；
16. Notify panic 只产生目标侧诊断；
17. 输入、响应和失败路径 Buffer 全部释放；
18. 停止后没有 goroutine、Timer 或 Buffer 泄漏。

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

var playerRPCDescriptor = rpc.ContractDescriptor{
    // 生成期常量和只读描述。
}

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
            playerRPCDescriptor,
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

业务不直接依赖 `rpc.Client`、Descriptor 或 Dispatcher 的低层调用方法。公开这些类型只为
生成代码、Node 装配或诊断确有需要，不能把它们扩张成另一套手写 RPC API。

## 20. 后续 M11 Review 项

以下问题已经定位，但尚未最终确认。每次只讨论一个问题，确认后立即回写本文：

1. `rpc.Dispatcher`、生成客户端底座和生成 Codec 的最小精确接口；
2. `service.IService` 与 `rpc` 取得 NodeID、本地 Service 和调度入口的最简依赖边界；
3. 本地 RPC Deadline 和取消如何复用 M8 TimerEngine，且不传递调用方 Task Context；
4. Async 是否只使用 Context 取消，还是额外返回公开取消句柄；
5. M11 基础类型的精确线布局、`int`/`uint` 跨平台规则和具名别名；
6. `localCall` 的字段、完成同步原语以及是否池化；
7. M11 新增错误码的名称、编号和 panic 映射；
8. Go 包加载是否固定使用 `golang.org/x/tools/go/packages` 及具体版本；
9. Protobuf 依赖版本和高性能 Marshal/Unmarshal API；
10. 接口标记被删除后，旧 `origin_rpc.gen.go` 的安全清理规则；
11. 完整契约指纹的精确规范化字节布局；
12. M11 是否需要公开只读 Descriptor 诊断接口。

在以上内容完成确认、本文状态改为“已确认”并在复核清单记录“允许实施”之前，不创建 M11
实施计划，不编写 M11 代码。

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
