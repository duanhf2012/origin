# Origin 第三版 M12 Origin 自定义静态编解码扩展设计

> 文档状态：已确认，允许实施
>
> 创建日期：2026-07-28
>
> 前置里程碑：M11 RPC 契约与代码生成

## 1. 目标

M12 在 M11 已经稳定的 RPC 契约、生成器和线格式之上，增加一种可以由项目声明的
**自定义静态 Codec**。它主要解决以下问题：

1. `time.Time`、UUID、Decimal 等不能或不适合按“普通 Go 结构体导出字段”编码的类型；
2. Protobuf Opaque API、`oneof` 等不能按 M11 嵌套结构体规则处理，但项目仍希望显式
   使用官方 Protobuf 或自有格式的类型；
3. 项目希望为自己拥有的某个具名类型替换 M11 内置表示，但不接受运行时反射、注册表和
   动态 Codec 查找；
4. Codec ID 或版本变化时，必须通过契约指纹在解码业务载荷前发现不兼容。

M12 只扩展特殊类型的静态编解码能力，不改变 M11 已有基础类型、普通结构体、容器和
Protobuf 的线格式。

## 2. 不在 M12 实现

M12 不包含：

- TCP、NATS、RequestID、远程 pending 表、重连或服务发现；
- 运行时 Codec 注册、运行时替换和按配置选择 Codec；
- JSON、反射或 `encoding.BinaryMarshaler` 隐式回退；
- 按 RPC、方法、参数或结构体字段分别选择 Codec；
- 有状态 Codec、Codec 工厂、依赖注入或 Codec 对象池；
- 流式编解码、压缩、加密和零拷贝业务借用；
- 修改 M11 内置类型的既有线格式。

这些边界保证 M12 是可以独立验证的小里程碑，不提前侵入 M13/M14 的网络实现。

## 3. 核心结论

建议 M12 采用以下单一方案：

1. 项目以带 `//origin:rpc-codec` 标记的**空结构体 Provider**声明 Codec；
2. Provider 的三个固定方法同时确定目标类型和静态调用外观；
3. `origingen` 在生成期扫描、验证和选择 Codec；
4. 生成代码直接调用具体 Provider 方法，不通过接口、Map 或反射分派；
5. 自定义 Codec 对目标具名类型全局生效，优先级高于顶层 Protobuf 和 M11 内置 Codec；
6. Codec ID、版本、目标类型和线格式版本进入完整契约指纹；
7. 一个目标类型只允许一个 Codec，一个 Codec ID 也只能属于一个目标类型；
8. 自定义值使用长度前缀形成独立边界，外层指针和容器语义仍由 M11 组合规则负责。

## 4. 为什么不把方法直接加到业务类型

不建议让业务类型直接实现 `OriginRPCSize`、`OriginRPCMarshal` 等方法，原因是：

1. `time.Time` 等第三方或标准库类型不能增加方法；
2. 编解码职责会污染业务对象的方法集合；
3. 同一个类型的 Codec ID 和版本没有自然、可靠的声明位置；
4. 方法名容易和现有业务或第三方生成代码冲突；
5. 以后替换实现时需要修改业务类型本身。

独立的无状态 Provider 可以支持外部类型，又不会在 RPC 热路径增加对象或动态分派。

## 5. 公开接口外观

### 5.1 静态 Codec 接口

`rpc` 包增加以下泛型接口，用于说明和编译期校验公开契约：

```go
type StaticCodec[T any] interface {
    Size(value *T) (int, error)
    MarshalTo(dst []byte, value *T) (int, error)
    Unmarshal(src []byte, value *T) error
}
```

接口只描述 Provider 必须具有的方法。生成代码始终调用具体 Provider，不把 Provider
转换成 `rpc.StaticCodec[T]`，因此热路径没有接口装箱和动态方法分派。

三个方法都接收 `*T`：

- `Size` 避免复制较大的结构体，并允许在计算长度时校验业务值；
- `MarshalTo` 直接写入 Origin 已经分配好的最终 Buffer；
- `Unmarshal` 直接填充生成代码创建的目标值。

`MarshalTo` 返回实际写入长度。生成代码必须验证它等于 `len(dst)`，避免错误的 Codec
把未覆盖的池内旧数据作为 RPC 载荷发送出去。

### 5.2 声明示例

