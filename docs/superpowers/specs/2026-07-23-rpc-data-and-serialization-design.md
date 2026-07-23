# Origin v3 RPC 数据类型与序列化设计

## 1. 文档范围

本文记录 Origin v3 RPC 在接口定义、数据类型、序列化、传输边界和低延迟方面已经确认的设计。服务发现、服务调度模型和流式 RPC 不属于本文范围。`Await` 与任务恢复规则见 [Origin v3 Service 协作式调度设计](./2026-07-23-service-cooperative-scheduling-design.md)，Deadline 的统一定时机制见 [Origin v3 定时器系统设计](./2026-07-23-timer-system-design.md)。

## 2. 设计目标

1. 业务开发者使用 Go 接口定义 RPC，不要求使用 `.proto` 定义 Origin Native RPC。
2. RPC 调用外观保持统一，不区分“普通 RPC”和“低延迟 RPC”。所有 RPC 底层都按低延迟目标实现。
3. 允许基础类型、普通 Go 结构体、Protobuf 生成的 Go 结构体以及它们的合法组合。
4. 不使用 msgp，不为业务项目增加额外的序列化生成器。业务侧只使用 `origin-gen`。
5. TCP 与 NATS 使用同一套 Origin Native RPC 语义；外部 gRPC 通过可选插件提供。
6. 优先保持代码精简、清晰和可维护。性能与可维护性发生冲突时，先提供方案和基准依据，再由开发者确认取舍。

## 3. RPC 定义和调用模型

Origin Native RPC 使用 Go 接口作为契约，由 `origin-gen` 在编译前生成：

- 强类型客户端；
- 服务端注册代码；
- 方法描述信息；
- TCP 与 NATS 适配代码；
- 静态序列化与反序列化代码。

第一版支持：

- 一元请求与响应；
- 异步调用；
- 单向通知。

第一版不支持流式 RPC。

### 3.1 Context、Deadline 与默认超时

所有由 `origin-gen` 生成的 RPC 调用函数都接受 `context.Context`，并统一遵循以下规则：

1. 调用方传入的 Context 已有 Deadline 时，原样继承该 Deadline，不再附加默认超时；
2. Context 没有 Deadline 时，依次查找当前 Service 默认值、当前 Node 默认值；
3. 前两级均未配置时，使用 Origin 内置默认超时 `15s`；
4. 调用方可以通过显式 Deadline 设置比默认值更短或更长的超时，默认值不能反向截断显式 Deadline；
5. 优先级固定为：`调用方显式 Deadline > Service 默认值 > Node 默认值 > Origin 内置 15s`。

`15s` 是防止请求无限等待的最终兜底值，不是业务目标延迟。Redis、数据库、战斗服内部 RPC 等延迟敏感路径应根据业务要求显式设置更短的 Deadline。

一次请求与响应 RPC 的超时范围从调用进入 RPC 客户端开始，覆盖本地排队、路由、传输、服务端处理、响应传输和客户端完成 Future 的全过程。异步 RPC 同样适用：调用函数可以立即返回 Future，但 Future 最迟在有效 Deadline 到达时完成为超时。

单向通知没有远端响应，但生成的调用函数仍使用相同的有效 Deadline，约束本地路由、排队和发送过程；发送完成后不等待远端业务执行结果。

如果 RPC 在 `Await` 内执行，`Await` 传入的 Context 已包含有效 Deadline，RPC 直接继承，不重复创建另一套默认超时。RPC 本地超时后的远端取消协议属于后续 RPC 调用语义设计，不能影响客户端按时完成。

## 4. 传输边界

### 4.1 Origin Native RPC

Origin Native RPC 支持 TCP 和 NATS。单次部署选择一种 Native 传输模式，RPC 接口和业务代码不因传输方式变化。

### 4.2 外部 gRPC

gRPC 作为可选适配插件，可以与 Origin Native TCP 或 NATS 同时启用。Origin 核心不依赖 gRPC。

可暴露为标准 gRPC 的方法，其顶层请求和响应必须是 Protobuf 消息。普通 Go 结构体即使嵌套了 Protobuf 结构体，也不会自动成为标准 gRPC 契约。

## 5. 统一类型处理原则

`origin-gen` 根据 Go 静态类型生成代码，热路径不使用运行时反射，也不静默回退到 JSON。

基础规则如下：

- 基础整数、浮点数、布尔值和字符串使用 Origin 内置二进制编码；
- `[]byte` 使用原始字节快速路径；
- 指针保留 `nil` 和零值的区别；
- 普通 Go 结构体只处理导出字段；
- 结构体中的小写非导出字段和运行时内部字段不进入线协议；
- Slice、数组和 Map 递归处理其元素、键和值；
- 不支持的类型必须由 `origin-gen` 在生成阶段报出完整字段路径；
- 用户可以通过 Origin 自定义静态编解码接口扩展特殊类型。

Go 结构体字段编号由 Origin 的协议描述信息稳定维护。调整字段声明顺序不改变字段编号；删除字段后编号不得复用；不兼容类型修改在生成阶段失败。

## 6. Protobuf 生成结构体按普通 Go 结构体处理

在 Origin Native RPC 中，Protobuf 生成的 Go 结构体不调用 `proto.Marshal`，也不解析 Protobuf 线协议。它与普通 Go 结构体使用相同规则：只序列化首字母大写的导出字段，忽略小写非导出字段。

