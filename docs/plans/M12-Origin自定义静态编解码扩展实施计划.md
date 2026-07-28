# Origin 第三版 M12 Origin 自定义静态编解码扩展实施计划

> 文档状态：已完成
>
> 创建日期：2026-07-28
>
> 对应设计：[M12 Origin 自定义静态编解码扩展设计](../design/milestones/M12-Origin自定义静态编解码扩展设计.md)

## 1. 目标

在 M11 已实现的 `rpc` 静态 Codec 和 `origingen` 生成器上，实现开发者确认的方案 A：

1. 通过带 `//origin:rpc-codec` 标记的无状态空结构体 Provider 声明自定义 Codec；
2. 从 `Size`、`MarshalTo`、`Unmarshal` 方法签名推导唯一目标具名类型；
3. 生成期完成标记、Provider、目标、ID、版本和冲突校验；
4. 自定义 Codec 对目标具名类型全局生效，并优先于 Protobuf 和 M11 内置 Codec；
5. 生成代码直接调用具体 Provider，不在热路径使用反射、注册表、Map 或接口动态分派；
6. 使用已有最终 Buffer，保持 M11 请求、响应和业务结果的所有权边界；
7. 通过同 Node RPC、错误路径、Fuzz、Benchmark 和跨平台测试独立验收。

## 2. 实施边界

M12 只修改：

- `rpc`：公开静态 Codec 形状和供生成代码使用的长度边界读写能力；
- `internal/rpcgen`：Codec 扫描、验证、选择、Schema 指纹和代码渲染；
- `tests/integration/rpcfixture`：真实自定义 Codec 和同 Node RPC 测试；
- M12 设计、实施计划、索引和复核记录。

M12 不修改 M11 内置线格式，不接入 TCP、NATS、RequestID、pendingCall、发现、路由、
压缩、流式 RPC 或运行时 Codec 替换。

## 3. 文件职责

### 3.1 `rpc`

- `static_codec.go`：`StaticCodec[T]` 公开接口；
- `codec.go`：Writer 准确取得自定义 payload 区域、Reader 读取长度边界和固定错误映射；
- 对应测试和 Benchmark：锁定零值、最大长度、截断、写入不完整和零分配基础路径。

### 3.2 `internal/rpcgen`

- `custom_codec.go`：标记扫描、Provider 方法签名推导、目标限制、ID/版本和冲突校验；
- `model.go`、`types.go`：把自定义 Codec 计划接入完整类型图和契约 Schema；
- `codec_render.go`：在递归生成入口优先产生具体 Provider 的直接调用；
- `generate.go`、`render.go`：把只读 Codec 目录传递给全 Module 渲染；
- `model_test.go`：覆盖合法跨包 Provider、非法标记、非法目标、重复声明和指纹变化。

### 3.3 集成夹具

- 使用自定义 `time.Time` Codec；
- 同一 Codec 覆盖顶层参数、结构体字段、指针、Slice、Map Key 和 Map Value；
- 覆盖自定义 Codec 替换原本可由 M11 编码的具名基础类型；
- 覆盖请求/响应错误映射、Buffer 独立所有权及生成文件检查。

## 4. 执行任务

### Task 1：建立公开静态边界

- [x] 增加 `rpc.StaticCodec[T]`，只作公开说明和编译期断言。
- [x] 增加 Writer/Reader 的准确长度 payload API，保持粘滞固定错误。
- [x] 覆盖零长度、最大长度、nil 标记、截断、重复失败和消息上限。
- [x] 保存基础 API 的分配基线。

### Task 2：扫描和验证 Codec Provider

- [x] 只识别紧邻类型声明的单条 `//origin:rpc-codec` 标记。
- [x] 严格解析 `id` 与正整数 `version`，拒绝未知、缺失或重复选项。
- [x] 要求已导出、非泛型、无字段空结构体和值接收者。
- [x] 从三个固定方法推导完全相同的具名目标类型。
- [x] 拒绝接口、函数、Channel、`unsafe.Pointer`、泛型和匿名目标。
- [x] 检查重复 Codec ID 和同一目标多个 Codec。
- [x] 任一失败不修改生成文件。

### Task 3：接入类型图和契约指纹