```go
//origin:rpc-codec id=game.time.unixnano version=1
type TimeCodec struct{}

var _ rpc.StaticCodec[time.Time] = TimeCodec{}

func (TimeCodec) Size(value *time.Time) (int, error) {
    return 8, nil
}

func (TimeCodec) MarshalTo(dst []byte, value *time.Time) (int, error) {
    if len(dst) != 8 {
        return 0, errs.ErrRPCEncodeFailed
    }
    binary.LittleEndian.PutUint64(dst, uint64(value.UnixNano()))
    return 8, nil
}

func (TimeCodec) Unmarshal(src []byte, value *time.Time) error {
    if len(src) != 8 {
        return errs.ErrRPCRequestDecodeFailed
    }
    *value = time.Unix(0, int64(binary.LittleEndian.Uint64(src)))
    return nil
}
```

业务契约可以直接使用目标类型，不引用 Provider：

```go
//origin:rpc
type PlayerRPC interface {
    SaveLoginTime(ctx context.Context, playerID int64, at time.Time) error
}
```

生成代码静态调用 `TimeCodec{}.Size`、`TimeCodec{}.MarshalTo` 和
`TimeCodec{}.Unmarshal`。

### 5.3 Provider 限制

带标记的 Provider 必须同时满足：

1. 是已导出的具名、非泛型空结构体；
2. 三个方法全部使用值接收者；
3. 方法名和签名与 `StaticCodec[T]` 完全一致；
4. 三个方法中的 `T` 是同一个具名、非指针目标类型；
5. Provider 不包含字段，不保存配置、Buffer、锁或运行时状态；
6. Provider 位于本次 `origingen rpc` 扫描到的当前 Module 中；
7. `id` 和 `version` 合法且在整个生成范围内唯一。

要求空结构体和值接收者后，生成的 `CodecType{}` 不分配、不需要初始化，也不会形成隐藏的
跨 Application 状态。

### 5.4 目标类型限制

自定义 Codec 的目标必须是一个具名类型。首版允许：

- 具名基础类型；
- 具名数组、Slice、Map；
- 普通结构体；
- 标准库或第三方具名结构体；
- Protobuf Open、Hybrid 或 Opaque API 生成的具名消息类型。

即使声明自定义 Codec，首版仍不允许把以下运行时对象作为目标：

- `unsafe.Pointer` 或以它为底层类型的类型；
- 函数和 Channel；
- 未实例化泛型或包含类型参数的类型；
- 匿名类型；
- `interface{}`、`any` 和具名接口。

这些对象缺少稳定、唯一的跨进程值语义。M12 不允许使用自定义 Codec 绕过 RPC 类型安全
边界。

## 6. 标记格式

标记固定写在 Provider 类型的紧邻文档注释中：

```text
//origin:rpc-codec id=<codec-id> version=<positive-uint32>
```

规则如下：

1. `id` 必填，长度为 1～128 字节；
2. 首字符必须是 ASCII 字母，后续只允许 ASCII 字母、数字、点、斜线、下划线和短横线；
3. `version` 必填，范围为 `1`～`4294967295`；
4. 不允许未知选项、重复选项、引号、空值或多条 Codec 标记；
5. Codec ID 在整个扫描 Module 中全局唯一；
6. 同一个目标类型只能声明一个 Codec。

Codec ID 是协议身份，不是 Go 标识符。移动或重命名 Provider 时只要 ID、版本、目标类型
和实现线格式没有变化，契约身份可以保持稳定。

## 7. 选择优先级

`origingen` 对一个类型位置按以下顺序选择：

1. 外层指针、数组、Slice 和 Map 仍按 M11 的组合规则处理；
2. 到达一个具名值时，如果存在精确匹配的自定义 Codec，使用自定义 Codec；
3. 否则，顶层 Protobuf 使用 M11 官方 Protobuf Codec；
4. 否则，使用 M11 基础类型或普通 Go 结构体 Codec；
5. 仍无法表示时，在生成阶段报告完整字段路径并终止全部生成。

因此，自定义 Codec 可以覆盖具名基础类型、普通结构体或 Protobuf，但不会接管外层指针的
nil 标记，也不会改变 Slice/Map 的 nil、长度和元素数量表示。

不支持字段级覆盖。同一具名类型在同一构建中只有一种表示，避免同一个类型因为出现位置
不同而产生难以排查的协议差异。

## 8. 线格式

### 8.1 非指针值

每个自定义值固定编码为：

```text
uint32 little-endian payload_length
payload[payload_length]
```

`payload_length` 可以为零，但不能使用 M11 保留的 `0xffffffff` nil 标记。单个值和整条
RPC 消息仍受默认 `4M` 上限约束。