例如：

```go
type Request struct {
    ID       int64
    Profile  pb.PlayerProfile
    Profiles map[int64]pb.PlayerProfile
}
```

以上字段均由 Origin 静态结构体编解码器处理。`map[int64]pb.PlayerProfile` 按普通 Map 编码，不要求改为 `map[int64]*pb.PlayerProfile`，也不调用 Protobuf 指针消息接口。

现代 Protobuf 生成代码中的 `state`、`sizeCache` 和 `unknownFields` 等小写内部字段不会进入 Origin 线协议。由此也带来明确边界：Protobuf 未知字段不会经过一次 Origin Native RPC 往返得到保留。

同一个 Protobuf 生成类型可以有两种传输表示：

- 用于 Origin Native TCP/NATS 时，使用 Origin 结构体二进制协议；
- 用于外部 gRPC 时，使用标准 Protobuf 线协议。

## 7. optional、oneof 与 Opaque API

### 7.1 optional

在 Open Struct API 中，带存在语义的 optional 字段通常表现为 Go 指针。Origin 按普通指针处理，因此可以区分“没有赋值”和“明确赋值为零”，不需要增加 Protobuf 专用逻辑。

### 7.2 oneof

Protobuf Open Struct API 的 oneof 通常生成接口字段和包装类型。第一版 Origin 静态结构体编码不支持接口动态类型，因此不支持 oneof。

`origin-gen` 发现 oneof 对应的接口字段时必须生成失败并报告字段路径，不能静默忽略该字段。

### 7.3 Opaque API

Opaque API 会隐藏 Protobuf 消息的逻辑字段，并通过 Getter、Setter、Has 和 Builder 操作。Origin Native RPC 的普通结构体模式只处理导出字段，因此第一版不支持 Opaque API。用于 Origin Native RPC 的 Protobuf Go 代码必须生成成 Open API 或 Hybrid API。

使用 Edition 2024 时，可以在 `.proto` 文件中显式选择 Open API：

```proto
edition = "2024";

package player;

import "google/protobuf/go_features.proto";

option go_package = "game/pb";
option features.(pb.go).api_level = API_OPEN;

message PlayerProfile {
  string name = 1;
  int32 level = 2;
}
```

也可以在生成命令中统一指定：

```shell
protoc --go_out=. --go_opt=default_api_level=API_OPEN player.proto
```

如果 `origin-gen` 发现某个类型的指针实现现代 `proto.Message`，其 Protobuf 描述信息包含逻辑字段，但对应 Go 结构体没有可序列化的导出逻辑字段，应判定为 Opaque API 并在生成阶段报错。描述信息本身不包含字段的空消息仍是合法的空结构体，不能误报。例如：

```text
cannot generate Origin codec for pb.PlayerProfile

reason:
  the type is a Protobuf message using Opaque API
  no exported logical fields are available

solutions:
  1. generate this message with API_OPEN or API_HYBRID
  2. provide a custom Origin codec
  3. do not use this type in Origin Native RPC
```

禁止把 Opaque 消息静默编码为空结构体，也禁止延迟到运行时才失败。

官方参考：

- [Protobuf Go Opaque API 迁移说明](https://protobuf.dev/reference/go/opaque-migration/)
- [Protobuf Go API level 说明](https://protobuf.dev/reference/go/go-generated-opaque/#api-level)

## 8. 低延迟约束

所有 RPC 共用以下底层要求：

- 客户端、服务端路由和编解码代码均在编译前生成；
- 热路径不使用反射、JSON 和字符串方法查找；
- 基础类型使用专用快速编码；
- 尽可能预估消息大小并向已有缓冲区追加；
- 控制内存分配和数据复制；
- 小消息默认不压缩；
- 不自动合并请求，避免批处理增加等待时间；
- 使用有界队列、超时和背压，避免负载升高时延迟无限累积；
- 未显式设置 Deadline 的 RPC 使用可配置的分级默认值，并以 Origin 内置 `15s` 作为最终兜底；
- TCP 和 NATS 分别进行基准测试，不能假定两者延迟相同。

性能验证至少记录：

- 编码与解码耗时；
- 每次操作的内存分配次数和字节数；
- 请求排队时间；
- RPC 往返时间；
- RPC 超时数量，并区分显式 Deadline、Service 默认值、Node 默认值和内置 `15s` 的来源；
- P50、P95 和 P99 延迟；
- 不同消息大小下的吞吐量。

## 9. 生成期失败原则

对于 oneof、Opaque API、未支持的接口类型或其他无法静态编码的类型，`origin-gen` 必须：

1. 在生成阶段终止；
2. 输出 RPC 方法、参数、结构体和字段的完整路径；
3. 说明不支持原因；
4. 给出可执行的修改建议；
5. 不使用反射、JSON 或空对象作为隐式回退方案。

## 10. 本设计的取舍

本设计优先获得一致的 Go 开发体验和低延迟静态代码。代价是 Origin Native RPC 不保留 Protobuf 未知字段，不支持 oneof 和 Opaque API，也不产生可供其他语言直接解析的 Protobuf 字节。跨语言调用由显式的 gRPC 适配插件负责。

该边界保持 Origin Native RPC 实现精简，避免同时维护普通结构体编码、Protobuf 嵌套编码和容器特殊规则三套逻辑。
