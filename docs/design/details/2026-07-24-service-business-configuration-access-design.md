# Origin v3 Service 业务配置访问设计

> 文档类型：详细设计储备

## 1. 文档状态与范围

- 状态：本文范围内的方案已确认
- 确认日期：2026-07-24
- 适用版本：Origin v3

本文只定义 Service 和所属 Module 读取业务配置的接口与基础行为。

业务配置包括：

- Service 容量、周期和业务开关；
- Module 使用的存档、排行、消息队列等参数；
- 项目为具体 Service 定义的其他参数。

以下相对独立的问题不在本文确定：

- Node、RPC、服务发现、Transport 和 TimerEngine 等框架配置；
- 配置文件格式和目录组织的最终清单；
- 模板 Service 实例的配置继承和覆盖规则；
- 公共 Service 配置与 Node 专属配置的覆盖或合并规则；
- 环境变量、命令行参数和远端配置中心的优先级；
- 未知字段是否按严格模式报错；
- 默认值、必填字段和业务校验机制；
- 配置热更新。

Module 如何取得所属 Service 的配置能力，同时遵守 [Origin v3 Module 生命周期与运行模型设计](./2026-07-24-module-lifecycle-and-runtime-design.md) 中的静态树和强类型依赖规则。配置解析错误遵守 [Origin v3 统一错误码设计](./2026-07-24-unified-error-code-design.md)。模板实例的名称和基础加载规则见 [Origin v3 Service 模板设计](./2026-07-24-service-template-design.md)。

## 2. 设计目标

1. 保留 v2 同时支持“读取单个字段”和“解析整个结构体”的使用习惯。
2. Service 与其 Module 读取同一份 Service 业务配置。
3. 不要求业务直接操作 `map[string]any` 或编写脆弱的类型断言。
4. 字段不存在、目标参数错误和类型不匹配时返回明确错误，不发生 panic。
5. 配置只加载和标准化一次，不在每次读取时重新解析完整文件。
6. 完整结构体解析不重复执行 v2 的 `map -> JSON Marshal -> JSON Unmarshal` 全流程。
7. 不为业务配置访问引入 Viper、mapstructure 等额外第三方依赖。
8. 配置访问主要服务于 `OnInit`，不为尚未证明存在的运行热路径增加复杂缓存。

## 3. v2 现状与取舍

v2 为 Service 提供：

```go
GetServiceCfg() interface{}
ParseServiceCfg(cfg interface{}) error
```

其中值得保留的是：

- Service 启动时已经取得属于自己的配置子树；
- 业务可以只读取少量字段；
- 业务也可以把完整配置解析到 Go 结构体；
- Module 可以通过所属 Service 使用同一份配置。

v3 不保留以下问题：

- `GetServiceCfg()` 返回 `interface{}`，业务大量断言为 `map[string]interface{}`；
- YAML 数值经无类型解析后经常表现为不直观的数值类型；
- 字段断言错误容易直接 panic；
- `ParseServiceCfg` 每次都把完整配置 Map 重新 Marshal，再 Unmarshal；
- 接口名继续使用不必要的 `Cfg` 缩写；
- Module 直接依赖 Service 的内部可变配置对象。

## 4. 配置所有权

框架在创建 Service 时，为它绑定唯一的业务配置视图。

该配置视图：

- 归 Service 所有；
- 由 Service 和全部 Module 共享；
- 在 `Service.OnInit` 和 `Module.OnInit` 前已经可用；
- 在 Service 生命周期内默认只读；
- 不向业务暴露可修改的内部 Map 或解析器对象；
- 在 Service 释放后由框架清除引用。

Module 不是独立配置单元。Node 配置仍然只声明 Service，Module 配置继续放在所属 Service 的配置子树中。

## 5. 公共接口

Service 配置能力采用：

```go
type IServiceConfig interface {
    GetServiceConfig(path string, dst any) error
    ParseServiceConfig(dst any) error
}
```

`IService` 组合 `IServiceConfig`：