长度前缀让 Reader 可以在进入自定义 Codec 前完成边界校验，也让 Codec 只能访问当前值
自己的只读字节。

### 8.2 指针

`*T` 继续先使用 M11 一字节 presence：

```text
uint8 present
custom_value_when_present
```

nil 指针不会调用 Codec；非 nil 指针调用 `T` 对应的 Codec。

### 8.3 容器

数组、Slice 和 Map 继续使用 M11 容器头。自定义 Codec 只处理单个元素或 Key。Map 仍不
排序，不保证 payload 字节确定性，只保证往返语义。

自定义类型只有在 Go 语言本身可比较时才能作为 Map Key。生成器必须继续执行 Go 类型和
Map Key 合法性检查。

## 9. 生成代码

生成代码在 Size 阶段：

1. 直接调用具体 Provider 的 `Size(&value)`；
2. 把任意 Provider error 映射为 `CodeRPCEncodeFailed`；
3. 校验长度非负、不是 nil 标记、没有整数溢出且不超过消息上限；
4. 把四字节长度和 payload 长度加入统一 `Sizer`。

生成代码在 Marshal 阶段：

1. 再次调用 `Size`，保证取得当前值的准确长度；
2. 写入长度并从最终 Writer 取得准确大小的目标 Slice；
3. 直接调用具体 Provider 的 `MarshalTo`；
4. Provider 返回 error 或写入长度不一致时返回 `CodeRPCEncodeFailed`；
5. 请求 Buffer 的释放责任继续沿用 M11，不交给 Provider。

生成代码在 Unmarshal 阶段：

1. Reader 先读取并校验长度；
2. Reader 只把当前值的 payload Slice 借给 Provider；
3. 生成代码创建业务目标值并直接调用具体 Provider 的 `Unmarshal`；
4. 请求侧错误映射为 `CodeRPCRequestDecodeFailed`；
5. 响应侧错误映射为 `CodeRPCResponseDecodeFailed`；
6. Provider 返回后，业务结果不得引用输入 Slice。

M12 不在每次调用时查询 Codec，不创建 Provider，不调用反射，也不建立辅助 goroutine。

## 10. Buffer 所有权

Provider 必须遵守：

1. `Size` 只能读取 `value`；
2. `MarshalTo` 借用 `dst`，不得保存、释放、扩容或把它交给其他 goroutine；
3. `Unmarshal` 借用 `src`，不得保存或让业务结果中的 `[]byte`、string、Slice、Map 或
   指针继续引用它；
4. Provider 需要保存二进制数据时必须自行复制为业务独立内存；
5. Provider 不得接触 `rpc.Buffer` 或 `bufferpool`；
6. Provider 返回后，Buffer 的唯一所有权仍由 M11 Client、Runtime 或 Dispatcher 管理。

生成器无法证明自定义 `Unmarshal` 是否偷偷保存 Slice，因此文档、集成测试和 Buffer
复用测试必须共同锁定该责任。

## 11. 错误与 panic

Codec 返回的具体 error 只用于本地诊断和控制流，不跨 RPC 边界暴露其动态类型：

| 阶段 | Origin 错误 |
|---|---|
| `Size`、`MarshalTo`、长度不一致 | `CodeRPCEncodeFailed` |
| 目标请求 `Unmarshal` | `CodeRPCRequestDecodeFailed` |
| 调用方响应 `Unmarshal` | `CodeRPCResponseDecodeFailed` |

Provider 必须以返回 error 表达数据错误，不得使用 panic。M12 不为每个 Codec 调用增加
独立 `defer/recover`，避免高频路径固定承担额外开销。真正的 Codec bug 发生 panic 时，
继续由现有 Service 任务或 RPC Dispatcher panic 边界处理。

错误信息和日志不得包含完整业务 payload、密码、Token 或其他敏感内容。

## 12. Codec ID、版本和契约指纹

完整 Schema 中每个自定义类型位置必须加入：

```text
custom:<codec-id>@<version>:<canonical-target-type>
```

其中：

- Codec ID 表示协议实现身份；
- version 表示该 Codec 的线格式版本；
- canonical target type 使用完整 Go Module 导入路径和具名类型；
- Origin 自定义 Codec 线格式自身具有独立格式版本，首版固定为 `custom-v1`。

以下任一变化都会改变完整契约指纹：

- Codec ID；
- Codec version；
- 目标类型；
- Origin 自定义 Codec 线格式版本；
- 包含该类型的 RPC 方法签名或对象图位置。

