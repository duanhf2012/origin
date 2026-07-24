# Origin v3 模板 Service 设计

## 1. 文档状态与范围

- 状态：本文范围内的方案已确认
- 确认日期：2026-07-24
- 适用版本：Origin v3

本文记录模板 Service 的配置语法、运行时名称、内部规范化形式和首版校验规则。

以下问题不属于本文范围，后续分别讨论：

- 模板构造器的注册 API；
- 模板实例的独立配置、继承与覆盖规则；
- 模板实例的构造、启动、停止和失败处理；
- RPC 如何选择或广播到模板实例。

## 2. 背景

Origin v2 允许在 Node 的 Service 列表中使用以下格式，从同一个模板构造多个具有不同名称的 Service：

```text
实际ServiceName:模板名称
```

v3 继续保留这种简洁用法。配置文件面向开发者保持紧凑，配置加载阶段再把字符串解析为明确的内部结构，避免运行时反复拆分和解释字符串。

## 3. 设计目标

1. 保留 v2 已有的模板 Service 使用习惯。
2. 允许一个 Node 从同一模板创建多个 Service 实例。
3. 每个模板实例使用独立、明确的运行时 `ServiceName`。
4. 模板名称只负责选择构造逻辑，不充当运行时实例身份。
5. 配置错误在 Node 启动前一次性发现。
6. 热路径不解析模板配置字符串。

## 4. 名称与身份

### 4.1 ServiceName

`ServiceName` 是 Service 实例对运行时、服务发现和 RPC 路由公开的实际名称。

模板实例的身份仍遵循统一规则：

```text
NodeID + ServiceName
```

v3 首版不为模板实例增加可配置的 `ServiceID`。

### 4.2 TemplateName

`TemplateName` 用于查找已经注册的模板构造器。它描述实例如何创建，不是实例的运行时名称，也不能代替 `ServiceName` 参与实例身份判定。

多个模板实例可以使用相同的 `TemplateName`，但在同一个 Node 内必须使用不同的 `ServiceName`。

## 5. 配置格式

Node 继续直接配置要生效的 Service。普通 Service 使用名称，模板 Service 使用 `实际ServiceName:模板名称`：

```yaml
nodes:
  - id: game-1
    services:
      - PlayerService
      - scene-1001:SceneService
      - scene-1002:SceneService
```

含义如下：

- `PlayerService`：普通 Service；
- `scene-1001:SceneService`：使用 `SceneService` 模板创建名为 `scene-1001` 的实例；
- `scene-1002:SceneService`：使用 `SceneService` 模板创建名为 `scene-1002` 的实例。

模板实例对外公开的是 `scene-1001` 和 `scene-1002`，不是 `SceneService`。

## 6. 配置加载与内部表示

配置只在加载阶段解析一次，并规范化为统一的内部结构：

```go
type ServiceConfig struct {
    Name     string
    Template string
}
```

字段语义：

- `Name`：实际 `ServiceName`；
- `Template`：模板名称；空字符串表示普通 Service。

示例：

| 原始配置 | Name | Template |
| --- | --- | --- |
| `PlayerService` | `PlayerService` | 空 |
| `scene-1001:SceneService` | `scene-1001` | `SceneService` |

Node 构造、生命周期管理、服务发现和 RPC 注册只使用规范化后的结构，不在运行期间反复对原始字符串执行 `Split`。

## 7. 首版校验规则

配置加载阶段必须执行以下校验：

1. 一个模板配置只能包含一个 `:`。
2. `:` 左侧的实际 `ServiceName` 不能为空。
3. `:` 右侧的 `TemplateName` 不能为空。
4. 同一个 Node 内的实际 `ServiceName` 必须唯一，普通 Service 和模板实例统一参与此校验。
5. `TemplateName` 必须对应已经注册的模板构造器。
6. 普通 Service 名称和模板名称不能包含 `:`。

以下配置均为错误：

```yaml
services:
  - :SceneService
  - scene-1001:
  - scene-1001:SceneService:Extra
  - scene-1001:SceneService
  - scene-1001:OtherTemplate
```

最后两项的实际 `ServiceName` 相同，因此不能同时出现在一个 Node 中。

校验失败时必须终止当前 Node 的启动，并返回包含 Node、原始配置项和失败原因的错误；不能忽略错误或退化为普通 Service。

## 8. 与服务发现的边界

服务发现发布并识别模板实例时，仍使用实际 `ServiceName`。因此在前面的示例中，发现快照中的实例名称是：

```text
scene-1001
scene-1002
```

与 v2 一致，模板只参与实例创建。实例创建完成后，服务发现把模板实例当作普通 Service，只识别创建后的实际 `ServiceName`：

```yaml
allow_discovery:
  - services:
      - scene-1001
      - scene-1002
```

首版不增加 `templates` 筛选字段，不使用 `SceneService` 模板名匹配实例，也不发布仅供发现筛选使用的模板元数据。需要关注同一模板创建的多个实例时，配置必须逐个列出它们的实际 `ServiceName`。

这样可以让普通 Service 与模板实例共用完全相同的发现快照、筛选和事件逻辑，避免引入第二套名称语义。

服务实例身份和发现事件的统一规则见[服务发现与关注筛选设计](./2026-07-24-service-discovery-and-interest-filter-design.md)。

## 9. 测试要求

至少覆盖以下测试：

1. 普通 Service 配置解析。
2. 模板 Service 配置解析。
3. 一个模板生成多个不同名称的实例。
4. 普通 Service 与模板实例发生名称冲突。
5. 两个模板实例发生名称冲突。
6. 模板名称未注册。
7. 左侧为空、右侧为空和包含多个 `:`。
8. 规范化完成后，运行时路径不再解析原始配置字符串。
9. `allow_discovery.services` 使用实际 `ServiceName` 可以匹配模板实例。
10. `allow_discovery.services` 使用 `TemplateName` 不能匹配模板实例。

## 10. 已确认结论

1. v3 保留 v2 的 `实际ServiceName:模板名称` 配置格式。
2. 模板配置在加载阶段解析为 `Name + Template`，运行时不重复解析字符串。
3. 实际 `ServiceName` 是模板实例的公开名称和身份组成部分。
4. `TemplateName` 只选择构造逻辑，不是实例身份。
5. 同一个 Node 内所有实际 `ServiceName` 必须唯一。
6. 首版不增加用户可配置的 `ServiceID`。
7. 服务发现只使用创建后的实际 `ServiceName`，不支持按模板名称筛选。

## 11. 后续讨论

后续按以下顺序继续：

1. 模板构造器的注册 API；
2. 模板实例的配置传递与覆盖规则；
3. 模板实例与 Node 启停顺序的关系；
4. 模板实例的单目标 RPC 选择和广播规则。
