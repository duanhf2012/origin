# JSON、YAML 与拆分配置

Origin 的 `--config` 固定接收一个目录。框架递归读取其中的 `.json`、`.yml`、`.yaml`，所以
公共配置、Service 配置和每个 Node 可以分别保存，不必集中到一个大文件。

## 本例目录

```text
config/
  00-log.yaml          # 公共日志配置
  10-service.json      # 严格 JSON 的业务配置
  nodes/
    20-game-1.yaml     # 一个 Node 一个文件
    30-game-2.json     # 另一个 Node，也可使用 JSON
```

执行 `run.bat` 或 `./run.sh`。脚本先设置 `ORIGIN_TUTORIAL_REGION=cn-east`，然后一次启动
`game-1,game-2`。两个 Node 会分别输出同一份 JSON 业务配置；`game-1` 的 `region` 标签来自
环境变量，`game-2` 的标签直接写在 JSON 中。

## 合并规则

- 文件按斜杠形式的相对路径稳定排序，结果不依赖操作系统枚举顺序。
- Mapping 与 Mapping 递归补充；`nodes` 这样的 Sequence 按文件顺序追加。
- 同一路径的标量、`null` 或不一致类型重复定义会报告两处来源，不允许后文件静默覆盖。
- Sequence 不按 `id` 自动合并或去重；同一个 Node 不能拆成两个列表元素。
- `.json` 使用严格 JSON：不允许注释、尾随逗号、JSONC 或 JSON5；YAML 每个文件只允许一个
  Mapping 根文档。
- 环境变量只替换字符串值，不生成字段名或容器；变量缺失会启动失败，错误不会打印变量值。

业务结构体的 `json` Tag 同时决定 JSON 和 YAML 字段名；无 Tag 时使用 Go 字段原名，不自动
转换为 `snake_case`。本例的 `Welcome` 显式声明 `json:"welcome"`，所以两个格式都写 `welcome`。

修改文件后需要重新启动 Application；配置加载后冻结，不做运行期热更新。

对应教程：[配置应用](../../../docs/baseline/v3.0/guides/02.configuration.md)。