只修改 Provider Go 类型名或所在包、但保持 Codec ID、版本、目标类型和线格式不变时，不应
改变 RPC 契约指纹。实现者修改线格式时必须主动增加 version；生成器无法自动理解函数体。

## 13. 冲突与诊断

`origingen` 必须在写任何文件前检查并报告：

- 标记语法错误；
- Provider 不是空结构体、未导出、含泛型或方法签名错误；
- 三个方法推导出不同目标类型；
- 目标类型不允许；
- Codec ID 重复；
- 同一目标存在多个 Codec；
- Codec 目标不可从生成代码所在包访问；
- 自定义 Codec 与方法签名组合后超过类型深度或消息边界；
- 生成导入别名冲突；
- Codec 变化后生成文件过期。

错误必须包含 Provider 完整包路径、Provider 名称、目标类型和具体原因。任一错误都阻止
全部生成文件修改。

## 14. 性能与低延迟

M12 热路径新增的固定成本只有：

1. 每个自定义值四字节长度；
2. Size 和 MarshalTo 两次具体静态方法调用；
3. Reader 的一次长度检查；
4. Codec 实现本身的计算。

实现不得加入：

- 运行时 Codec Map；
- `any` 参数；
- 反射；
- Provider 接口装箱；
- Codec 对象分配或对象池；
- 中间 payload Buffer；
- 每调用 goroutine、锁、Channel 或 Timer；
- JSON 回退。

Provider 是零大小值，直接构造比池化更简单且没有堆分配，不为它建立对象池。

## 15. 测试与 Benchmark

M12 至少覆盖：

1. `time.Time` 作为顶层参数、普通结构体字段、指针、Slice、Map Key 和 Map Value；
2. 自定义 Codec 覆盖原本可由 M11 编码的具名类型；
3. 空 payload、最大合法 payload、负长度、超长、长度溢出和截断数据；
4. Size error、Marshal error、写入长度不一致、请求 Unmarshal error 和响应
   Unmarshal error；
5. 重复 Codec ID、同类型多个 Codec、非法 Provider、非法目标和非法标记；
6. Codec ID 或版本变化导致契约指纹和生成文件变化；
7. 跨包 Provider 和目标类型；
8. `--check`、旧生成文件替换和重复生成稳定；
9. Buffer 复用后，解码结果仍独立持有数据；
10. Windows/Linux 单测、竞态检测和 Linux/macOS 交叉构建。

Benchmark 至少记录：

- 固定八字节 `time.Time` 自定义 Codec 的 `ns/op`、`B/op`、`allocs/op`；
- 自定义 Codec 与相同八字节内置整数 Codec 的差值；
- 含自定义字段的同 Node Await P50、P95、P99；
- 16B、1KB 和接近 `4M` 自定义 payload；
- 生成路径逃逸分析。

## 16. 完成标准

M12 完成必须同时满足：

1. 已确认本设计并在复核清单记录允许实施；
2. 建立独立实施计划；
3. `rpc.StaticCodec[T]`、生成器扫描、静态选择和代码生成完成；
4. M11 所有内置 Codec golden 和指纹保持不变；
5. 自定义 Codec 全部错误、边界和所有权测试通过；
6. `origingen rpc --check ./...`、`gofmt`、`go vet ./...`、`go test ./...` 和
   `go test -race ./...` 通过；
7. 完成覆盖率、Fuzz、Benchmark、逃逸分析、Windows/Linux 验收和跨平台构建；
8. 真实实现和性能数据回写本文及实施计划；
9. 形成唯一的 M12 中文里程碑提交。

## 17. 开工 Review 已确认

开发者于 2026-07-28 确认采用方案 A，并一次确认以下结论：

1. 采用空结构体 Provider，而不是把 Codec 方法加到业务类型；
2. Provider 使用 `Size`、`MarshalTo`、`Unmarshal` 三个方法；
3. 目标类型从方法签名推导，标记只声明 Codec ID 和版本；
4. 自定义 Codec 对精确具名类型全局生效，并优先于 Protobuf 和内置 Codec；
5. 不支持字段级、方法级或配置级选择；
6. 自定义值统一使用四字节长度前缀，外层指针和容器仍用 M11 规则；
7. Codec error 映射为 Origin 固定错误，Codec panic 不增加逐次 recover；
8. Provider 必须无状态、零大小，不池化；
9. 生成代码直接调用具体 Provider，热路径没有运行时注册表、反射和接口分派；
10. M12 不接入 TCP/NATS 或其他后续能力。

该 Review 已完成，允许创建 M12 实施计划并按本文范围编码。