- [x] 自定义 Codec 优先于顶层 Protobuf 和内置 Codec。
- [x] 指针和容器先保留 M11 结构语义，再对精确具名元素使用 Codec。
- [x] Schema 写入 Codec ID、版本、目标类型和 `custom-v1` 格式。
- [x] Codec Provider Go 名称或包位置不进入指纹。
- [x] 锁定 M11 无自定义 Codec 的既有指纹和生成结果不变。

### Task 4：生成直接编解码代码

- [x] Size 阶段静态调用 Provider 并检查长度、溢出和消息上限。
- [x] Marshal 阶段直接写入最终 Buffer，并校验返回长度等于目标 Slice。
- [x] Unmarshal 阶段只借用当前 payload，按请求或响应映射固定错误。
- [x] 生成代码不把 Provider 转成接口，不建立运行时 Codec 表。
- [x] 自定义 Codec 在 Map Key、Map Value、Slice、指针和结构体字段中正确生成。

### Task 5：集成和错误测试

- [x] `time.Time` 自定义 Codec 完成真实 Await/Async/Notify/Broadcast 往返。
- [x] 覆盖嵌套、nil、空值、容器和 Map。
- [x] 覆盖 Size、MarshalTo、写入长度和 Unmarshal 错误。
- [x] 覆盖 Codec ID/版本改变、跨包 Provider、非法 Provider 和重复 Codec。
- [x] 覆盖解码结果不引用输入 Buffer。
- [x] 覆盖 `origingen rpc --check ./...` 和重复生成稳定。

### Task 6：性能和里程碑验收

- [x] 记录固定八字节自定义 Codec 与内置 `int64` 的 `ns/op`、`B/op`、`allocs/op`。
- [x] 记录含自定义字段同 Node Await 的 P50、P95、P99。
- [x] 覆盖 16B、1KB 和接近 `4M` 自定义 payload。
- [x] 执行 Fuzz、逃逸分析、覆盖率和逐函数低覆盖复核。
- [x] 执行 `gofmt`、`go vet ./...`、`go test ./...`、`go test -race ./...`。
- [x] 完成 Windows/Linux 实测及 Linux/macOS 交叉构建。
- [x] 回写真实结果，形成唯一 M12 中文提交并做提交后复验。

## 5. 实现不变量

1. 自定义 Codec 只在生成期查找，RPC 热路径没有动态目录。
2. 同一目标具名类型在一次生成中只有一种线格式。
3. Provider 是零大小无状态值，不池化也不形成包级可变状态。
4. 自定义 payload 只有一个四字节长度边界和一个最终 Buffer 区域。
5. Provider 只借用输入输出 Slice，不能保存、释放或转移 Origin Buffer。
6. Provider error 必须映射为 M11 已有固定错误码。
7. M11 无自定义 Codec 的生成文件、指纹和线格式保持不变。
8. 任一生成期错误都发生在文件写入前。

## 6. 当前状态

当前状态：**已完成。**

## 7. 实际结果

1. 新增 `rpc.StaticCodec[T]`、`Sizer.AddCustom`、`Writer.ReserveCustom`、
   `Reader.ReadCustomPayload` 和 `Reader.Reject`；
2. `origingen` 新增全 Module Provider 扫描、严格标记解析、目标推导、冲突检查、
   `custom-v1` Schema 和静态代码渲染；
3. 真实夹具覆盖 `time.Time`、具名基础类型和具名变长 Slice，并通过
   Await/Async/Notify/Broadcast 以及请求/响应全部错误阶段；
4. 生成热路径没有反射、运行时 Map、接口分派、中间 payload Buffer、Provider 分配、
   goroutine、锁、Channel 或 Timer；
5. 固定八字节自定义边界在 Windows/Linux 均为 0 B/op、0 allocs/op；
6. Linux 自定义 `time.Time` 同 Node Await 中位性能为 3.490 μs/op、约 1.10 KB/op、
   18 allocs/op，P50/P95/P99 分别为 2.785/5.961/10.559 μs；
7. Windows/Linux 的生成检查、Vet、全仓测试、竞态、Fuzz 和 Benchmark 通过，
   `linux/amd64` 与 `darwin/arm64` 交叉构建通过；
8. 详细覆盖率、Fuzz 次数、Windows/Linux 性能表和逃逸结论已回写 M12 设计文档。