```go
type IService interface {
    IServiceConfig

    // 其他 Service 能力
}
```

业务 Service 可以直接调用这两个方法。Module 通过所属 Service 提供的接口使用相同能力：

```go
func (m *MongoPersistModule) OnInit() error {
    var config MongoConfig
    return m.Service().GetServiceConfig("SaveMongo", &config)
}
```

`Service()` 只返回该 Module 已绑定的所属 Service 接口，不进行 Node、ServiceName、Module ID、名称或 Go 类型查找。Module 仍然不能遍历整棵 Module 树。

## 6. 读取单个字段

接口：

```go
GetServiceConfig(path string, dst any) error
```

示例配置：

```yaml
SaveMongo:
  URL: mongodb://127.0.0.1:27017
  RetryCount: 3
Zones:
  - 1
  - 2
```

读取标量：

```go
var retryCount int

if err := s.GetServiceConfig(
    "SaveMongo.RetryCount",
    &retryCount,
); err != nil {
    return err
}
```

读取一个子结构：

```go
var mongo MongoConfig

if err := s.GetServiceConfig("SaveMongo", &mongo); err != nil {
    return err
}
```

读取整个 Slice：

```go
var zones []int

if err := s.GetServiceConfig("Zones", &zones); err != nil {
    return err
}
```

首版字段路径采用简单的点分对象路径：

```text
SaveMongo.RetryCount
```

规则如下：

- 每一段按配置中的字段名精确匹配；
- 空路径非法；
- 空路径段非法，例如 `SaveMongo..URL`；
- 首版不提供数组下标、通配符或转义语法；
- 需要读取数组时，读取整个数组字段并解析到 Slice；
- 字段名本身包含 `.` 时不能通过字段路径读取，应改用完整结构体或上级子结构解析；
- 字段不存在返回错误；
- 字段存在但不能解析为目标类型时返回错误；
- `dst` 必须是非 nil 指针。

接口不返回内部原始值，调用方不能通过修改 Map、Slice 或解析节点改变 Service 的配置视图。

## 7. 解析完整配置

接口：

```go
ParseServiceConfig(dst any) error
```

示例：

```go
type MongoConfig struct {
    URL        string `json:"URL"`
    RetryCount int    `json:"RetryCount"`
}

type PlayerServiceConfig struct {
    MaxPlayers int         `json:"MaxPlayers"`
    SaveMongo  MongoConfig `json:"SaveMongo"`
}

func (s *PlayerService) OnInit() error {
    var config PlayerServiceConfig

    if err := s.ParseServiceConfig(&config); err != nil {
        return err
    }

    s.config = config
    return nil
}
```

规则如下：

- `dst` 必须是非 nil 指针；
- 可以解析到结构体、Map、Slice 或基础类型指针；
- 业务结构体只处理可导出字段；
- 首版保持 v2 的 `json` Tag 使用习惯；
- 解析失败返回错误，不允许部分解析后按成功处理；
- 是否拒绝未知配置字段、如何处理默认值和必填项，在后续配置校验设计中确定。

配置解析只负责类型转换。数据库地址是否为空、人数是否大于零等业务合法性，仍由 Service 或 Module 的 `OnInit` 返回错误。

## 8. 参数与错误

以下情况必须返回错误：

- Service 没有业务配置；
- `path` 为空或格式非法；
- 字段不存在；
- `dst == nil`；
- `dst` 不是指针；
- `dst` 是 nil 指针；
- 配置值与目标类型不兼容；
- 内部标准化配置损坏；
- Service 已经完成释放，配置视图失效。

API 不通过 panic 报告普通配置错误，也不静默使用目标类型零值代替读取失败。

错误遵守 Origin 统一错误码和本地 cause 规则。配置专用错误码随完整配置系统设计统一登记，本文不重复建立错误体系。

## 9. 内部表示与性能

框架加载配置时，为每个 Service 建立只读配置视图。实现至少保存：

- 用于字段路径查找的标准化配置树；
- 用于完整结构体解析的标准化编码结果或等价只读表示。

字段读取流程：

1. 沿标准化树定位字段；
2. 只转换目标字段或子结构；
3. 不重新读取配置文件；
4. 不重新解析整个 Service 配置。

完整结构体解析直接使用已经标准化的结果，不再像 v2 一样先把整个运行时 Map 重新 Marshal。

配置访问默认发生在 `OnInit`：

- 不创建 goroutine；
- 不需要业务热路径锁；
- 不使用全局可变配置 Map；
- 不要求为每个字段建立提前生成的访问器；
- 不在没有基准依据时加入反射缓存、对象池或代码生成。

如果项目确实在业务热路径频繁读取配置，应在 `OnInit` 解析到 Service 或 Module 的强类型字段，而不是每次通过字符串路径查询。

## 10. Service 与 Module 示例

```yaml
PlayerService:
  MaxPlayers: 5000
  SaveMongo:
    URL: mongodb://127.0.0.1:27017
    RetryCount: 3
```

Service 解析完整结构：

```go
type PlayerService struct {
    origin.Service

    config  PlayerServiceConfig
    persist *MongoPersistModule
}

func (s *PlayerService) OnInit() error {
    if err := s.ParseServiceConfig(&s.config); err != nil {
        return err
    }

    s.persist = &MongoPersistModule{}
    return s.AddModule(s.persist)
}
```

Module 读取自己的子结构：

```go
type MongoPersistModule struct {
    origin.Module

    config MongoConfig
}

func (m *MongoPersistModule) OnInit() error {
    return m.Service().GetServiceConfig(
        "SaveMongo",
        &m.config,
    )
}
```

Service 和 Module 读取的是同一份只读 Service 配置，不产生 Module 独立配置层。

## 11. 测试要求

实现阶段至少覆盖：

1. Service 读取顶层基础字段；
2. Service 读取嵌套字段；
3. Service 读取完整子结构；
4. Service 读取完整 Slice 和 Map；
5. Service 解析完整配置结构体；
6. Module 通过所属 Service 读取同一配置；
7. 空路径、连续点和不存在字段返回错误；
8. 数组下标、通配符和转义路径被拒绝；
9. nil、非指针和 nil 指针目标返回错误；
10. 类型不匹配返回错误且不 panic；
11. Service 没有配置时返回错误；
12. Service 释放后读取配置被拒绝；
13. 业务无法通过返回值修改框架内部配置树；
14. 字段读取不重新解析完整文件；
15. 完整结构体解析不重复执行完整 Map Marshal；
16. 高频路径建议能够通过一次结构体解析消除字符串查找。

## 12. 已确认的取舍

Origin v3 Service 业务配置访问首版最终采用：

- 业务配置以 Service 为单位；
- Module 不建立独立配置层；
- Service 和 Module 共享同一份只读配置视图；
- 保留读取单个字段和解析完整结构体两种方式；
- 单字段接口为 `GetServiceConfig(path, dst)`；
- 完整解析接口为 `ParseServiceConfig(dst)`；
- 两个接口都使用非 nil 目标指针；
- 不公开 `GetServiceCfg() any`；
- 不要求业务操作 `map[string]any`；
- Module 通过所属 Service 接口使用相同配置能力；
- 字段读取使用简单点分对象路径；
- 配置加载和标准化只执行一次；
- 完整结构解析不重复执行 v2 的完整 Map Marshal；
- 不新增 Viper、mapstructure 等第三方依赖；
- 推荐在 `OnInit` 把配置解析到强类型字段，业务热路径不反复查字符串路径。

## 13. 后续讨论

1. 结构体解析是否默认拒绝未知字段；
2. 默认值、必填字段和业务校验的职责边界；
3. JSON、YAML 和其他配置格式的保留范围；
4. 公共 Service 配置与 Node 专属配置的覆盖或合并规则；
5. 环境变量展开和优先级；
6. 配置热更新是否属于 v3 首版。
